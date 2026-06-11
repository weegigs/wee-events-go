package we

import (
	"strings"
)

type EventID string

func (id EventID) String() string {
	return string(id)
}

type EventType string

func (et EventType) String() string {
	return string(et)
}

type CorrelationID string

func (id CorrelationID) String() string {
	return string(id)
}

type Payload any

// Data is the encoding-tagged payload envelope and the store-boundary
// presentation contract: Encoding instructs the reader how to decode the
// bytes presented in Data. Stores re-present these bytes verbatim with the
// original tag; how a store lays them down at rest is store-local
// (ADR-0011).
type Data struct {
	Encoding string `json:"encoding"`
	Data     []byte `json:"data"`
}

type AggregateId struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

// EncodedAggregateId is the durable reference format for aggregate
// identities: the encoded form is the reference format for documents that
// point at aggregates — the string they store when they reference one
// (IDENTITY-S2.R4). Its canonical form is `<type> ":" <key>` and its grammar
// is frozen (documents/spec/aggregate-identity.md, ADR-0010): loosening is permitted, tightening is not.
type EncodedAggregateId string

// Encode renders the canonical string form of an aggregate identity:
// `<type> ":" <key>`, byte-for-byte the wee-events.rs Display form
// (IDENTITY-S2, ADR-0010). It is infallible for every identity accepted by
// MakeAggregateId.
func (id AggregateId) Encode() EncodedAggregateId {
	return EncodedAggregateId(id.Type + ":" + id.Key)
}

func (id EncodedAggregateId) String() string {
	return string(id)
}

// Decode is the canonical parser for the encoded form (IDENTITY-S3),
// mirroring Rust's FromStr: the first ':' separates type from key, and the
// parts are validated through MakeAggregateId. Any later colon lands in the
// key part and is rejected by MakeAggregateId's charset validation; the Cut
// strategy survives future grammar loosening. When no separator exists, the
// returned error's Type field carries the raw input and Key is empty.
func (id EncodedAggregateId) Decode() (AggregateId, error) {
	aggregateType, key, found := strings.Cut(string(id), ":")
	if !found {
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Reason: ReasonMissingSeparator}
	}
	return MakeAggregateId(aggregateType, key)
}

type DomainEvent any

func EventTypeOf(event DomainEvent) EventType {
	return EventType(NameOf(event))
}

type RecordedEventMetadata struct {
	CausationId   EventID       `json:"causationId,omitempty"`
	CorrelationId CorrelationID `json:"correlationId,omitempty"`
}

type RecordedEvent struct {
	AggregateId AggregateId           `json:"aggregate"`
	Revision    Revision              `json:"revision"`
	EventID     EventID               `json:"id"`
	EventType   EventType             `json:"type"`
	Timestamp   Timestamp             `json:"timestamp"`
	Metadata    RecordedEventMetadata `json:"metadata"`
	Data        Data                  `json:"data"`
}
