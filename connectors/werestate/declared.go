package werestate

import (
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

// declaredFromMessage is the Go client library's classification pipeline over
// a terminal-error message: strip transport decoration, decode the frame,
// consult decoders in registration order, fall back to the generic
// we.Rejection. ok=false means the message carries no frame — the transport /
// infrastructure lane. Both boundary entry points (the ingress Client and
// DeclaredError) share this one implementation.
//
// The cross-language contract ends at the frame (prefix discriminator +
// we.ErrorFrame shape); everything past frame decode here — the decoder
// registry and the rejection fallback — is the Go mapping convention only,
// owed nothing by other language clients (see
// docs/superpowers/specs/2026-07-14-declared-service-errors-design.md).
func declaredFromMessage(message string, decoders []FrameDecoder) (error, bool) {
	frame, ok := decodeErrorFrame(stripIngressDecoration(message))
	if !ok {
		return nil, false
	}
	for _, decode := range decoders {
		if declared, claimed := decode(frame); claimed && declared != nil {
			return declared, true
		}
	}
	return we.Rejection(frame), true
}

// DeclaredError classifies an error returned by a Restate SDK call (or any
// error whose message may carry a wee-events error frame). ok reports whether
// the error was a declared service error; when true the returned error is the
// decoded declared error — a decoder-mapped typed error, or the generic
// we.Rejection fallback carrying the frame's code, message, and fields.
// ok=false means the transport / infrastructure lane: the input is not a
// declared service outcome and the returned error is nil.
//
// This is the in-handler counterpart of the ingress Client's failure
// classification: a service calling another wee-events service inside a
// Restate handler receives the callee's terminal error through the SDK and
// recovers the declared error with one call:
//
//	_, err := restate.Object[EntityResponse](ctx, "account", key, "execute").Request(command)
//	if declared, ok := werestate.DeclaredError(err); ok {
//		var rejection we.Rejection
//		if errors.As(declared, &rejection) { ... }
//	}
func DeclaredError(err error, decoders ...FrameDecoder) (error, bool) {
	if err == nil {
		return nil, false
	}
	return declaredFromMessage(err.Error(), decoders)
}

// stripIngressDecoration removes the "[<code>] " prefix the Restate runtime
// prepends to a terminal error's message when rendering the ingress failure
// body. The decoration is a transport artifact of the runtime edge — it is
// not part of the error-frame contract, so it is stripped defensively in
// both classification lanes rather than tolerated in the shared frame codec.
func stripIngressDecoration(message string) string {
	rest, ok := strings.CutPrefix(message, "[")
	if !ok {
		return message
	}
	digits, undecorated, found := strings.Cut(rest, "] ")
	if !found || digits == "" {
		return message
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return message
		}
	}
	return undecorated
}
