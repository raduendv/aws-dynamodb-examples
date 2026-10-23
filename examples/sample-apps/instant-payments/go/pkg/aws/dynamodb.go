package aws

import (
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodbstreams"
)

// DynamoDB creates a DynamoDB client from an AWS SDK configuration.
func DynamoDB(cfg aws.Config) *dynamodb.Client {
	return dynamodb.NewFromConfig(cfg)
}

// DynamoDBStreams creates a DynamoDB Streams client from an AWS SDK configuration.
func DynamoDBStreams(cfg aws.Config) *dynamodbstreams.Client {
	if cfg.BaseEndpoint != nil {
		endpoint, err := url.Parse(*cfg.BaseEndpoint)
		if err != nil || !isLocalDynamoDBHost(endpoint.Hostname()) {
			// Only DynamoDB Local serves table and Streams APIs at one endpoint.
			cfg.BaseEndpoint = nil
		}
	}
	return dynamodbstreams.NewFromConfig(cfg)
}
