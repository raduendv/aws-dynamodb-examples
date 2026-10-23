package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"instantpayments/pkg/env"
)

const (
	// sdkAttemptTimeout bounds one network attempt below the overall call deadline.
	sdkAttemptTimeout = 1500 * time.Millisecond
	// sdkCallTimeout caps retries so SDK failure precedes the HTTP result deadline.
	sdkCallTimeout = 5 * time.Second
	// sdkMaxAttempts permits eight transient retries within the call deadline.
	sdkMaxAttempts = 9
	// sdkBackoffCap avoids unbounded exponential sleeps.
	sdkBackoffCap = 20 * time.Second
	// sdkGeneralBackoff quickly retries ordinary transient failures.
	sdkGeneralBackoff = 25 * time.Millisecond
	// sdkThrottleBackoff gives exhausted service capacity longer to recover.
	sdkThrottleBackoff = time.Second
	// sdkMaxConcurrency bounds sockets and response bodies held in flight.
	sdkMaxConcurrency = 200
	// sdkAcquisitionTimeout bounds waiting for a shared connection permit.
	sdkAcquisitionTimeout = 2 * time.Second
	// sdkConnectTimeout limits TCP and TLS establishment within an attempt.
	sdkConnectTimeout = 2 * time.Second
	// sdkKeepAlive retains healthy TCP connections across nearby requests.
	sdkKeepAlive = 30 * time.Second
	// sdkIdleTimeout retires unused sockets without disrupting active requests.
	sdkIdleTimeout = 90 * time.Second
	// sdkExpectContinueTimeout avoids waiting indefinitely for a provisional response.
	sdkExpectContinueTimeout = time.Second
)

// Config loads the AWS SDK configuration for the configured region.
func Config(env *env.Env) (aws.Config, error) {
	if env == nil {
		return aws.Config{}, fmt.Errorf("DynamoDB environment settings are required")
	}
	if env.DynamoDBEndpoint == "" {
		return aws.Config{}, fmt.Errorf("DYNAMODB_ENDPOINT is required")
	}
	if env.AWSRegion == "" {
		return aws.Config{}, fmt.Errorf("DYNAMODB_REGION is required")
	}
	if env.DynamoDBClientType != "high-level" && env.DynamoDBClientType != "low-level" {
		return aws.Config{}, fmt.Errorf("DYNAMODB_CLIENTTYPE must be high-level or low-level")
	}
	endpoint, err := url.ParseRequestURI(env.DynamoDBEndpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" {
		return aws.Config{}, fmt.Errorf("DYNAMODB_ENDPOINT must be an absolute http or https URL")
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(env.AWSRegion)}
	if isLocalDynamoDBHost(endpoint.Hostname()) {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIAlocal", "local", "")))
	}
	cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return aws.Config{}, err
	}
	if env.DynamoDBEndpoint != "" {
		cfg.BaseEndpoint = aws.String(env.DynamoDBEndpoint)
	}
	cfg.APIOptions = append(cfg.APIOptions, withAttemptTimeout(sdkAttemptTimeout))
	cfg.APIOptions = append(cfg.APIOptions, withOptionalResponseChecksum)
	cfg.APIOptions = append(cfg.APIOptions, withCallTimeout(sdkCallTimeout))
	cfg.RetryMaxAttempts = sdkMaxAttempts
	cfg.RetryMode = aws.RetryModeStandard
	cfg.Retryer = func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = sdkMaxAttempts
			o.MaxBackoff = sdkBackoffCap
			o.Backoff = retry.BackoffDelayerFunc(fullJitterBackoff)
			o.RateLimiter = ratelimit.None
			o.Retryables = append([]retry.IsErrorRetryable{retry.IsErrorRetryableFunc(func(err error) aws.Ternary {
				var timeout *attemptTimeoutError
				if errors.As(err, &timeout) {
					return aws.TrueTernary
				}
				return aws.UnknownTernary
			})}, o.Retryables...)
		})
	}
	cfg.HTTPClient = newBoundedHTTPClient()

	return cfg, nil
}

// fullJitterBackoff chooses bounded exponential jitter with a separate throttling base.
func fullJitterBackoff(attempt int, err error) (time.Duration, error) {
	base := sdkGeneralBackoff
	if (retry.ThrottleErrorCode{Codes: retry.DefaultThrottleErrorCodes}).IsErrorThrottle(err) == aws.TrueTernary {
		base = sdkThrottleBackoff
	}
	limit := base
	for n := 1; n < attempt && limit < sdkBackoffCap; n++ {
		limit = min(2*limit, sdkBackoffCap)
	}
	return time.Duration(rand.Int64N(int64(limit))), nil
}

// attemptTimeoutError marks an attempt deadline as retryable within the overall call deadline.
type attemptTimeoutError struct{ error }

