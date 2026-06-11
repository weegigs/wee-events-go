package jetstream

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

// TestStorageLayoutIsCBORNotJSON pins the storage layout mechanically
// (ADR-0011 decision 4): the raw NATS message body at rest is a CBOR
// changeset envelope, never JSON text. It bypasses Load and inspects the
// stored bytes directly, so the envelope claim cannot be satisfied by a
// symmetric marshal/unmarshal pair alone.
func TestStorageLayoutIsCBORNotJSON(t *testing.T) {
	ctx := context.Background()

	store, cleanup, err := NewTestStore(ctx)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	id := makeEncodingAggregateId()
	event := encodingTestEvent{Value: "layout"}
	require.NoError(t, store.Publish(ctx, id, we.Options(), event))

	raw, err := store.stream.GetLastMsgForSubject(ctx, subject(id))
	require.NoError(t, err)
	body := raw.Data

	// No text envelope at rest: the body must not parse as JSON.
	assert.False(t, json.Valid(body), "stored envelope must not be JSON text")

	// The body CBOR-decodes into the changeset shape with real field data.
	var cs ChangeSet
	require.NoError(t, cbor.Unmarshal(body, &cs))
	require.Len(t, cs.Events, 1)

	record := cs.Events[0]
	assert.Equal(t, id, record.AggregateId)
	assert.Equal(t, we.EventTypeOf(event), record.EventType)
	assert.NotEmpty(t, record.EventID)

	// The payload bytes inside the envelope are the publisher's verbatim
	// presentation (ADR-0011 decision 2): NewTestStore encodes with JSON, so
	// the tag is JSON and the bytes decode to the published value.
	assert.Equal(t, we.JSONEncoding, record.Data.Encoding)
	var decoded encodingTestEvent
	require.NoError(t, we.UnmarshalFromData(record.Data, &decoded))
	assert.Equal(t, "layout", decoded.Value)
}
