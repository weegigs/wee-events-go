package ds

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"

	"github.com/weegigs/wee-events-go/we"
)

// LocalDynamoStore builds a store against a local DynamoDB endpoint. The
// caller names the write encoding explicitly (ENCODING-S2.R1).
func LocalDynamoStore(ctx context.Context, encoder we.Encoder) (*DynamoEventStore, error) {
	const tableName = "wee-events"

	cfg, err := localConfig(ctx)
	if err != nil {
		return nil, err
	}

	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String("http://localhost:8000")
	})

	exists, err := tableExists(ctx, client, tableName)
	if err != nil {
		return nil, err
	}

	if !exists {
		if err := createTable(ctx, client, tableName); err != nil {
			return nil, err
		}
	}

	return NewEventStore(
		client,
		EventStoreTableName("wee-events"),
		encoder,
	)
}

func localConfig(_ context.Context) (aws.Config, error) {
	cfg := aws.Config{
		Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(
			"dummy", "dummy", "dummy",
		),
	}

	otelaws.AppendMiddlewares(&cfg.APIOptions)
	return cfg, nil
}

func tableExists(ctx context.Context, client *dynamodb.Client, name string) (bool, error) {
	required := &dynamodb.DescribeTableInput{TableName: aws.String(name)}
	description, err := client.DescribeTable(ctx, required)
	if err != nil {
		var errorType *types.ResourceNotFoundException
		if errors.As(err, &errorType) {
			return false, nil
		}
		return false, err
	}

	if description.Table.TableStatus != types.TableStatusActive {
		return false, errors.New("events table exists but is not active")
	}

	return true, nil
}

func createTable(ctx context.Context, client *dynamodb.Client, table string) error {
	log.Info().Msg("creating events table")

	_, err := client.CreateTable(
		ctx, &dynamodb.CreateTableInput{
			TableName: aws.String(table),
			AttributeDefinitions: []types.AttributeDefinition{
				{AttributeName: aws.String("pk"), AttributeType: types.ScalarAttributeTypeS},
				{AttributeName: aws.String("sk"), AttributeType: types.ScalarAttributeTypeS},
			},
			KeySchema: []types.KeySchemaElement{
				{AttributeName: aws.String("pk"), KeyType: types.KeyTypeHash},
				{AttributeName: aws.String("sk"), KeyType: types.KeyTypeRange},
			},
			BillingMode: types.BillingModePayPerRequest,
		},
	)

	if err != nil {
		return err
	}

	return waitForTable(ctx, client, table)
}

func waitForTable(ctx context.Context, client *dynamodb.Client, name string) error {
	required := &dynamodb.DescribeTableInput{TableName: aws.String(name)}
	return dynamodb.NewTableExistsWaiter(client).Wait(ctx, required, 2*time.Minute)
}
