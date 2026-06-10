package ds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"github.com/weegigs/wee-events-go/we"
)

type DynamoEventStore struct {
	db       *dynamodb.Client
	table    string
	revision *we.RevisionGenerator
	encoder  we.Encoder
}

type EventStoreTableName string

func (name EventStoreTableName) String() string {
	return string(name)
}

// NewEventStore builds a DynamoDB-backed event store. The encoder is the
// store's explicit write encoding (ENCODING-S2.R1); nil is a construction
// error, never a deferred nil-dereference at first publish (ENCODING-S2.R2).
// The change-set record is a JSON transport: a non-JSON encoder constructs
// successfully but fails every non-empty publish loudly at serialization —
// end-to-end CBOR is scoped to BLOB-backed stores (ENCODING-S3.R2).
func NewEventStore(db *dynamodb.Client, table EventStoreTableName, encoder we.Encoder) (*DynamoEventStore, error) {
	if encoder == nil {
		return nil, errors.New("ds: encoder is required")
	}

	return &DynamoEventStore{db: db, table: string(table), revision: we.NewRevisionGenerator(), encoder: encoder}, nil
}

func (ds *DynamoEventStore) Load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	events, err := ds.read(ctx, id)
	if err != nil {
		return we.Aggregate{}, err
	}

	revision := revisionFrom(events)

	return we.Aggregate{
		Id:       id,
		Revision: revision,
		Events:   events,
	}, nil
}

func (ds *DynamoEventStore) Publish(ctx context.Context, aggregateId we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	return ds.publish(ctx, aggregateId, options, events)
}

func (ds *DynamoEventStore) Remove(ctx context.Context, aggregateId we.AggregateId) (int, error) {
	return ds.remove(ctx, aggregateId)
}

func partitionKey(id we.AggregateId) string {
	return id.Encode().String()
}

func sortKey(revision we.Revision) string {
	return strings.Join([]string{`change-set#`, revision.String()}, "")
}

func latestFor(record ChangeSet) LatestRecord {
	return LatestRecord{
		PartitionKey: record.PartitionKey,
		SortKey:      "latest-revision",
		Revision:     record.Revision,
		Timestamp:    record.Timestamp,
	}
}

// KAO: Some of this could be done in parallel
func (ds *DynamoEventStore) read(ctx context.Context, id we.AggregateId) ([]we.RecordedEvent, error) {
	query := expression.Key("pk").Equal(expression.Value(partitionKey(id))).And(
		expression.Key("sk").BeginsWith("change-set#"),
	)

	projection := expression.NamesList(expression.Name("events"))

	builder := expression.NewBuilder().WithKeyCondition(query).WithProjection(projection)
	expr, err := builder.Build()
	if err != nil {
		return nil, err
	}

	var events []we.RecordedEvent
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

		var items []ChangeSet
		err = attributevalue.UnmarshalListOfMaps(out.Items, &items)
		if err != nil {
			return nil, err
		}

		// KAO: this could be done in parallel
		for _, record := range items {
			var evts []we.RecordedEvent
			if err := json.Unmarshal([]byte(record.Events), &evts); err != nil {
				return nil, fmt.Errorf("failed to unmarshal events: %w", err)
			}
			events = append(events, evts...)
		}

		start = out.LastEvaluatedKey
		if start == nil {
			break
		}
	}

	return events, nil
}

func latestCondition(revision we.Revision, expectedRevision we.Revision) expression.ConditionBuilder {
	if len(expectedRevision) == 0 {
		return expression.Name("revision").LessThan(expression.Value(revision)).Or(
			expression.AttributeNotExists(expression.Name("revision")),
		)
	}

	if expectedRevision == we.InitialRevision {
		return expression.AttributeNotExists(expression.Name("revision"))
	}

	return expression.Name("revision").Equal(expression.Value(expectedRevision))
}

type Update struct {
	AggregateId we.AggregateId
	Event       []we.DomainEvent
}

func isRevisionConflict(err error) bool {
	return err == we.RevisionConflict
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
						return we.RevisionConflict
					}
				}
			}
		}
	}

	return err
}

func (ds *DynamoEventStore) makeChangeSet(encoder we.Encoder, aggregateId we.AggregateId, options we.PublishOptions, events []we.DomainEvent) (ChangeSet, error) {
	now := time.Now()
	timestamp := we.Timestamp(now.UTC().Format(we.RFC3339Milli))

	recorded := make([]we.RecordedEvent, len(events))

	for index, event := range events {

		revision := ds.revision.NewRevision(now)
		data, err := we.MarshalToData(encoder, event)
		if err != nil {
			return ChangeSet{}, err
		}

		recorded[index] = we.RecordedEvent{
			EventID:     we.EventID(revision),
			EventType:   we.EventTypeOf(event),
			AggregateId: aggregateId,
			Data:        data,
			Revision:    revision,
			Timestamp:   timestamp,
			Metadata:    options.RecordedEventMetadata,
		}
	}

	last := recorded[len(events)-1].Revision

	evts, err := json.Marshal(recorded)
	if err != nil {
		return ChangeSet{}, err
	}

	return ChangeSet{
		PartitionKey: partitionKey(aggregateId),
		SortKey:      sortKey(last),
		Events:       string(evts),
		Timestamp:    timestamp,
		Revision:     last,
	}, nil

}

func (ds *DynamoEventStore) publish(ctx context.Context, aggregateId we.AggregateId, options we.PublishOptions, events []we.DomainEvent) error {
	if len(events) == 0 {
		// KAO - an empty publish is a no-op, not an error: "nothing to record" is a
		// normal state outcome, not an infrastructure failure (CONFORMANCE-S3).
		// The no-op deliberately short-circuits BEFORE override validation: with
		// zero events the nil encoder is never used and nothing can be
		// mis-recorded, so an empty publish is a state, not an error.
		return nil
	}

	// Resolve the publish encoder before encoding: an explicit per-publish
	// override wins, and an explicit nil override is an error that records
	// nothing (ENCODING-S2.R3, ENCODING-S2.R5).
	encoder, err := options.EncoderFor(ds.encoder)
	if err != nil {
		return fmt.Errorf("ds: %w", err)
	}

	err = retry.Do(
		func() error {
			changes, err := ds.makeChangeSet(encoder, aggregateId, options, events)
			if err != nil {
				return err
			}

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
					changes.Revision,
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

	if err != nil && !isRevisionConflict(err) {
		return fmt.Errorf("failed to publish events: %w", err)
	}

	return err
}

func revisionFrom(events []we.RecordedEvent) we.Revision {
	count := len(events)
	if count == 0 {
		return we.InitialRevision
	}

	return events[count-1].Revision
}

func (ds *DynamoEventStore) remove(ctx context.Context, id we.AggregateId) (int, error) {
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
