// This suite checks environment defaults, overrides, and Fx module wiring.
package env

import "testing"

func TestModule(t *testing.T) {
	if Module() == nil {
		t.Fatal("expected environment module")
	}
}

func TestGetEnvDefaultsAndOverrides(t *testing.T) {
	t.Setenv("HTTP_SERVER_ADDRESS", ":9090")
	t.Setenv("DYNAMODB_REGION", "us-east-1")
	t.Setenv("DYNAMODB_ENDPOINT", "http://localhost:8000")
	t.Setenv("DYNAMODB_TABLE_INSTANTPAYMENTS_NAME", "payments-test")
	t.Setenv("WORKER_POOL_SIZE", "3")
	t.Setenv("JOB_POOL_SIZE", "4")
	loaded, err := GetEnv()
	if err != nil {
		t.Fatalf("GetEnv failed: %v", err)
	}
	if loaded.HTTPServerAddress != ":9090" || loaded.AWSRegion != "us-east-1" || loaded.DynamoDBEndpoint != "http://localhost:8000" || loaded.TableName != "payments-test" || loaded.WorkerPoolSize != 3 || loaded.JobPoolSize != 4 {
		t.Fatalf("unexpected env %#v", loaded)
	}
}
