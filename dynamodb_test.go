package es

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "math/rand"
  "strings"
  "testing"
  "time"

  "github.com/aws/aws-sdk-go-v2/aws"
  "github.com/aws/aws-sdk-go-v2/config"
  "github.com/aws/aws-sdk-go-v2/service/dynamodb"
  "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
  "github.com/oklog/ulid/v2"
  "github.com/stretchr/testify/assert"
  "github.com/testcontainers/testcontainers-go"
  "github.com/testcontainers/testcontainers-go/wait"
)

var entropy = ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)

func createId() AggregateId {
  return AggregateId{
    Type: "go-test",
    Key:  ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String(),
  }
}

type _setup struct {
  teardown func() error
  store    *DynamoEventStore
}

func setup(ctx context.Context) (*_setup, error) {

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
    return nil, err
  }

  host, err := db.Host(ctx)
  if err != nil {
    return nil, err
  }

  port, err := db.MappedPort(ctx, "8000")
  if err != nil {
    return nil, err
  }

  customResolver := aws.EndpointResolverWithOptionsFunc(
    func(service, region string, options ...interface{}) (aws.Endpoint, error) {
      if service == dynamodb.ServiceID {
        return aws.Endpoint{
          PartitionID:   "aws",
          URL:           fmt.Sprintf("http://%s:%s", host, port),
          SigningRegion: "ap-southeast-2",
        }, nil
      }
      return aws.Endpoint{}, fmt.Errorf("unknown endpoint requested")
    },
  )

  cfg, err := config.LoadDefaultConfig(
    ctx,
    config.WithRegion("us-west-2"),
    config.WithEndpointResolverWithOptions(customResolver),
  )
  if err != nil {
    return nil, err
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
    return nil, err
  }

  store := NewEventStore(
    func() *dynamodb.Client { return client },
    *table.TableDescription.TableName,
    Marshaller(),
  )

  return &_setup{
    teardown: func() error {
      return db.Terminate(ctx)
    },
    store: store,
  }, nil
}

var TestedEvent = EventType("test:test-event")

type Tested struct {
  TestStringValue string `json:"test_string_value"`
  TestIntValue    int    `json:"test_int_value"`
}

func (Tested) EventType() EventType {
  return TestedEvent
}

func Marshaller() EventMarshaller {
  return JsonEventMarshaller(
    func(event *RecordedEvent) (any, error) {
      switch event.EventType {
      case TestedEvent:
        var v Tested
        if err := json.Unmarshal(event.Data, &v); err != nil {
          return nil, err
        }

        return v, nil
      }

      return nil, nil
    },
  )
}

