package es

import (
  "context"
  "errors"
  "fmt"
  "strings"
  "time"

  "github.com/avast/retry-go"
  "github.com/aws/aws-sdk-go-v2/aws"
  "github.com/aws/aws-sdk-go-v2/aws/transport/http"
  "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
  "github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
  "github.com/aws/aws-sdk-go-v2/service/dynamodb"
  "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
  "github.com/aws/smithy-go"
)

type DynamoEventStore struct {
  db         *dynamodb.Client
  table      string
  revision   *RevisionGenerator
  marshaller EventMarshaller
}

type ClientFactory func() *dynamodb.Client

func NewEventStore(factory ClientFactory, table string, marshaller EventMarshaller) *DynamoEventStore {
  client := factory()
  return &DynamoEventStore{db: client, table: table, revision: NewRevisionGenerator(), marshaller: marshaller}
}

func (ds *DynamoEventStore) Load(ctx context.Context, id AggregateId) (*Aggregate, error) {
  events, err := ds.read(ctx, &id)
  if err != nil {
    return nil, err
  }

  Revision := revisionFrom(events)

  return &Aggregate{
    Id:       id,
    Revision: Revision,
    Events:   events,
  }, nil
}

func (ds *DynamoEventStore) Publish(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...any) (Revision, error) {
  return ds.publish(ctx, &aggregateId, options, events)
}

func (ds *DynamoEventStore) Remove(ctx context.Context, aggregateId AggregateId) (int, error) {
  return ds.remove(ctx, &aggregateId)
}

// internal

type changeSet struct {
  PartitionKey string          `dynamodbav:"pk"`
  SortKey      string          `dynamodbav:"sk"`
  Events       []RecordedEvent `dynamodbav:"events"`
  Revision     Revision        `dynamodbav:"revision"`
  Timestamp    Timestamp       `dynamodbav:"timestamp"`
}

type latestRecord struct {
  PartitionKey string    `dynamodbav:"pk"`
  SortKey      string    `dynamodbav:"sk"`
  Revision     Revision  `dynamodbav:"revision"`
  Timestamp    Timestamp `dynamodbav:"timestamp"`
}

func partitionKey(id *AggregateId) string {
  return id.Encode().String()
}

func sortKey(revision Revision) string {
  return strings.Join([]string{`change-set#`, revision.String()}, "")
}

func latestFor(record *changeSet) *latestRecord {
  return &latestRecord{
    PartitionKey: record.PartitionKey,
    SortKey:      "latest-revision",
    Revision:     record.Revision,
    Timestamp:    record.Timestamp,
  }
}

// KAO: Some of this could be done in parallel
func (ds *DynamoEventStore) read(ctx context.Context, id *AggregateId) ([]DecodedEvent, error) {
  query := expression.Key("pk").Equal(expression.Value(partitionKey(id))).And(
    expression.Key("sk").BeginsWith("change-set#"),
  )

  projection := expression.NamesList(expression.Name("events"))

  builder := expression.NewBuilder().WithKeyCondition(query).WithProjection(projection)
  expr, err := builder.Build()
  if err != nil {
    return nil, err
  }

  var events []RecordedEvent
  var start map[string]types.AttributeValue
  for {
    query := &dynamodb.QueryInput{
      TableName:                 aws.String(ds.table),
      ExclusiveStartKey:         start,
      ExpressionAttributeNames:  expr.Names(),
      ExpressionAttributeValues: expr.Values(),
      KeyConditionExpression:    expr.KeyCondition(),
      ProjectionExpression:      expr.Projection(),
    }

    out, err := ds.db.Query(ctx, query)
    if err != nil {
      return nil, err
    }

    var items []changeSet
    err = attributevalue.UnmarshalListOfMaps(out.Items, &items)
    if err != nil {
      return nil, err
    }

    for _, record := range items {
      events = append(events, record.Events...)
    }

    start = out.LastEvaluatedKey
    if start == nil {
      break
    }
  }

  var decoded = make([]DecodedEvent, len(events))
  for i, event := range events {
    payload, err := ds.marshaller.Unmarshall(&event)
    if err != nil {
      return nil, err
    }

    decoded[i] = DecodedEvent{
      RecordedEvent: event,
      Payload:       payload,
    }
  }

  return decoded, nil
}

func latestCondition(Revision *Revision, expectedRevision Revision) expression.ConditionBuilder {
  if len(expectedRevision) == 0 {
    return expression.Name("revision").LessThan(expression.Value(Revision)).Or(
      expression.AttributeNotExists(expression.Name("revision")),
    )
  }

  if expectedRevision == InitialRevision {
    return expression.AttributeNotExists(expression.Name("revision"))
  }

  return expression.Name("revision").Equal(expression.Value(expectedRevision))
}

type Update struct {
  AggregateId AggregateId
  Event       []DomainEvent
}

func isRevisionConflict(err error) bool {
  return err == RevisionConflict
}

func maybeRevisionConflict(err error) error {
  var oe *smithy.OperationError
  if errors.As(err, &oe) {
    var re *http.ResponseError
    if errors.As(oe.Unwrap(), &re) {
      var tc *types.TransactionCanceledException
      if errors.As(re.Unwrap(), &tc) {
        for _, reason := range tc.CancellationReasons {
          if *reason.Code == "ConditionalCheckFailed" {
            return RevisionConflict
          }
        }
      }
    }
  }

  return err
}

