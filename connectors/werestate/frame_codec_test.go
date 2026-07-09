package werestate

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func clockDriftFrame() we.ErrorFrame {
	return we.ErrorFrame{
		Code:    "time.clock_drift_too_high",
		Message: "time server clock drift too high",
		Fields: map[string]we.ErrorField{
			"observed_ms": we.MakeI64Field(50),
			"allowed_ms":  we.MakeI64Field(10),
		},
	}
}

// Mirrors Rust error_frame_survives_restate_terminal_message_encoding.
func TestErrorFrameSurvivesTerminalMessageEncoding(t *testing.T) {
	encoded, err := encodeErrorFrame(clockDriftFrame())
	require.NoError(t, err)
	assert.Equal(t,
		`wee-events:error-frame+json:{"code":"time.clock_drift_too_high","message":"time server clock drift too high","fields":{"allowed_ms":{"I64":10},"observed_ms":{"I64":50}}}`,
		encoded)

	decoded, ok := decodeErrorFrame(encoded)
	require.True(t, ok)
	assert.Equal(t, clockDriftFrame(), decoded)
}

// Mirrors Rust legacy_display_string_is_not_a_service_error_frame.
func TestLegacyDisplayStringIsNotAFrame(t *testing.T) {
	_, ok := decodeErrorFrame("time server clock drift too high")
	assert.False(t, ok)
}

// A prefixed message whose payload is not a valid frame is not a frame either;
// it must fall to the transport lane, not decode into garbage.
func TestCorruptFramePayloadIsNotAFrame(t *testing.T) {
	_, ok := decodeErrorFrame(errorFramePrefix + `{"code":"a"}`)
	assert.False(t, ok)
}

// framedError puts the encoded frame on the wire while keeping the original
// error reachable for in-process errors.As recovery.
func TestFramedErrorCarriesFrameAndUnwrapsCause(t *testing.T) {
	rejection := we.MakeRejection("order.closed", "order is closed", nil)
	encoded, err := encodeErrorFrame(rejection.ToErrorFrame())
	require.NoError(t, err)

	framed := &framedError{message: encoded, cause: rejection}
	assert.Equal(t, encoded, framed.Error())

	var recovered we.Rejection
	require.True(t, errors.As(framed, &recovered))
	assert.Equal(t, "order.closed", recovered.Code)
}
