// This suite verifies runtime profile defaults, explicit overrides, and invalid
// executor and shutdown settings while restoring process environment changes.
package env

import (
	"os"
	"strings"
	"testing"
	"time"
)

// clearRuntimeSettings isolates profile defaults from the host environment.
// All changed variables are restored when the test completes.
func clearRuntimeSettings(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"APP_PROFILE", "DOCS_ENABLED", "HTTP_ASYNC_CORE_POOL_SIZE", "HTTP_ASYNC_MAX_POOL_SIZE",
		"HTTP_ASYNC_QUEUE_CAPACITY", "HTTP_ASYNC_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT", "DYNAMODB_STREAMS_ITERATOR_TYPE",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DYNAMODB_ENDPOINT", "http://localhost:8000")
}

func TestRuntimeProfileDefaults(t *testing.T) {
	for _, test := range []struct {
		profile              string
		core, maximum, queue int
		docs                 bool
		iterator             string
	}{
		{"dev", 10, 50, 100, true, "LATEST"},
		{"test", 4, 8, 50, true, "TRIM_HORIZON"},
		{"prod", 10, 50, 100, false, "LATEST"},
	} {
		t.Run(test.profile, func(t *testing.T) {
			clearRuntimeSettings(t)
			t.Setenv("APP_PROFILE", test.profile)
			settings, err := GetEnv()
			if err != nil {
				t.Fatal(err)
			}
			if settings.AppProfile != test.profile || settings.DocsEnabled != test.docs ||
				settings.HTTPAsyncCoreSize != test.core || settings.HTTPAsyncMaxSize != test.maximum ||
				settings.HTTPAsyncQueueSize != test.queue || settings.StreamIteratorType != test.iterator ||
				settings.HTTPAsyncTimeout != 10*time.Second || settings.HTTPShutdownTimeout != 30*time.Second {
				t.Fatalf("unexpected runtime defaults: %+v", settings)
			}
		})
	}
}

func TestRuntimeProfileOverrides(t *testing.T) {
	clearRuntimeSettings(t)
	t.Setenv("APP_PROFILE", "test")
	t.Setenv("DOCS_ENABLED", "false")
	t.Setenv("HTTP_ASYNC_CORE_POOL_SIZE", "2")
	t.Setenv("HTTP_ASYNC_MAX_POOL_SIZE", "3")
	t.Setenv("HTTP_ASYNC_QUEUE_CAPACITY", "0")
	t.Setenv("HTTP_ASYNC_TIMEOUT", "500ms")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("DYNAMODB_STREAMS_ITERATOR_TYPE", "LATEST")
	settings, err := GetEnv()
	if err != nil {
		t.Fatal(err)
	}
	if settings.DocsEnabled || settings.HTTPAsyncCoreSize != 2 || settings.HTTPAsyncMaxSize != 3 ||
		settings.HTTPAsyncQueueSize != 0 || settings.HTTPAsyncTimeout != 500*time.Millisecond ||
		settings.HTTPShutdownTimeout != 2*time.Second || settings.StreamIteratorType != "LATEST" {
		t.Fatalf("explicit runtime settings lost: %+v", settings)
	}
	t.Setenv("APP_PROFILE", "prod")
	t.Setenv("DOCS_ENABLED", "true")
	settings, err = GetEnv()
	if err != nil || settings.DocsEnabled {
		t.Fatalf("prod docs must remain disabled: %+v, %v", settings, err)
	}
}

func TestNumericDefaultsPreserveEmptyEnvironmentAndZeroQueues(t *testing.T) {
	clearRuntimeSettings(t)
	for _, name := range []string{
		"HTTP_ASYNC_CORE_POOL_SIZE", "HTTP_ASYNC_MAX_POOL_SIZE", "HTTP_ASYNC_QUEUE_CAPACITY",
		"HTTP_ASYNC_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT", "WORKER_POOL_SIZE", "JOB_POOL_SIZE",
		"DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL", "DYNAMODB_TABLE_INSTANTPAYMENTS_RESERVATION_TIMEOUT",
	} {
		t.Setenv(name, "")
	}
	settings, err := GetEnv()
	if err != nil {
		t.Fatal(err)
	}
	if settings.HTTPAsyncCoreSize != 10 || settings.HTTPAsyncMaxSize != 50 || settings.HTTPAsyncQueueSize != 100 ||
		settings.HTTPAsyncTimeout != 10*time.Second || settings.HTTPShutdownTimeout != 30*time.Second ||
		settings.WorkerPoolSize != 10 || settings.JobPoolSize != 100 ||
		settings.IdempotencyTTL != 2592000 || settings.ReservationTimeout != 900 {
		t.Fatalf("empty numeric environment must retain existing defaults: %+v", settings)
	}
	t.Setenv("HTTP_ASYNC_QUEUE_CAPACITY", "0")
	t.Setenv("JOB_POOL_SIZE", "0")
	settings, err = GetEnv()
	if err != nil {
		t.Fatal(err)
	}
	if settings.HTTPAsyncQueueSize != 0 || settings.JobPoolSize != 0 {
		t.Fatalf("explicit zero queue capacities must override defaults: %+v", settings)
	}
}

func TestRuntimeSettingsValidation(t *testing.T) {
	for _, test := range []struct{ name, value, message string }{
		{"HTTP_ASYNC_CORE_POOL_SIZE", "0", "HTTP_ASYNC_CORE_POOL_SIZE"},
		{"HTTP_ASYNC_MAX_POOL_SIZE", "9", "HTTP_ASYNC_MAX_POOL_SIZE"},
		{"HTTP_ASYNC_QUEUE_CAPACITY", "-1", "HTTP_ASYNC_QUEUE_CAPACITY"},
		{"HTTP_ASYNC_TIMEOUT", "0s", "HTTP_ASYNC_TIMEOUT"},
		{"HTTP_SHUTDOWN_TIMEOUT", "-1s", "HTTP_SHUTDOWN_TIMEOUT"},
		{"HTTP_ASYNC_TIMEOUT", "invalid", "error reading environment"},
		{"WORKER_POOL_SIZE", "0", "WORKER_POOL_SIZE"},
		{"JOB_POOL_SIZE", "-1", "JOB_POOL_SIZE"},
	} {
		t.Run(test.name+"="+test.value, func(t *testing.T) {
			clearRuntimeSettings(t)
			t.Setenv(test.name, test.value)
			if settings, err := GetEnv(); err == nil || settings != nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected %s error, got %+v, %v", test.message, settings, err)
			}
		})
	}
}
