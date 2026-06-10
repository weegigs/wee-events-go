package ds

import (
	"encoding/json"
	"fmt"

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
		return nil, fmt.Errorf("failed to unmarshal events: %w", err)
	}

	return evts, nil
}
