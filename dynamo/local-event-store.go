package dynamo

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	log "github.com/sirupsen/logrus"
)

func LocalDynamoStore(ctx context.Context) (*DynamoEventStore, error) {
	const tableName = "wee-events"

	cfg, err := localConfig(ctx)
	if err != nil {
		return nil, err
	}

	client := dynamodb.NewFromConfig(cfg)

	exists, err := tableExists(ctx, client, tableName)
	if err != nil {
		return nil, err
	}

	if !exists {
		createTable(ctx, client, tableName)
	}

	store := NewEventStore(
		client,
		EventStoreTableName("wee-events"),
	)

	return store, nil
}

func localConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithEndpointResolver(aws.EndpointResolverFunc(
			func(service, region string) (aws.Endpoint, error) {
				return aws.Endpoint{URL: "http://localhost:8000"}, nil
			})),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID: "dummy", SecretAccessKey: "dummy", SessionToken: "dummy",
				Source: "Hard-coded credentials; values are irrelevant for local DynamoDB",
			},
		}))
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
	log.Info("creating events table")

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
