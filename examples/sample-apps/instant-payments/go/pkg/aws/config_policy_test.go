// This suite checks SDK retry limits, jitter, deadlines, response-body lifetime,
// checksums, and connection admission using controlled HTTP transports.
package aws

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"

	"instantpayments/pkg/env"
)

// immediateRetryer preserves retry classification and limits while removing test delays.
type immediateRetryer struct{ awssdk.Retryer }

// RetryDelay eliminates backoff so attempt-count tests do not depend on wall-clock timing.
func (r immediateRetryer) RetryDelay(int, error) (time.Duration, error) { return 0, nil }

// policyConfig returns the application's Local SDK policy without contacting DynamoDB.
func policyConfig(t *testing.T) awssdk.Config {
	t.Helper()
	cfg, err := Config(&env.Env{
		AWSRegion: "eu-west-1", DynamoDBEndpoint: "http://localhost:8000", DynamoDBClientType: "high-level",
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// policyCall exercises either client with a minimal list operation under the supplied context.
func policyCall(ctx context.Context, cfg awssdk.Config, streams bool) error {
	if streams {
		_, err := DynamoDBStreams(cfg).ListStreams(ctx, &dynamodbstreams.ListStreamsInput{})
		return err
	}
	_, err := DynamoDB(cfg).ListTables(ctx, &dynamodb.ListTablesInput{})
	return err
}

func TestConfiguredRetryAttemptLimit(t *testing.T) {
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
	t.Setenv("AWS_RETRY_MODE", "adaptive")
	for _, streams := range []bool{false, true} {
		t.Run(map[bool]string{false: "DynamoDB", true: "Streams"}[streams], func(t *testing.T) {
			cfg := policyConfig(t)
			factory := cfg.Retryer
			if got := factory().MaxAttempts(); got != 9 {
				t.Fatalf("max attempts = %d, want 9", got)
			}
			cfg.Retryer = func() awssdk.Retryer { return immediateRetryer{factory()} }
			var calls atomic.Int32
			cfg.HTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{
					StatusCode: 500, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(`{"__type":"InternalServerError","message":"retry"}`)), Request: r,
				}, nil
			})}
			if err := policyCall(context.Background(), cfg, streams); err == nil || calls.Load() != 9 {
				t.Fatalf("attempts = %d, want 9; error = %v", calls.Load(), err)
			}
		})
	}
}

func TestConfiguredFullJitterBackoff(t *testing.T) {
	for _, test := range []struct {
		name    string
		attempt int
		err     error
		limit   time.Duration
	}{
		{"general first retry", 1, errors.New("transient"), 25 * time.Millisecond},
		{"general second retry", 2, errors.New("transient"), 50 * time.Millisecond},
		{"throttle first retry", 1, &smithy.GenericAPIError{Code: "ThrottlingException"}, time.Second},
		{"throttle second retry", 2, &smithy.GenericAPIError{Code: "ProvisionedThroughputExceededException"}, 2 * time.Second},
		{"general cap", 100, errors.New("transient"), 20 * time.Second},
		{"throttle cap", 100, &smithy.GenericAPIError{Code: "ThrottlingException"}, 20 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			retryer := policyConfig(t).Retryer()
			var sum time.Duration
			lowest, highest := test.limit, time.Duration(0)
			for range 1000 {
				delay, err := retryer.RetryDelay(test.attempt, test.err)
				if err != nil || delay < 0 || delay >= test.limit {
					t.Fatalf("delay %v outside [0, %v): %v", delay, test.limit, err)
				}
				sum += delay
				lowest, highest = min(lowest, delay), max(highest, delay)
			}
			mean := sum / 1000
			if lowest > test.limit/4 || highest < test.limit*3/4 || mean < test.limit*35/100 || mean > test.limit*65/100 {
				t.Fatalf("not full jitter: min=%v max=%v mean=%v limit=%v", lowest, highest, mean, test.limit)
			}
		})
	}
}

func TestConfiguredAttemptAndWholeCallDeadlines(t *testing.T) {
	for _, streams := range []bool{false, true} {
		t.Run(map[bool]string{false: "DynamoDB", true: "Streams"}[streams], func(t *testing.T) {
			cfg := policyConfig(t)
			var attempts atomic.Int32
			cfg.HTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				n := attempts.Add(1)
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > 1500*time.Millisecond || (n == 1 && remaining < 1400*time.Millisecond) {
					t.Errorf("attempt %d deadline remaining = %v, present=%v", n, remaining, ok)
				}
				start := time.Now()
				<-r.Context().Done()
				if n == 1 && time.Since(start) < 1400*time.Millisecond {
					t.Error("first attempt ended before its 1500ms timeout")
				}
				return nil, r.Context().Err()
			})}
			start := time.Now()
			err := policyCall(context.Background(), cfg, streams)
			elapsed := time.Since(start)
			if !errors.Is(err, context.DeadlineExceeded) || elapsed < 4800*time.Millisecond || elapsed > 6*time.Second {
				t.Fatalf("call timeout = %v, error = %v", elapsed, err)
			}
			if n := attempts.Load(); n < 3 || n >= 9 {
				t.Fatalf("attempts = %d, want retries bounded by overall deadline", n)
			}
		})
	}
}

// lifecycleBody detects premature context cancellation and repeated response-body closure.
type lifecycleBody struct {
	io.Reader
	ctx    context.Context
	closes atomic.Int32
}

