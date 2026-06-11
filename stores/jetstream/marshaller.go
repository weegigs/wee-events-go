package jetstream

import (
	"github.com/fxamacker/cbor/v2"

	"github.com/weegigs/wee-events-go/we"
)

// WithMarshaller overrides the store's changeset envelope marshaller. The
// marshaller is the store's storage-format seam (ADR-0011 decision 4): it
// chooses how the envelope is laid down at rest, a store-private layout
// choice below the we.Data presentation contract.
func WithMarshaller(marshaller Marshaller) EventStoreOption {
	return func(store *EventStore) {
		store.marshaller = marshaller
	}
}

// Marshaller serialises the changeset envelope for the NATS message body.
// It defines the store's storage format — store-private and invisible above
// the presentation contract: payload bytes pass through it verbatim,
// regardless of their encoding tag (ADR-0011 decisions 2 and 4).
type Marshaller interface {
	Unmarshal(data []byte, v any) error
	Marshal(v any) ([]byte, error)
}

// CBORMarshaller is the store's default storage encoding: NATS message
// bodies are binary-capable, so the changeset envelope is laid down as CBOR
// (fxamacker/cbor/v2) — a STORE-LOCAL layout choice below the presentation
// contract, never a constraint on payload encodings (ADR-0011 decision 4).
// Decode hardening is shared via we.HardenedCBORUnmarshal: store envelopes
// can carry foreign-writer bytes (SURFACE-S4.R2), so at-rest reads get the
// same protections as wire intake.
type CBORMarshaller struct{}

func (CBORMarshaller) Unmarshal(data []byte, v any) error {
	return we.HardenedCBORUnmarshal(data, v)
}

func (CBORMarshaller) Marshal(v any) ([]byte, error) {
	return cbor.Marshal(v)
}
