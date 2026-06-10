package we

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type encoderState struct {
	Count int    `json:"count"`
	Label string `json:"label"`
}

func makeEncoderEntity() Entity[encoderState] {
	state := encoderState{Count: 7, Label: "seven"}
	return Entity[encoderState]{
		Aggregate: AggregateId{Type: "counter", Key: "a"},
		Revision:  Revision("01HX0000000000000000000000"),
		Type:      "counter",
		State:     &state,
	}
}

// Marshal is pure (SURFACE-S4.R3): each path is observable from the returned
// bytes and error alone — no writer doubles required.
func TestResourceEncoderMarshal(t *testing.T) {
	t.Run("happy path emits state fields and identity metadata", func(t *testing.T) {
		body, err := NewResourceEncoder[encoderState]().Marshal(makeEncoderEntity())
		require.NoError(t, err)

		var resource map[string]any
		require.NoError(t, json.Unmarshal(body, &resource))
		assert.Equal(t, "counter:a", resource["$id"], "canonical colon form via Encode()")
		assert.Equal(t, "counter", resource["$type"])
		assert.Equal(t, "01HX0000000000000000000000", resource["$revision"])
		assert.Equal(t, float64(7), resource["count"])
		assert.Equal(t, "seven", resource["label"])
	})

	t.Run("custom serializer shapes the resource and keeps the metadata", func(t *testing.T) {
		serializer := func(entity Entity[encoderState]) (map[string]any, error) {
			return map[string]any{"total": entity.State.Count * 2}, nil
		}
		body, err := NewCustomResourceEncoder(serializer).Marshal(makeEncoderEntity())
		require.NoError(t, err)

		var resource map[string]any
		require.NoError(t, json.Unmarshal(body, &resource))
		assert.Equal(t, float64(14), resource["total"])
		assert.Equal(t, "counter:a", resource["$id"])
		assert.Equal(t, "counter", resource["$type"])
		assert.Equal(t, "01HX0000000000000000000000", resource["$revision"])
	})

	t.Run("custom serializer failure returns the wrapped error and no bytes", func(t *testing.T) {
		cause := errors.New("state is not serializable")
		serializer := func(Entity[encoderState]) (map[string]any, error) { return nil, cause }
		body, err := NewCustomResourceEncoder(serializer).Marshal(makeEncoderEntity())

		assert.ErrorIs(t, err, cause)
		assert.ErrorContains(t, err, "failed to serialize resource")
		assert.Nil(t, body)
	})

	t.Run("default serializer failure returns the wrapped error and no bytes", func(t *testing.T) {
		ch := make(chan int)
		entity := Entity[chan int]{
			Aggregate: AggregateId{Type: "counter", Key: "a"},
			Revision:  Revision("01HX0000000000000000000000"),
			Type:      "counter",
			State:     &ch,
		}
		body, err := NewResourceEncoder[chan int]().Marshal(entity)

		assert.ErrorContains(t, err, "failed to serialize resource")
		assert.Nil(t, body)
	})
}
