package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"

	"instantpayments/pkg/env"
)

// Config loads the AWS SDK configuration for the configured region.
func Config(env *env.Env) (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(env.AWSRegion))
	if err != nil {
		return aws.Config{}, err
	}

	return cfg, nil
}
