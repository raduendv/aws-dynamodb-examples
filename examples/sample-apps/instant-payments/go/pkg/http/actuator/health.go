package actuator

import (
	"context"
	"encoding/json"
	"instantpayments/pkg/env"
	stdhttp "net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// Health serves the actuator health endpoint.
type Health struct {
	ddb *dynamodb.Client
	env *env.Env
}

// Pattern returns the actuator health route.
func (*Health) Pattern() string {
	return "/actuator/health"
}

// ServeHTTP writes the actuator health status as JSON.
func (h *Health) ServeHTTP(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")

	_, err := h.ddb.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(h.env.TableName),
		Limit:     aws.Int32(1),
	})

	status := "healthy"
	if err != nil {
		status = "unhealthy"
	}

	_ = e.Encode(map[string]string{
		"status": status,
	})
}

// NewHealth constructs the actuator health handler.
func NewHealth(ddb *dynamodb.Client, env *env.Env) *Health {
	return &Health{
		ddb: ddb,
		env: env,
	}
}
