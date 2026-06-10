package ds

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weegigs/wee-events-go/we"
)

func DynamoTestStore(ctx context.Context) (*DynamoEventStore, func(), error) {

	ctr, err := testcontainers.Run(
		ctx,
		"amazon/dynamodb-local:2.5.4",
		testcontainers.WithExposedPorts("8000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("8000")),
	)
	if err != nil {
		return nil, nil, err
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, nil, err
	}

	port, err := ctr.MappedPort(ctx, "8000")
	if err != nil {
		return nil, nil, err
	}

	cfg := aws.Config{
		Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(
			"dummy", "dummy", "dummy",
		),
	}

	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	table, err := client.CreateTable(
		ctx, &dynamodb.CreateTableInput{
			TableName: aws.String("test-events"),
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
		return nil, nil, err
	}

	// Test scaffolding names the recommended interop encoding (ENCODING-S2.R4).
	store, err := NewEventStore(
		client,
		EventStoreTableName(*table.TableDescription.TableName),
		we.MakeJSONEncoder(),
	)
	if err != nil {
		return nil, nil, err
	}

	return store, func() {
		if err := ctr.Terminate(ctx); err != nil {
			panic(err)
		}
	}, nil
}
