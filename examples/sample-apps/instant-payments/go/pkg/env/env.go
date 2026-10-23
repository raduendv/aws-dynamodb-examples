package env

import (
	"fmt"
	"os"
	"time"

	env "github.com/caarlos0/env/v11"
)

const (
	// DefaultHTTPCoreSize keeps baseline concurrency modest for sample deployments.
	DefaultHTTPCoreSize = 10
	// DefaultHTTPMaxSize bounds bursts after the pending queue fills.
	DefaultHTTPMaxSize = 50
	// DefaultHTTPQueueSize absorbs brief bursts without unbounded waiting requests.
	DefaultHTTPQueueSize = 100
	// DefaultHTTPTimeout keeps the result deadline above the five-second SDK call limit.
	DefaultHTTPTimeout = 10 * time.Second
	// DefaultHTTPShutdownTimeout gives requests a bounded graceful-drain period.
	DefaultHTTPShutdownTimeout = 30 * time.Second
	// DefaultIdempotencyRetention keeps accepted intents replayable for thirty days.
	DefaultIdempotencyRetention = 30 * 24 * time.Hour
	// DefaultReservationTimeout bounds abandoned holds to fifteen minutes.
	DefaultReservationTimeout = 15 * time.Minute
	// defaultWorkerPoolSize bounds business concurrency independently of HTTP admission.
	defaultWorkerPoolSize = 10
	// defaultJobPoolSize absorbs business-work bursts without an unbounded queue.
	defaultJobPoolSize = 100
	// Test profile admission limits reduce fixture resources without serializing requests.
	testHTTPCoreSize  = 4
	testHTTPMaxSize   = 8
	testHTTPQueueSize = 50
	// Idempotency retention is bounded to one hour through one year by the shared contract.
	minimumIdempotencyTTL = 3600
	maximumIdempotencyTTL = 31536000
)

// Env contains runtime configuration loaded from environment variables.
// Named defaults encode the shared operational policy: HTTP admission 10/50/100,
// processing/shutdown deadlines 10s/30s, thirty-day idempotency, fifteen-minute holds,
// and ten business workers with a hundred queued jobs. The test profile lowers
// HTTP limits and replays stream history; explicit settings always take precedence.
type Env struct {
	AppProfile          string        `env:"APP_PROFILE" envDefault:"dev"`
	DocsEnabled         bool          `env:"DOCS_ENABLED" envDefault:"true"`
	HTTPAsyncCoreSize   int           `env:"HTTP_ASYNC_CORE_POOL_SIZE"`
	HTTPAsyncMaxSize    int           `env:"HTTP_ASYNC_MAX_POOL_SIZE"`
	HTTPAsyncQueueSize  int           `env:"HTTP_ASYNC_QUEUE_CAPACITY"`
	HTTPAsyncTimeout    time.Duration `env:"HTTP_ASYNC_TIMEOUT"`
	HTTPShutdownTimeout time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT"`
	HTTPServerAddress   string        `env:"HTTP_SERVER_ADDRESS" envDefault:":8080"`
	AWSRegion           string        `env:"DYNAMODB_REGION" envDefault:"eu-west-1"`
	DynamoDBEndpoint    string        `env:"DYNAMODB_ENDPOINT"`
	DynamoDBClientType  string        `env:"DYNAMODB_CLIENTTYPE" envDefault:"high-level"`
	TableName           string        `env:"DYNAMODB_TABLE_INSTANTPAYMENTS_NAME" envDefault:"GoInstantPayments"`
	CreateResources     bool          `env:"DYNAMODB_PROVISIONING_CREATERESOURCES" envDefault:"false"`
	IdempotencyTTL      int64         `env:"DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL"`
	ReservationTimeout  int64         `env:"DYNAMODB_TABLE_INSTANTPAYMENTS_RESERVATION_TIMEOUT"`
	StreamIteratorType  string        `env:"DYNAMODB_STREAMS_ITERATOR_TYPE" envDefault:"LATEST"`
	StreamsEnabled      bool          `env:"DYNAMODB_STREAMS_ENABLED" envDefault:"true"`
	WorkerPoolSize      int           `env:"WORKER_POOL_SIZE"`
	JobPoolSize         int           `env:"JOB_POOL_SIZE"`
}

