package werestate

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

// framedTerminalError builds an error whose message is an encoded frame —
// the shape an SDK service-to-service call surfaces when the callee raised a
// declared error. decorations is the number of "[422] " prefixes the runtime
// prepended: one at the ingress edge, one per service-to-service hop.
func framedTerminalError(t *testing.T, rejection we.Rejection, decorations int) error {
	t.Helper()
	message, err := encodeErrorFrame(rejection.ToErrorFrame())
	require.NoError(t, err)
	for range decorations {
		message = "[422] " + message
	}
	return errors.New(message)
}

// DeclaredError recovers the declared error from a framed terminal message
// regardless of how many "[<code>] " decorations the runtime applied: none,
// one (the ingress edge), or two (the observed service-to-service shape,
// "[422] [422] wee-events:error-frame+json:{...}").
func TestDeclaredErrorDecodesFrame(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{
			"balance":   we.MakeI64Field(0),
			"requested": we.MakeI64Field(100),
		})

	for _, tc := range []struct {
		name        string
		decorations int
	}{
		{name: "undecorated", decorations: 0},
		{name: "single decoration", decorations: 1},
		{name: "doubled decoration", decorations: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declared, ok := DeclaredError(framedTerminalError(t, rejection, tc.decorations))
			require.True(t, ok, "a framed message must classify as declared")

			var recovered we.Rejection
			require.True(t, errors.As(declared, &recovered), "expected we.Rejection, got %T: %v", declared, declared)
			assert.Equal(t, rejection, recovered)
		})
	}
}

// Decoders are consulted in order; the first claim wins.
func TestDeclaredErrorRespectsDecoderOrder(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{"balance": we.MakeI64Field(25)})

	first := errors.New("claimed by first")
	second := errors.New("claimed by second")

	declared, ok := DeclaredError(framedTerminalError(t, rejection, 0),
		func(we.ErrorFrame) (error, bool) { return first, true },
		func(we.ErrorFrame) (error, bool) { return second, true },
	)
	require.True(t, ok)
	assert.Same(t, first, declared, "the first claiming decoder must win")
}

// A decoder that claims but returns nil is treated as unclaimed — same rule
// as the ingress client — so the frame still lands on the generic fallback.
func TestDeclaredErrorNilClaimFallsThrough(t *testing.T) {
	rejection := we.MakeRejection("order.closed", "order is closed", nil)

	declared, ok := DeclaredError(framedTerminalError(t, rejection, 0),
		func(we.ErrorFrame) (error, bool) { return nil, true },
	)
	require.True(t, ok)

	var recovered we.Rejection
	require.True(t, errors.As(declared, &recovered), "expected we.Rejection, got %T: %v", declared, declared)
	assert.Equal(t, "order.closed", recovered.Code)
}

// An unclaimed frame falls back to the generic we.Rejection carrying the
// frame's code, message, and fields — the Go library's guaranteed floor.
func TestDeclaredErrorFallsBackToRejection(t *testing.T) {
	rejection := we.MakeRejection("order.closed", "order is closed", nil)

	declared, ok := DeclaredError(framedTerminalError(t, rejection, 0),
		func(we.ErrorFrame) (error, bool) { return nil, false },
	)
	require.True(t, ok)

	var recovered we.Rejection
	require.True(t, errors.As(declared, &recovered))
	assert.Equal(t, "order.closed", recovered.Code)
	assert.Equal(t, "order is closed", recovered.Message)
	assert.Equal(t, map[string]we.ErrorField{}, recovered.Fields,
		"a nil-fields rejection round-trips with an empty (non-nil) fields map: the frame codec always emits \"fields\":{}")
}

// A message without a frame is the transport lane: not declared, no error
// synthesised — with or without runtime decoration in front of it.
func TestDeclaredErrorNonFrameIsNotDeclared(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
	}{
		{name: "undecorated", message: "connection reset by peer"},
		{name: "doubled decoration", message: "[500] [500] connection reset by peer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declared, ok := DeclaredError(errors.New(tc.message))
			assert.False(t, ok, "a plain error must not classify as declared")
			assert.Nil(t, declared)
		})
	}
}

// A prefixed-but-corrupt frame payload is not half-decoded; it stays in the
// transport lane, matching decodeErrorFrame's contract.
func TestDeclaredErrorCorruptFrameIsNotDeclared(t *testing.T) {
	declared, ok := DeclaredError(errors.New(errorFramePrefix + `{"code":`))
	assert.False(t, ok)
	assert.Nil(t, declared)
}

// nil in, (nil, false) out.
func TestDeclaredErrorNilIsNotDeclared(t *testing.T) {
	declared, ok := DeclaredError(nil)
	assert.False(t, ok)
	assert.Nil(t, declared)
}
