package ds

import (
	"fmt"

	"github.com/fxamacker/cbor/v2"

	"github.com/weegigs/wee-events-go/we"
)

// ChangeSet is the store's storage layout for one publish: the recorded
// events travel as CBOR bytes in a native DynamoDB binary (B) attribute
// (ADR-0011 decision 4). This is the storage encoding, below the
// presentation contract — payload bytes inside each recorded event stay
// verbatim under their own encoding tag.
type ChangeSet struct {
	PartitionKey string       `dynamodbav:"pk"`
	SortKey      string       `dynamodbav:"sk"`
	Events       []byte       `dynamodbav:"events"`
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
	if err := cbor.Unmarshal(cs.Events, &evts); err != nil {
		return nil, fmt.Errorf("failed to unmarshal events: %w", err)
	}

	return evts, nil
}