// Read rejects consumption after the request context has been canceled.
func (b *lifecycleBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, fmt.Errorf("response context canceled before read: %w", err)
	}
	return b.Reader.Read(p)
}

// Close records closure and rejects duplicate closure or premature cancellation.
func (b *lifecycleBody) Close() error {
	if b.closes.Add(1) != 1 {
		return errors.New("response body closed twice")
	}
	if err := b.ctx.Err(); err != nil {
		return fmt.Errorf("response context canceled before close: %w", err)
	}
	return nil
}

func TestConfiguredResponseBodyLifecycleAndOptionalChecksum(t *testing.T) {
	for _, streams := range []bool{false, true} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			for _, checksum := range []string{"absent", "valid", "invalid"} {
				t.Run(fmt.Sprintf("streams=%t/status=%d/checksum=%s", streams, status, checksum), func(t *testing.T) {
					cfg := policyConfig(t)
					var mu sync.Mutex
					var messages []string
					cfg.Logger = logging.LoggerFunc(func(_ logging.Classification, format string, args ...interface{}) {
						mu.Lock()
						defer mu.Unlock()
						messages = append(messages, fmt.Sprintf(format, args...))
					})
					client := cfg.HTTPClient.(*boundedHTTPClient)
					var body *lifecycleBody
					client.client = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
						payload := "{}"
						if status == http.StatusBadRequest {
							payload = `{"__type":"ResourceNotFoundException","message":"missing test resource"}`
						}
						header := make(http.Header)
						if checksum == "valid" {
							header.Set("X-Amz-Crc32", fmt.Sprint(crc32.ChecksumIEEE([]byte(payload))))
						} else if checksum == "invalid" {
							header.Set("X-Amz-Crc32", "1")
						}
						body = &lifecycleBody{Reader: strings.NewReader(payload), ctx: r.Context()}
						return &http.Response{StatusCode: status, Header: header, Body: body, Request: r}, nil
					})}
					err := policyCall(context.Background(), cfg, streams)
					if (err != nil) != (status >= 400) {
						t.Fatalf("unexpected operation error: %v", err)
					}
					if body == nil || body.closes.Load() != 1 || len(client.slots) != 0 {
						t.Fatalf("response close count/permit leak: body=%+v permits=%d", body, len(client.slots))
					}
					if body.ctx.Err() != context.Canceled {
						t.Fatalf("attempt context not canceled after operation cleanup: %v", body.ctx.Err())
					}
					mu.Lock()
					logged := strings.Join(messages, "\n")
					mu.Unlock()
					// Only DynamoDB currently installs this SDK checksum middleware.
					wantChecksumWarning := !streams && checksum == "invalid"
					if got := strings.Contains(logged, "failed to close HTTP response body"); got != wantChecksumWarning {
						t.Fatalf("checksum warning=%t want=%t; logs=%q", got, wantChecksumWarning, logged)
					}
				})
			}
		}
	}
}

func TestPermitBodyCloseIsIdempotent(t *testing.T) {
	for _, closeError := range []error{nil, errors.New("genuine close failure")} {
		t.Run(fmt.Sprint(closeError), func(t *testing.T) {
			var closes, releases atomic.Int32
			body := &permitBody{
				ReadCloser: closeFuncBody{close: func() error {
					closes.Add(1)
					return closeError
				}},
				release: func() { releases.Add(1) },
			}
			var callers sync.WaitGroup
			for range 20 {
				callers.Add(1)
				go func() {
					defer callers.Done()
					if err := body.Close(); !errors.Is(err, closeError) {
						t.Errorf("close error=%v want=%v", err, closeError)
					}
				}()
			}
			callers.Wait()
			if closes.Load() != 1 || releases.Load() != 1 {
				t.Fatalf("close=%d release=%d, want each once", closes.Load(), releases.Load())
			}
		})
	}
}

// closeFuncBody exposes a controlled close callback for permit-release assertions.
type closeFuncBody struct{ close func() error }

// Read represents an empty response body.
func (b closeFuncBody) Read([]byte) (int, error) { return 0, io.EOF }

// Close delegates to the test's closure counter or failure.
func (b closeFuncBody) Close() error { return b.close() }

func TestConfiguredConnectionConcurrencyAndAcquisition(t *testing.T) {
	client := newBoundedHTTPClient()
	if cap(client.slots) != 200 || client.acquisitionTimeout != 2*time.Second {
		t.Fatalf("wrong connection policy: capacity=%d timeout=%v", cap(client.slots), client.acquisitionTimeout)
	}
	client.client = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})}
	responses := make([]*http.Response, 0, 200)
	defer func() {
		for _, response := range responses {
			_ = response.Body.Close()
		}
	}()
	for range 200 {
		request, _ := http.NewRequest(http.MethodGet, "http://localhost/", nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	request, _ := http.NewRequest(http.MethodGet, "http://localhost/", nil)
	start := time.Now()
	if _, err := client.Do(request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connection acquisition error = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 1900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("connection acquisition timeout = %v, want 2s", elapsed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := client.Do(request.WithContext(ctx)); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("acquisition ignored request cancellation: %v", err)
	}
	_ = responses[0].Body.Close()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("closed body did not release permit: %v", err)
	}
	_ = response.Body.Close()
	_ = response.Body.Close()
	if len(client.slots) != 199 {
		t.Fatalf("permit was leaked or released twice: %d", len(client.slots))
	}
}
