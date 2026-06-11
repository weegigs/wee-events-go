package ds

import (
	"fmt"

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

// RecordedEvents decodes the change set's events attribute. Decode hardening
// is shared via we.HardenedCBORUnmarshal: store envelopes can carry
// foreign-writer bytes (SURFACE-S4.R2), so at-rest reads get the same
// protections as wire intake.
func (cs *ChangeSet) RecordedEvents() ([]we.RecordedEvent, error) {
	var evts []we.RecordedEvent
	if err := we.HardenedCBORUnmarshal(cs.Events, &evts); err != nil {
		return nil, fmt.Errorf("failed to unmarshal events: %w", err)
	}

	return evts, nil
}