// Unwrap exposes the original transport or middleware failure for classification.
func (e *attemptTimeoutError) Unwrap() error { return e.error }

// withAttemptTimeout bounds each SDK attempt independently of the retry loop.
func withAttemptTimeout(timeout time.Duration) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Finalize.Insert(middleware.FinalizeMiddlewareFunc("BoundedAttempt",
			func(ctx context.Context, input middleware.FinalizeInput, next middleware.FinalizeHandler) (
				middleware.FinalizeOutput, middleware.Metadata, error,
			) {
				attemptCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				out, metadata, err := next.HandleFinalize(attemptCtx, input)
				if err != nil && errors.Is(attemptCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
					err = &attemptTimeoutError{err}
				}
				return out, metadata, err
			}), "Retry", middleware.After)
	}
}

// The permit spans response-body consumption, not just receipt of headers.
type boundedHTTPClient struct {
	client             aws.HTTPClient
	slots              chan struct{}
	acquisitionTimeout time.Duration
}

// newBoundedHTTPClient shares one bounded socket pool and response-body permit budget.
func newBoundedHTTPClient() *boundedHTTPClient {
	return &boundedHTTPClient{
		client: &http.Client{Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       (&net.Dialer{Timeout: sdkConnectTimeout, KeepAlive: sdkKeepAlive}).DialContext,
			ForceAttemptHTTP2: true,
			MaxConnsPerHost:   sdkMaxConcurrency, MaxIdleConns: sdkMaxConcurrency, MaxIdleConnsPerHost: sdkMaxConcurrency,
			IdleConnTimeout: sdkIdleTimeout, TLSHandshakeTimeout: sdkConnectTimeout,
			ExpectContinueTimeout: sdkExpectContinueTimeout,
		}},
		slots:              make(chan struct{}, sdkMaxConcurrency),
		acquisitionTimeout: sdkAcquisitionTimeout,
	}
}

// Do acquires a cancellable permit retained until the response body is closed.
func (c *boundedHTTPClient) Do(request *http.Request) (*http.Response, error) {
	timer := time.NewTimer(c.acquisitionTimeout)
	defer timer.Stop()
	select {
	case c.slots <- struct{}{}:
	case <-request.Context().Done():
		return nil, request.Context().Err()
	case <-timer.C:
		return nil, fmt.Errorf("HTTP connection acquisition: %w", context.DeadlineExceeded)
	}
	var once sync.Once
	release := func() { once.Do(func() { <-c.slots }) }
	response, err := c.client.Do(request)
	if err != nil || response == nil || response.Body == nil {
		release()
	} else {
		response.Body = &permitBody{ReadCloser: response.Body, release: release}
	}
	return response, err
}

// permitBody releases a transport permit exactly once even if Close is repeated.
type permitBody struct {
	io.ReadCloser
	release  func()
	once     sync.Once
	closeErr error
}

// Close closes the underlying body and returns its first close error.
func (b *permitBody) Close() error {
	b.once.Do(func() {
		defer b.release()
		b.closeErr = b.ReadCloser.Close()
	})
	return b.closeErr
}

// The SDK currently treats an absent optional CRC32 header as an expected zero
// checksum. Local error responses omit it; retain SDK validation when present.
func withOptionalResponseChecksum(stack *middleware.Stack) error {
	const id = "DynamoDB:ResponseChecksumValidation"
	checksum, exists := stack.Deserialize.Get(id)
	if !exists {
		return nil
	}
	_, err := stack.Deserialize.Swap(id, middleware.DeserializeMiddlewareFunc(id,
		func(ctx context.Context, input middleware.DeserializeInput, next middleware.DeserializeHandler) (
			middleware.DeserializeOutput, middleware.Metadata, error,
		) {
			out, metadata, err := next.HandleDeserialize(ctx, input)
			response, ok := out.RawResponse.(*smithyhttp.Response)
			if err != nil || !ok || response.Header.Get("X-Amz-Crc32") == "" {
				return out, metadata, err
			}
			return checksum.HandleDeserialize(ctx, input, middleware.DeserializeHandlerFunc(
				func(context.Context, middleware.DeserializeInput) (middleware.DeserializeOutput, middleware.Metadata, error) {
					return out, metadata, err
				}))
		}))
	return err
}

// isLocalDynamoDBHost selects the explicitly supported Local credential and endpoint shortcut.
func isLocalDynamoDBHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "dynamodb", "dynamodb-local", "extenddb":
		return true
	default:
		return false
	}
}

// withCallTimeout bounds the entire SDK operation including backoff and all attempts.
func withCallTimeout(timeout time.Duration) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("BoundedCall",
			func(ctx context.Context, input middleware.InitializeInput, next middleware.InitializeHandler) (
				middleware.InitializeOutput, middleware.Metadata, error,
			) {
				ctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				return next.HandleInitialize(ctx, input)
			}), middleware.Before)
	}
}
