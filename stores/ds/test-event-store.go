package ds

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func DynamoTestStore(ctx context.Context) (*DynamoEventStore, func(), error) {

	db, err := testcontainers.GenericContainer(
		ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "amazon/dynamodb-local",
				ExposedPorts: []string{"8000/tcp"},
				WaitingFor:   wait.ForListeningPort("8000"),
			},
			Started: true,
		},
	)
	if err != nil {
		return nil, nil, err
	}

	host, err := db.Host(ctx)
	if err != nil {
		return nil, nil, err
	}

	port, err := db.MappedPort(ctx, "8000")
	if err != nil {
		return nil, nil, err
	}

	customResolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if service == dynamodb.ServiceID {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           fmt.Sprintf("http://%s:%s", host, port),
					SigningRegion: "us-east-1",
				}, nil
			}
			return aws.Endpoint{}, fmt.Errorf("unknown endpoint requested")
		},
	)

	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion("us-east-1"),
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID: "dummy", SecretAccessKey: "dummy", SessionToken: "dummy",
				Source: "Hard-coded credentials; values are irrelevant for local DynamoDB",
			},
		}),
	)
	if err != nil {
		return nil, nil, err
	}

	client := dynamodb.NewFromConfig(cfg)

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

	store := NewEventStore(
		client,
		EventStoreTableName(*table.TableDescription.TableName),
	)

	return store, func() {
		if err := db.Terminate(ctx); err != nil {
			panic(err)
		}
	}, nil
}
