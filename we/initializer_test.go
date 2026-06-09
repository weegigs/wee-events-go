package we

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializerFunction(t *testing.T) {
	t.Run("rejects unregistered encoding with UnknownEncodingError", func(t *testing.T) {
		// KAO - decoding now dispatches by encoding across the registered JSON
		// and CBOR decoders (feature 01); an encoding with no registered decoder
		// is rejected as unknown via the distinct UnknownEncodingError type.
		evt := RecordedEvent{
			EventType: EventTypeOf(opened{}),
			Data:      Data{Encoding: "application/xml", Data: []byte("<opened/>")},
		}
		_, err := openAccount.Initialize(&evt)

		var enc *UnknownEncodingError
		require.ErrorAs(t, err, &enc)
		assert.Equal(t, "application/xml", enc.Actual)
	})

	t.Run("constructs state from a json-encoded genesis event", func(t *testing.T) {
		data, err := MarshalToData(opened{Owner: "alice"})
		require.NoError(t, err)
		evt := RecordedEvent{EventType: EventTypeOf(opened{}), Data: data}

		state, err := openAccount.Initialize(&evt)
		require.NoError(t, err)
		require.NotNil(t, state)
		assert.Equal(t, "alice", state.Owner)
	})
}
