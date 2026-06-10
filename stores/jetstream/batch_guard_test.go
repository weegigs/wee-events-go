package jetstream

import (
	"context"
	"math"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

type batchGuardEvent struct {
	Value string `json:"value"`
}

func makeBatchGuardEvents(n int) []we.DomainEvent {
	events := make([]we.DomainEvent, n)
	for i := range events {
		events[i] = batchGuardEvent{Value: "x"}
	}
	return events
}

// SURFACE-S4.R1 — a changeset that would wrap the uint16 batch index is
// rejected, never published with duplicate revisions. The guard is a
// structural verdict on len(events) that fires before any field access or
// I/O, so a bare EventStore suffices: no broker, no encoder, no marshaller.
// The explicit nil encoder override doubles as an ordering probe — were the
// size guard placed after encoder resolution, this publish would surface
// we.NilEncoder instead of the batch-size error.
func TestOversizedChangesetRejected(t *testing.T) {
	store := &EventStore{}
	events := makeBatchGuardEvents(math.MaxUint16 + 2) // 65537

	err := store.Publish(context.Background(), makeEncodingAggregateId(), we.Options(we.WithEncoder(nil)), events...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jetstream: changeset exceeds maximum batch size 65536")
}

// SURFACE-S4.R1 boundary — exactly 65536 events is the largest legal
// changeset and must NOT trip the size guard. The bare store has no
// connection, so the publish still errors — but downstream, at encoder
// resolution (the deliberate nil override), proving control flowed past the
// size check.
func TestMaximumSizedChangesetPassesGuard(t *testing.T) {
	store := &EventStore{}
	events := makeBatchGuardEvents(math.MaxUint16 + 1) // 65536

	err := store.Publish(context.Background(), makeEncodingAggregateId(), we.Options(we.WithEncoder(nil)), events...)
	require.Error(t, err)
	require.ErrorIs(t, err, we.NilEncoder)
	assert.NotContains(t, err.Error(), "exceeds maximum batch size")
}

// SURFACE-S4.R2 — a stored changeset written by a foreign writer (one without
// the publish-side guard) is rejected at decode before the uint16 index loop
// can wrap and mint duplicate revisions. The guard fires after unmarshal and
// before any metadata use, so a bare store with only a marshaller suffices.
func TestOversizedStoredChangesetRejectedAtDecode(t *testing.T) {
	store := &EventStore{marshaller: JSONMarshaller{}}

	oversized := ChangeSet{Events: make([]EventRecord, math.MaxUint16+2)} // 65537
	data, err := store.marshaller.Marshal(oversized)
	require.NoError(t, err)

	recorded, err := store.decodeChangeSet(data, &jetstream.MsgMetadata{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jetstream: stored changeset exceeds maximum batch size 65536")
	assert.Nil(t, recorded)
}
