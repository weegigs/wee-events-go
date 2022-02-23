package dynamo

import (
	"context"
	"errors"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/google/wire"
	we "github.com/weegigs/wee-events-go"
)

var Live = wire.NewSet(
	DefaultAWSConfig,
	Client,
	LiveEventsTableName,
	NewEventStore,
	we.NewJsonEventMarshaller,
	wire.Bind(new(we.EventMarshaller), new(we.JsonEventMarshaller)),
	wire.Bind(new(we.EventStore), new(*DynamoEventStore)),
)

var Local = wire.NewSet(
	LocalDynamoStore,
	we.NewJsonEventMarshaller,
	wire.Bind(new(we.EventMarshaller), new(we.JsonEventMarshaller)),
	wire.Bind(new(we.EventStore), new(*DynamoEventStore)),
)

var Test = wire.NewSet(
	TestStore,
	we.NewJsonEventMarshaller,
	wire.Bind(new(we.EventMarshaller), new(we.JsonEventMarshaller)),
	wire.Bind(new(we.EventStore), new(*DynamoEventStore)),
)

func LiveEventsTableName() (EventStoreTableName, error) {
	table := os.Getenv("DYNAMODB_EVENTS_TABLE_NAME")
	if len(table) == 0 {
		return "", errors.New("DYNAMODB_EVENTS_TABLE_NAME is not set")
	}

	return EventStoreTableName(table), nil
}

func LocalEventsTableName() EventStoreTableName {
	return EventStoreTableName("wee-events")
}

func TestStore(ctx context.Context) (*DynamoEventStore, func(), error) {
	return DynamoTestStore(ctx)
}

func DefaultAWSConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx)
}