func (ds *DynamoEventStore) makeChangeSet(aggregateId *AggregateId, options PublishOptions, events []any) (*changeSet, error) {
  now := time.Now()
  timestamp := Timestamp(now.UTC().Format(RFC3339Milli))

  recorded := make([]RecordedEvent, len(events))

  for index, e := range events {
    event, err := ds.marshaller.Marshall(e)
    if err != nil {
      return nil, err
    }
    if event == nil {
      return nil, errors.New(fmt.Sprintf("no marshaller found for event type %T", e))
    }

    revision := ds.revision.NewRevision(now)

    recorded[index] = RecordedEvent{
      EventID:     EventID(revision),
      EventType:   event.EventType,
      AggregateId: *aggregateId,
      Data:        event.Data,
      Revision:    revision,
      Timestamp:   timestamp,
      Metadata:    options.RecordedEventMetadata,
    }
  }

  last := recorded[len(events)-1].Revision

  return &changeSet{
    PartitionKey: partitionKey(aggregateId),
    SortKey:      sortKey(last),
    Events:       recorded,
    Timestamp:    timestamp,
    Revision:     last,
  }, nil

}

func (ds *DynamoEventStore) publish(ctx context.Context, aggregateId *AggregateId, options PublishOptions, events []any) (Revision, error) {
  if len(events) == 0 {
    return "error", errors.New("attempted to publish empty list of events")
  }

  var revision Revision

  err := retry.Do(
    func() error {
      changes, err := ds.makeChangeSet(aggregateId, options, events)
      if err != nil {
        return err
      }
      revision = changes.Revision

      latest, err := attributevalue.MarshalMap(latestFor(changes))
      if err != nil {
        return err
      }

      record, err := attributevalue.MarshalMap(changes)
      if err != nil {
        return err
      }

      condition, err := expression.NewBuilder().WithCondition(
        latestCondition(
          &changes.Revision,
          options.ExpectedRevision,
        ),
      ).Build()
      if err != nil {
        return err
      }

      write := &dynamodb.TransactWriteItemsInput{
        TransactItems: []types.TransactWriteItem{
          {
            Put: &types.Put{
              Item:                                latest,
              TableName:                           aws.String(ds.table),
              ConditionExpression:                 condition.Condition(),
              ExpressionAttributeNames:            condition.Names(),
              ExpressionAttributeValues:           condition.Values(),
              ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureNone,
            },
          },
          {
            Put: &types.Put{
              Item:      record,
              TableName: aws.String(ds.table),
            },
          },
        },
      }

      _, err = ds.db.TransactWriteItems(ctx, write)
      return maybeRevisionConflict(err)
    }, retry.RetryIf(
      func(err error) bool {
        // todo: KAO ... check for retryable errors
        return isRevisionConflict(err) && len(options.ExpectedRevision) == 0
      },
    ),
    retry.LastErrorOnly(true),
  )

  if err != nil {
    return "error", err
  }

  return revision, nil
}

func revisionFrom(events []DecodedEvent) Revision {
  count := len(events)
  if count == 0 {
    return InitialRevision
  }

  return events[count-1].Revision
}

func (ds *DynamoEventStore) remove(ctx context.Context, id *AggregateId) (int, error) {
  type record struct {
    PartitionKey string `dynamodbav:"pk"`
    SortKey      string `dynamodbav:"sk"`
  }

  query := expression.Key("pk").Equal(expression.Value(partitionKey(id)))
  projection := expression.NamesList(expression.Name("pk"), expression.Name("sk"))

  builder := expression.NewBuilder().WithKeyCondition(query).WithProjection(projection)
  expr, err := builder.Build()
  if err != nil {
    return 0, err
  }

  var count int
  var start map[string]types.AttributeValue
  for {
    query := &dynamodb.QueryInput{
      TableName:                 aws.String(ds.table),
      ExclusiveStartKey:         start,
      ExpressionAttributeNames:  expr.Names(),
      ExpressionAttributeValues: expr.Values(),
      KeyConditionExpression:    expr.KeyCondition(),
      ProjectionExpression:      expr.Projection(),
      Limit:                     aws.Int32(25),
    }

    out, err := ds.db.Query(ctx, query)
    if err != nil {
      return count, err
    }

    if len(out.Items) > 0 {
      var items []record
      err = attributevalue.UnmarshalListOfMaps(out.Items, &items)
      if err != nil {
        return count, err
      }

      var actions []types.TransactWriteItem
      for _, record := range items {
        key, err := attributevalue.MarshalMap(record)
        if err != nil {
          return count, err
        }

        actions = append(
          actions, types.TransactWriteItem{
            Delete: &types.Delete{
              Key:       key,
              TableName: aws.String(ds.table),
            },
          },
        )
      }

      write := &dynamodb.TransactWriteItemsInput{
        TransactItems: actions,
      }

      _, err = ds.db.TransactWriteItems(ctx, write)
      if err != nil {
        return count, err
      }

      count += len(items)
    }

    start = out.LastEvaluatedKey
    if start == nil {
      break
    }
  }

  return count, nil
}
