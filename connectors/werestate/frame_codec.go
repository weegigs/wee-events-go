package werestate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

// errorFramePrefix marks a Restate terminal-error message as carrying an
// encoded we.ErrorFrame. Restate 0.9's TerminalError has no typed payload
// channel (code + message only), so the frame rides in the message string;
// the prefix is the format discriminator — an unprefixed message is by
// definition not a frame. The constant is shared verbatim with wee-events.rs
// (crates/wee-events-restate/src/frame_codec.rs).
const errorFramePrefix = "wee-events:error-frame+json:"

// encodeErrorFrame renders a frame as a prefixed terminal-error message.
func encodeErrorFrame(frame we.ErrorFrame) (string, error) {
	data, err := json.Marshal(frame)
	if err != nil {
		return "", fmt.Errorf("werestate: encode error frame: %w", err)
	}
	return errorFramePrefix + string(data), nil
}

// decodeErrorFrame recovers a frame from a terminal-error message. The comma-ok
// result distinguishes "not a frame" (legacy or foreign message — transport
// lane) from a decoded declared error; a prefixed-but-corrupt payload is
// treated as not a frame rather than half-decoded.
func decodeErrorFrame(message string) (we.ErrorFrame, bool) {
	encoded, ok := strings.CutPrefix(message, errorFramePrefix)
	if !ok {
		return we.ErrorFrame{}, false
	}
	var frame we.ErrorFrame
	if err := json.Unmarshal([]byte(encoded), &frame); err != nil {
		return we.ErrorFrame{}, false
	}
	return frame, true
}

// framedError carries the encoded frame as its message while keeping the
// original error reachable through Unwrap: the wire sees the frame, and
// in-process callers can still recover the underlying we.Rejection via
// errors.As (RESTATE-S3.R2, RESTATE-S3.R3).
type framedError struct {
	message string
	cause   error
}

func (e *framedError) Error() string { return e.message }

func (e *framedError) Unwrap() error { return e.cause }
