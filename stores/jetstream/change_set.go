package jetstream

import (
	"github.com/weegigs/wee-events-go/we"
)

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
