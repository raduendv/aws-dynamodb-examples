package env

import (
	"fmt"

	env "github.com/caarlos0/env/v11"
)

// Env contains runtime configuration loaded from environment variables.
type Env struct {
	HTTPServerAddress string `env:"HTTP_SERVER_ADDRESS" envDefault:":8080"`
	AWSRegion         string `env:"AWS_REGION" envDefault:"eu-west-1"`
	TableName         string `env:"TABLE_NAME" envDefault:"instant-payments-go"`
	WorkerPoolSize    int    `env:"WORKER_POOL_SIZE" envDefault:"10"`
	JobPoolSize       int    `env:"JOB_POOL_SIZE" envDefault:"100"`
}

// GetEnv parses the process environment into an Env value.
func GetEnv() (*Env, error) {
	out, err := env.ParseAs[Env]()
	if err != nil {
		return nil, fmt.Errorf("error reading environment: %w", err)
	}
	return &out, nil
}
