package jetstream

import (
	"github.com/weegigs/wee-events-go/we"
)

// The json tags below are load-bearing for the at-rest layout: the default
// CBORMarshaller (fxamacker/cbor) falls back to json tag names for its CBOR
// map keys, so renaming or removing a tag silently changes the stored
// envelope (ADR-0011 decision 4).
type EventRecord struct {
	AggregateId we.AggregateId           `json:"aggregate-id"`
	EventID     we.EventID               `json:"id"`
	EventType   we.EventType             `json:"type"`
	Data        we.Data                  `json:"data"`
	Metadata    we.RecordedEventMetadata `json:"metadata"`
}

type ChangeSet struct {
	Events []EventRecord `json:"events"`
}
