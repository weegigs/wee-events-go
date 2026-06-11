package ds

import (
	"context"
	"errors"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
)

func EventsTableNameFromEnvironment() (EventStoreTableName, error) {
	table := os.Getenv("EVENTS_DYNAMODB_TABLE_NAME")
	if len(table) == 0 {
		return "", errors.New("EVENTS_DYNAMODB_TABLE_NAME is not set")
	}

	return EventStoreTableName(table), nil
}

func DefaultAWSConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx)
}

func Client(cfg aws.Config) *dynamodb.Client {
	otelaws.AppendMiddlewares(&cfg.APIOptions)
	return dynamodb.NewFromConfig(cfg)
}
