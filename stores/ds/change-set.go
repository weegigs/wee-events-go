package ds

import (
  "encoding/json"

  "github.com/pkg/errors"

  "github.com/weegigs/wee-events-go/we"
)

type ChangeSet struct {
  PartitionKey string       `dynamodbav:"pk"`
  SortKey      string       `dynamodbav:"sk"`
  Events       string       `dynamodbav:"events"`
  Revision     we.Revision  `dynamodbav:"revision"`
  Timestamp    we.Timestamp `dynamodbav:"timestamp"`
}

type LatestRecord struct {
  PartitionKey string       `dynamodbav:"pk"`
  SortKey      string       `dynamodbav:"sk"`
  Revision     we.Revision  `dynamodbav:"revision"`
  Timestamp    we.Timestamp `dynamodbav:"timestamp"`
}

func (cs *ChangeSet) RecordedEvents() ([]we.RecordedEvent, error) {
  var evts []we.RecordedEvent
  if err := json.Unmarshal([]byte(cs.Events), &evts); err != nil {
    return nil, errors.Wrap(err, "failed to unmarshal events")
  }

  return evts, nil
}

func (cs *ChangeSet) AggregateId() (*we.AggregateId, error) {
  return we.EncodedAggregateId(cs.PartitionKey).Decode()
}
