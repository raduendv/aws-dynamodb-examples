//go:build integration || smoke

// Package testutil shares container activation and the test runtime profile
// between integration and smoke suites. It never targets a remote AWS endpoint.
package testutil

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"instantpayments/pkg/env"
)

// LocalSettings activates the test profile and starts the module's Local service
// unless an explicit loopback test endpoint selects an already-running instance.
// Shared containers are left running. Each caller must isolate and clean up its table.
func LocalSettings(t *testing.T) *env.Env {
	t.Helper()
	endpoint := os.Getenv("AWS_ENDPOINT_URL_DYNAMODB")
	if endpoint == "" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("cannot resolve shared Local fixture source path")
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
		command := exec.Command(filepath.Join(root, "scripts", "start-dynamodb-local.sh"))
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("activate DynamoDB Local: %v\n%s", err, output)
		}
		endpoint = "http://localhost:8000"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1") {
		t.Fatal("database tests require a loopback http DynamoDB Local endpoint")
	}
	t.Setenv("APP_PROFILE", "test")
	t.Setenv("DYNAMODB_ENDPOINT", endpoint)
	t.Setenv("DYNAMODB_REGION", "eu-west-1")
	t.Setenv("DYNAMODB_CLIENTTYPE", "low-level")
	t.Setenv("DYNAMODB_STREAMS_ENABLED", "false")
	settings, err := env.GetEnv()
	if err != nil {
		t.Fatalf("activate test runtime profile: %v", err)
	}
	return settings
}
