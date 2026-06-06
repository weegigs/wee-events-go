package we

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializerFunction(t *testing.T) {
	t.Run("rejects non-json encoding with InvalidEncodingError", func(t *testing.T) {
		evt := RecordedEvent{
			EventType: EventTypeOf(opened{}),
			Data:      Data{Encoding: "application/xml", Data: []byte("<opened/>")},
		}
		_, err := openAccount.Initialize(&evt)

		var enc *InvalidEncodingError
		require.ErrorAs(t, err, &enc)
		assert.Equal(t, "application/json", enc.Expected)
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