// GetEnv parses the process environment into an Env value.
func GetEnv() (*Env, error) {
	out := Env{
		HTTPAsyncCoreSize: DefaultHTTPCoreSize, HTTPAsyncMaxSize: DefaultHTTPMaxSize,
		HTTPAsyncQueueSize: DefaultHTTPQueueSize, HTTPAsyncTimeout: DefaultHTTPTimeout,
		HTTPShutdownTimeout: DefaultHTTPShutdownTimeout,
		IdempotencyTTL:      int64(DefaultIdempotencyRetention / time.Second),
		ReservationTimeout:  int64(DefaultReservationTimeout / time.Second),
		WorkerPoolSize:      defaultWorkerPoolSize, JobPoolSize: defaultJobPoolSize,
	}
	if err := env.Parse(&out); err != nil {
		return nil, fmt.Errorf("error reading environment: %w", err)
	}
	if out.AppProfile == "prod" {
		out.DocsEnabled = false
	}
	if out.AppProfile == "test" {
		if _, explicit := os.LookupEnv("DYNAMODB_STREAMS_ITERATOR_TYPE"); !explicit {
			out.StreamIteratorType = "TRIM_HORIZON"
		}
		for _, setting := range []struct {
			name     string
			value    *int
			fallback int
		}{
			{"HTTP_ASYNC_CORE_POOL_SIZE", &out.HTTPAsyncCoreSize, testHTTPCoreSize},
			{"HTTP_ASYNC_MAX_POOL_SIZE", &out.HTTPAsyncMaxSize, testHTTPMaxSize},
			{"HTTP_ASYNC_QUEUE_CAPACITY", &out.HTTPAsyncQueueSize, testHTTPQueueSize},
		} {
			if _, explicit := os.LookupEnv(setting.name); !explicit {
				*setting.value = setting.fallback
			}
		}
	}
	if out.HTTPAsyncCoreSize <= 0 || out.HTTPAsyncMaxSize < out.HTTPAsyncCoreSize || out.HTTPAsyncQueueSize < 0 {
		return nil, fmt.Errorf("HTTP_ASYNC_CORE_POOL_SIZE must be positive, HTTP_ASYNC_MAX_POOL_SIZE must be at least the core size, and HTTP_ASYNC_QUEUE_CAPACITY must be nonnegative")
	}
	if out.HTTPAsyncTimeout <= 0 || out.HTTPShutdownTimeout <= 0 {
		return nil, fmt.Errorf("HTTP_ASYNC_TIMEOUT and HTTP_SHUTDOWN_TIMEOUT must be positive durations")
	}
	if out.WorkerPoolSize <= 0 || out.JobPoolSize < 0 {
		return nil, fmt.Errorf("WORKER_POOL_SIZE must be positive and JOB_POOL_SIZE must be nonnegative")
	}
	if out.DynamoDBEndpoint == "" {
		return nil, fmt.Errorf("DYNAMODB_ENDPOINT is required")
	}
	if out.DynamoDBClientType != "high-level" && out.DynamoDBClientType != "low-level" {
		return nil, fmt.Errorf("DYNAMODB_CLIENTTYPE must be high-level or low-level")
	}
	if out.IdempotencyTTL < minimumIdempotencyTTL || out.IdempotencyTTL > maximumIdempotencyTTL {
		return nil, fmt.Errorf("DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL must be between 3600 and 31536000 seconds")
	}
	if out.ReservationTimeout <= 0 {
		return nil, fmt.Errorf("DYNAMODB_TABLE_INSTANTPAYMENTS_RESERVATION_TIMEOUT must be positive")
	}
	if out.StreamIteratorType != "LATEST" && out.StreamIteratorType != "TRIM_HORIZON" {
		return nil, fmt.Errorf("DYNAMODB_STREAMS_ITERATOR_TYPE must be LATEST or TRIM_HORIZON")
	}
	return &out, nil
}