func TestDynamoDBStore(t *testing.T) {
  ctx := context.Background()
  ts, err := DynamoTestStore(ctx, DefaultJsonEventMarshaller())
  if err != nil {
    t.Fatalf("failed to create test store. %+v", err)
  }

  defer ts.TearDown()

  eventStore := ts.EventStore

  loadInitial := func(t *testing.T) {
    aggregateId := createId()
    aggregate, err := eventStore.Load(
      ctx, aggregateId,
    )

    if !assert.Nil(t, err) {
      return
    }

    assert.Empty(t, aggregate.Events)
    assert.Equal(t, InitialRevision, aggregate.Revision)
    assert.EqualValues(t, aggregateId, aggregate.Id)
  }

  basePublish := func(t *testing.T) {
    event := Tested{
      TestStringValue: "test string",
      TestIntValue:    42,
    }

    aggregateId := createId()
    published, err := eventStore.Publish(ctx, aggregateId, Options(), event)
    if !assert.Nil(t, err) {
      return
    }

    assert.NotNil(t, published)

    _, err = eventStore.Remove(ctx, aggregateId)
    if !assert.Nil(t, err) {
      return
    }
  }

  loadsRevisionWithEvents := func(t *testing.T) {
    aggregateId := createId()
    event := Tested{
      TestStringValue: "test string",
      TestIntValue:    42,
    }

    published, err := eventStore.Publish(ctx, aggregateId, Options(), event)
    if !assert.Nil(t, err) {
      return
    }

    aggregate, err := eventStore.Load(
      ctx, aggregateId,
    )
    if !assert.Nil(t, err) {
      return
    }

    assert.NotEmpty(t, aggregate.Events)
    assert.Equal(t, published, aggregate.Revision)
    assert.EqualValues(t, aggregateId, aggregate.Id)

    _, err = eventStore.Remove(ctx, aggregateId)
    if !assert.Nil(t, err) {
      return
    }
  }

  lastEvent := func(id AggregateId) (*DecodedEvent, error) {
    loaded, err := eventStore.Load(ctx, id)
    if err != nil {
      return nil, err
    }

    length := len(loaded.Events)
    if length == 0 {
      return nil, errors.New("no events founds")
    }

    return &loaded.Events[length-1], nil
  }

  RevisionConflictOnInitialRevision := func(t *testing.T) {
    event := Tested{
      TestStringValue: "test string",
      TestIntValue:    42,
    }

    aggregateId := createId()
    _, err := eventStore.Publish(ctx, aggregateId, Options(), event)
    if !assert.Nil(t, err) {
      return
    }

    _, err = eventStore.Publish(ctx, aggregateId, Options(WithExpectedRevision(InitialRevision)), event)
    assert.NotNil(t, err)
    assert.Equal(t, RevisionConflict, err)

    _, err = eventStore.Remove(ctx, aggregateId)
    if !assert.Nil(t, err) {
      return
    }
  }

  RevisionConflictOnSubsequentRevision := func(t *testing.T) {
    event := Tested{
      TestStringValue: "test string",
      TestIntValue:    42,
    }

    aggregateId := createId()
    one, err := eventStore.Publish(ctx, aggregateId, Options(), event)
    if !assert.Nil(t, err) {
      return
    }

    _, err = eventStore.Publish(ctx, aggregateId, Options(), event)
    if !assert.Nil(t, err) {
      return
    }

    _, err = eventStore.Publish(ctx, aggregateId, Options(WithExpectedRevision(one)), event)
    assert.NotNil(t, err)
    assert.Equal(t, RevisionConflict, err)

    _, err = eventStore.Remove(ctx, aggregateId)
    if !assert.Nil(t, err) {
      return
    }
  }

  causation := func(t *testing.T) {
    event := Tested{
      TestStringValue: "test string",
      TestIntValue:    42,
    }

    aggregateId := createId()
    _, err := eventStore.Publish(ctx, aggregateId, Options(), event)
    if !assert.Nil(t, err) {
      return
    }

    first, err := lastEvent(aggregateId)
    if !assert.Nil(t, err) {
      return
    }

    correlationId := CorrelationID(strings.Join([]string{"event/", first.EventID.String()}, ""))

    _, err = eventStore.Publish(
      ctx,
      aggregateId,
      Options(WithCausationId(correlationId, first.EventID)),
      event,
    )
    if !assert.Nil(t, err) {
      return
    }

    second, err := lastEvent(aggregateId)
    if !assert.Nil(t, err) {
      return
    }

    assert.Equal(t, correlationId, second.Metadata.CorrelationId)
    assert.Equal(t, first.EventID, second.Metadata.CausationId)
  }

  removeEntity := func(t *testing.T) {
    event := Tested{
      TestStringValue: "test string",
      TestIntValue:    42,
    }
    aggregateId := createId()

    _, err := eventStore.Publish(ctx, aggregateId, Options(), event)
    if !assert.Nil(t, err) {
      return
    }

    count, err := eventStore.Remove(ctx, aggregateId)
    if !assert.Nil(t, err) {
      return
    }

    assert.Equal(t, 2, count)

    loaded, err := eventStore.Load(ctx, aggregateId)
    assert.Equal(t, InitialRevision, loaded.Revision)
  }

  t.Run("loads an initial Revision", loadInitial)
  t.Run("loads a Revision with events", loadsRevisionWithEvents)
  t.Run("base publish", basePublish)
  t.Run("Revision conflict on initial Revision", RevisionConflictOnInitialRevision)
  t.Run("Revision conflict on subsequent Revision", RevisionConflictOnSubsequentRevision)
  t.Run("supports causation id", causation)
  t.Run("removes details for entities", removeEntity)
}
