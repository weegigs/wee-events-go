# Declared Service Errors End-to-End Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Any typed error implementing `we.ServiceErrorContract` crosses the werestate boundary as an error frame and is recoverable by any caller — ingress clients and in-handler service-to-service callers — plus three ingress-client hardening items.

**Architecture:** Server side, `mapError` in the werestate connector generalises from the concrete `we.Rejection` to the `we.ServiceErrorContract` interface. Client side, the frame-classification pipeline is extracted into a shared function with two entry points: the existing ingress `Client.classifyFailure` and a new exported `DeclaredError` for in-handler callers. The cross-language contract ends at the frame; the decoder-registry-plus-rejection-fallback pipeline is the Go library's mapping convention only.

**Tech Stack:** Go 1.26 (via mise), restate sdk-go v0.24.0, testify, testcontainers-go v0.42.0 (integration only).

**Spec:** `docs/superpowers/specs/2026-07-14-declared-service-errors-design.md`
**Issue:** wee-events-go-t3p

## Global Constraints

- Version control is **jj**, not git. Create commits with `jj split . -m "<message>"` (never `git add`/`git commit`, never `jj describe` on the working copy). Commit messages use past tense ("Added X", not "Add X"). No AI co-writer notes.
- Run Go through mise: `mise exec -- go test ...`. Project gates: `just lint`, `just test`; integration: `just test-integration` or `mise exec -- go test -tags integration -v ./connectors/werestate/` (requires Docker).
- No lint suppressions of any kind.
- The response-body cap value is `1 << 20`, matching `defaultMaxBodyBytes` in `connectors/wehttp/http.go:58`.
- All new code lives in `connectors/werestate/`; the `we/` core is not modified.
- Comments follow the existing package style: explain constraints and contract decisions, cite requirement IDs (RESTATE-S3.Rn) where the existing code does.

---

### Task 1: Server — `mapError` frames any `we.ServiceErrorContract`

**Files:**
- Modify: `connectors/werestate/restate.go` (mapError body ~line 282-319, mapError doc comment ~line 256-281, package doc comment ~line 17-21)
- Modify: `documents/features/03-restate-integration.md` (RESTATE-S3.R2 wording, ~line 88)
- Test: `connectors/werestate/restate_test.go`

**Interfaces:**
- Consumes: `we.ServiceErrorContract` (exists at `we/error_frame.go:181` — `error` + `ToErrorFrame() we.ErrorFrame`), `encodeErrorFrame`, `framedError` (both in `frame_codec.go`).
- Produces: `mapError(err error) error` behaviour later tasks and the integration test rely on: any error chain containing a `we.ServiceErrorContract` becomes a `*framedError` whose message is the encoded frame, wrapping `restate.TerminalError(err, 422)`.

- [ ] **Step 1: Write the failing test**

Append to `connectors/werestate/restate_test.go`:

```go
// quotaExceededError is a typed declared error implementing
// we.ServiceErrorContract WITHOUT being a we.Rejection: the boundary must
// frame any contract implementation, not just the generic rejection type.
type quotaExceededError struct {
	Limit int64
	Used  int64
}

func (e quotaExceededError) Error() string {
	return fmt.Sprintf("quota exceeded: used %d of %d", e.Used, e.Limit)
}

func (e quotaExceededError) ToErrorFrame() we.ErrorFrame {
	return we.ErrorFrame{
		Code:    "counter.quota-exceeded",
		Message: e.Error(),
		Fields: map[string]we.ErrorField{
			"limit": we.MakeI64Field(e.Limit),
			"used":  we.MakeI64Field(e.Used),
		},
	}
}

// RESTATE-S3.R2 (generalised) — a typed error implementing
// we.ServiceErrorContract is framed exactly like the generic rejection:
// terminal, frame in the message, original error reachable through Unwrap.
func TestServiceErrorContractMapsToTerminalFrame(t *testing.T) {
	declared := quotaExceededError{Limit: 100, Used: 250}

	mapped := mapError(fmt.Errorf("dispatch failed: %w", declared))
	require.Error(t, mapped)
	require.True(t, restate.IsTerminalError(mapped), "a declared service error must be terminal")

	frame, ok := decodeErrorFrame(mapped.Error())
	require.True(t, ok, "terminal message must carry an encoded error frame, got %q", mapped.Error())
	assert.Equal(t, declared.ToErrorFrame(), frame)
	assert.Equal(t, restate.Code(http.StatusUnprocessableEntity), restate.ErrorCode(mapped))

	var recovered quotaExceededError
	require.True(t, errors.As(mapped, &recovered), "typed error must stay recoverable through Unwrap")
	assert.Equal(t, declared, recovered)
}
```

All imports used (`fmt`, `errors`, `http`, `restate`, `assert`, `require`, `we`) are already imported by the file.

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./connectors/werestate/ -run TestServiceErrorContractMapsToTerminalFrame -v`
Expected: FAIL — `mapError` today only recovers the concrete `we.Rejection`, so `quotaExceededError` falls through to the retryable lane; the `IsTerminalError` require fails.

- [ ] **Step 3: Generalise mapError**

In `connectors/werestate/restate.go`, replace the rejection branch of `mapError`:

```go
	var rejection we.Rejection
	if errors.As(err, &rejection) {
		message, encodeErr := encodeErrorFrame(rejection.ToErrorFrame())
		if encodeErr != nil {
			// A frame that cannot encode is a programmer error (a zero-value
			// field); surface it as the fault it is rather than shipping a
			// half-formed frame.
			return restate.TerminalError(fmt.Errorf("werestate: rejection frame not encodable: %w", errors.Join(encodeErr, err)), http.StatusInternalServerError)
		}
		// framedError wraps OUTSIDE the SDK terminal error: the runtime ships
		// the outermost Error() string as the failure message, and the SDK's
		// code wrapper decorates its message with "[422] ", which would break
		// the frame prefix. IsTerminalError and ErrorCode read through Unwrap.
		return &framedError{message: message, cause: restate.TerminalError(err, http.StatusUnprocessableEntity)}
	}
```

with:

```go
	var contract we.ServiceErrorContract
	if errors.As(err, &contract) {
		message, encodeErr := encodeErrorFrame(contract.ToErrorFrame())
		if encodeErr != nil {
			// A frame that cannot encode is a programmer error (a zero-value
			// field); surface it as the fault it is rather than shipping a
			// half-formed frame.
			return restate.TerminalError(fmt.Errorf("werestate: declared error frame not encodable: %w", errors.Join(encodeErr, err)), http.StatusInternalServerError)
		}
		// framedError wraps OUTSIDE the SDK terminal error: the runtime ships
		// the outermost Error() string as the failure message, and the SDK's
		// code wrapper decorates its message with "[422] ", which would break
		// the frame prefix. IsTerminalError and ErrorCode read through Unwrap.
		return &framedError{message: message, cause: restate.TerminalError(err, http.StatusUnprocessableEntity)}
	}
```

Update the first bullet of the `mapError` doc comment from:

```go
//   - we.Rejection (recovered via errors.As) — a domain refusal of a well-formed
//     command. TERMINAL: retrying a refused command cannot change the outcome.
//     The terminal message carries the rejection encoded as a
//     "wee-events:error-frame+json:" frame so remote callers decode the declared
//     error, and the rejection value stays recoverable in-process — its code,
//     message, and fields intact — through the terminal error's Unwrap chain
//     (RESTATE-S3.R2, RESTATE-S3.R3).
```

to:

```go
//   - we.ServiceErrorContract (recovered via errors.As; we.Rejection is the
//     base case) — a declared service error the caller is expected to branch
//     on. TERMINAL: retrying a declared refusal cannot change the outcome.
//     The terminal message carries the error rendered as a
//     "wee-events:error-frame+json:" frame so remote callers decode the declared
//     error, and the original error value stays recoverable in-process —
//     through the terminal error's Unwrap chain (RESTATE-S3.R2, RESTATE-S3.R3).
//     Declaring the contract is a stronger statement of intent than the
//     built-in classifications below, so this check runs first.
```

Update the package doc comment's error paragraph (restate.go ~line 17) from:

```go
// Errors: a recovered we.Rejection crosses the boundary as a
// "wee-events:error-frame+json:" frame in the terminal error message (Restate
// 0.9 has no typed error payload channel), byte-compatible with wee-events.rs.
```

to:

```go
// Errors: a recovered declared service error (we.ServiceErrorContract;
// we.Rejection is the base case) crosses the boundary as a
// "wee-events:error-frame+json:" frame in the terminal error message (Restate
// 0.9 has no typed error payload channel), byte-compatible with wee-events.rs.
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./connectors/werestate/ -v`
Expected: PASS — including the regressions `TestRejectionMapsToTerminalCarryingDetail` and `TestWrappedRejectionMapsToTerminal` (`we.Rejection` implements the interface, so behaviour is identical) and `TestInfrastructureFailureIsRetryable` (a plain error does not implement the contract).

- [ ] **Step 5: Synchronise RESTATE-S3.R2 in the feature spec**

In `documents/features/03-restate-integration.md`, replace:

```markdown
- **RESTATE-S3.R2** (event-driven) — When a handler fails with a domain rejection (the
  Feature 05 taxonomy), the framework shall surface it as a Restate **terminal** error
  carrying the rejection code, message, and context.
```

with:

```markdown
- **RESTATE-S3.R2** (event-driven) — When a handler fails with a declared service error
  (any `we.ServiceErrorContract` implementation; the Feature 05 rejection is the base
  case), the framework shall surface it as a Restate **terminal** error carrying the
  declared code, message, and fields.
```

- [ ] **Step 6: Commit**

```bash
jj split . -m "Generalised the werestate boundary mapping from we.Rejection to any we.ServiceErrorContract"
```

---

### Task 2: Client — shared classification pipeline and exported `DeclaredError`

**Files:**
- Create: `connectors/werestate/declared.go`
- Modify: `connectors/werestate/client.go` (`classifyFailure` ~line 156-176; delete `stripIngressDecoration` ~line 178-198, it moves to declared.go)
- Test: `connectors/werestate/declared_test.go` (new)

**Interfaces:**
- Consumes: `decodeErrorFrame(message string) (we.ErrorFrame, bool)` and `encodeErrorFrame` from `frame_codec.go`; `FrameDecoder` from `client.go:46`; `we.Rejection`.
- Produces:
  - `DeclaredError(err error, decoders ...FrameDecoder) (error, bool)` — exported; Task 4's negative assertions and Task 5's integration test call it.
  - `declaredFromMessage(message string, decoders []FrameDecoder) (error, bool)` — package-private pipeline shared with `classifyFailure`.
  - `stripIngressDecoration(message string) string` — moved verbatim into declared.go.

- [ ] **Step 1: Write the failing tests**

Create `connectors/werestate/declared_test.go`:

```go
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
// declared error.
func framedTerminalError(t *testing.T, rejection we.Rejection, decorated bool) error {
	t.Helper()
	message, err := encodeErrorFrame(rejection.ToErrorFrame())
	require.NoError(t, err)
	if decorated {
		message = "[422] " + message
	}
	return errors.New(message)
}

// DeclaredError recovers the declared error from a framed terminal message,
// with or without the runtime's "[<code>] " decoration.
func TestDeclaredErrorDecodesFrame(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{
			"balance":   we.MakeI64Field(0),
			"requested": we.MakeI64Field(100),
		})

	for _, tc := range []struct {
		name      string
		decorated bool
	}{
		{name: "undecorated", decorated: false},
		{name: "decorated", decorated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declared, ok := DeclaredError(framedTerminalError(t, rejection, tc.decorated))
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

	declared, ok := DeclaredError(framedTerminalError(t, rejection, false),
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

	declared, ok := DeclaredError(framedTerminalError(t, rejection, false),
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

	declared, ok := DeclaredError(framedTerminalError(t, rejection, false),
		func(we.ErrorFrame) (error, bool) { return nil, false },
	)
	require.True(t, ok)

	var recovered we.Rejection
	require.True(t, errors.As(declared, &recovered))
	assert.Equal(t, rejection, recovered)
}

// A message without a frame is the transport lane: not declared, no error
// synthesised.
func TestDeclaredErrorNonFrameIsNotDeclared(t *testing.T) {
	declared, ok := DeclaredError(errors.New("connection reset by peer"))
	assert.False(t, ok, "a plain error must not classify as declared")
	assert.Nil(t, declared)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./connectors/werestate/ -run TestDeclaredError -v`
Expected: FAIL to compile — `DeclaredError` undefined.

- [ ] **Step 3: Create declared.go and delegate classifyFailure**

Create `connectors/werestate/declared.go`:

```go
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
```

In `connectors/werestate/client.go`:

1. Delete the `stripIngressDecoration` function (moved above, comment updated for the two-lane framing).
2. Replace the body of `classifyFailure` after the JSON unmarshal:

```go
// classifyFailure separates the two failure lanes: a failure body whose
// message carries an encoded error frame is a DECLARED service error and is
// decoded back into a branchable error value; anything else is a
// *TransportError. A frame always becomes a declared error — at minimum the
// generic we.Rejection — never a transport failure.
func (c *Client) classifyFailure(status int, body []byte) error {
	var failure struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	if err := json.Unmarshal(body, &failure); err != nil {
		return &TransportError{Status: status, Message: string(body)}
	}

	if declared, ok := declaredFromMessage(failure.Message, c.decoders); ok {
		return declared
	}
	return &TransportError{Status: status, Message: failure.Message}
}
```

3. Remove `"strings"` from client.go's imports **only if** no longer used — `NewClient` still calls `strings.TrimSuffix`, so it stays.

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./connectors/werestate/ -v`
Expected: PASS — the new `TestDeclaredError*` tests plus every existing client test (`TestClientDecodesFramedRejection`, `TestClientDecodesDecoratedFramedRejection`, `TestClientCustomDecoderClaimsFrame`, `TestClientUnclaimedFrameFallsBackToRejection`, `TestClientNilClaimedDecoderFallsBackToRejection`, `TestClientDecoratedPlainFailureStaysTransport`) which now exercise the shared pipeline through `classifyFailure`.

- [ ] **Step 5: Commit**

```bash
jj split . -m "Extracted the frame-classification pipeline and exported DeclaredError for in-handler callers"
```

---

### Task 3: Ingress client hardening — Accept header and bounded body reads

**Files:**
- Modify: `connectors/werestate/client.go` (`call` ~line 110-149; new constant)
- Test: `connectors/werestate/client_test.go`

**Interfaces:**
- Consumes: `Client.call` internals from client.go.
- Produces: `maxResponseBytes = 1 << 20` package constant; every ingress request carries `Accept: application/json`; a response body larger than the cap is a `*TransportError`.

- [ ] **Step 1: Write the failing tests**

Append to `connectors/werestate/client_test.go` (add `"strings"` to the imports):

```go
// Every ingress request declares the response format it can decode.
func TestClientSendsAcceptHeader(t *testing.T) {
	var gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(entityBody(t))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))
	require.NoError(t, err)

	assert.Equal(t, "application/json", gotAccept)
}

// paddedEntityBody returns a valid entity body padded with a filler state
// field to exactly size bytes. The filler is plain ASCII so the JSON length
// grows byte-for-byte with the filler content.
func paddedEntityBody(t *testing.T, size int) []byte {
	t.Helper()
	build := func(filler string) []byte {
		body, err := json.Marshal(EntityResponse{
			State:    map[string]any{"filler": filler},
			ID:       we.EncodedAggregateId("counter:client-1"),
			Type:     we.EntityType("counter"),
			Revision: we.Revision("00000000000000000003"),
		})
		require.NoError(t, err)
		return body
	}
	base := build("")
	require.LessOrEqual(t, len(base), size, "cap too small to build a padded body")
	padded := build(strings.Repeat("x", size-len(base)))
	require.Len(t, padded, size)
	return padded
}

// A response body over the cap is a transport failure — never a truncated,
// half-decoded value.
func TestClientOversizedBodyIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(paddedEntityBody(t, maxResponseBytes+1))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
	assert.Contains(t, transport.Message, "response body exceeds")
}

// A body exactly at the cap still decodes: the bound is a ceiling, not an
// off-by-one truncation.
func TestClientBodyAtCapStillDecodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(paddedEntityBody(t, maxResponseBytes))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	response, err := client.Load(context.Background(), clientAggregateId(t))
	require.NoError(t, err)
	assert.Equal(t, we.EncodedAggregateId("counter:client-1"), response.ID)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./connectors/werestate/ -run 'TestClientSendsAcceptHeader|TestClientOversizedBodyIsTransportError|TestClientBodyAtCapStillDecodes' -v`
Expected: FAIL to compile — `maxResponseBytes` undefined. (After defining only the constant, `TestClientSendsAcceptHeader` fails on an empty Accept header and `TestClientOversizedBodyIsTransportError` fails because the oversized body is read whole and decodes.)

- [ ] **Step 3: Implement header and cap**

In `connectors/werestate/client.go`, add below the `FrameDecoder` type (before `Client`):

```go
// maxResponseBytes bounds how much of an ingress response body the client
// reads, mirroring the intake-side cap (wehttp defaultMaxBodyBytes): a
// response beyond the cap is a transport failure, never a truncated,
// half-decoded value.
const maxResponseBytes = 1 << 20
```

In `call`, after the Content-Type header block:

```go
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
```

and replace the body read:

```go
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return EntityResponse{}, &TransportError{Status: response.StatusCode, Message: "unreadable response body: " + err.Error(), cause: err}
	}
```

with:

```go
	// Read one byte past the cap so an at-cap body is distinguishable from an
	// over-cap one without buffering an unbounded response.
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return EntityResponse{}, &TransportError{Status: response.StatusCode, Message: "unreadable response body: " + err.Error(), cause: err}
	}
	if len(data) > maxResponseBytes {
		return EntityResponse{}, &TransportError{Status: response.StatusCode, Message: fmt.Sprintf("response body exceeds %d byte cap", maxResponseBytes)}
	}
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./connectors/werestate/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
jj split . -m "Hardened the ingress client with an Accept header and a bounded response-body read"
```

---

### Task 4: Transport-lane negative assertions

**Files:**
- Modify: `connectors/werestate/client_test.go` (`TestClientConnectionFailureIsTransportError` ~line 82, `TestClientPlainFailureIsTransportError` ~line 96, `TestClientDecoratedPlainFailureStaysTransport` ~line 205)

**Interfaces:**
- Consumes: `DeclaredError` from Task 2.
- Produces: test-only changes; no API.

- [ ] **Step 1: Add the symmetric negative assertions**

The declared-lane tests already assert a rejection never reads as transport; these three transport-lane tests gain the mirror-image assertions. Append to the end of each of `TestClientConnectionFailureIsTransportError`, `TestClientPlainFailureIsTransportError`, and `TestClientDecoratedPlainFailureStaysTransport`:

```go
	var rejection we.Rejection
	assert.False(t, errors.As(err, &rejection), "a transport failure must never classify as a declared rejection")

	_, declared := DeclaredError(err)
	assert.False(t, declared, "a transport failure must not be claimed by DeclaredError")
```

- [ ] **Step 2: Run the tests**

Run: `mise exec -- go test ./connectors/werestate/ -run 'TestClientConnectionFailureIsTransportError|TestClientPlainFailureIsTransportError|TestClientDecoratedPlainFailureStaysTransport' -v`
Expected: PASS immediately — these assertions document existing behaviour. If any fails, that is a real lane-separation bug: stop and investigate rather than adjusting the assertion.

- [ ] **Step 3: Commit**

```bash
jj split . -m "Added the symmetric negative assertions to the transport-lane client tests"
```

---

### Task 5: Integration — declared error recovered across a service-to-service call

**Files:**
- Modify: `connectors/werestate/integration_test.go` (`startEnvironment` ~line 70-98; new orchestrator definition; new test)

**Interfaces:**
- Consumes: `DeclaredError` (Task 2), `mapError` contract behaviour (Task 1), the existing testcontainers harness (`startEnvironment`, `startRestateRuntime`), `samples/account` (`account.Open{Owner string}`, `account.Withdraw{Amount int64}`, rejection code `account.insufficient-funds` with I64 fields `balance`/`requested`), SDK APIs `restate.NewService`, `restate.NewServiceHandler`, `restate.Object[O]`, `ingress.Service[I, O]`.
- Produces: the load-bearing end-to-end proof; documents the runtime's service-to-service message decoration empirically.

- [ ] **Step 1: Write the failing test**

In `connectors/werestate/integration_test.go`, first bind the orchestrator in `startEnvironment`:

```go
	restateSrv := server.NewRestate().
		Bind(svc.Definition(serviceName)).
		Bind(accountSvc.Definition("account")).
		Bind(orchestratorDefinition())
```

Then append:

```go
// declaredReport is what the orchestrator hands back to the test: the
// classification result of an in-handler service-to-service failure.
type declaredReport struct {
	Declared   bool   `json:"declared"`
	Code       string `json:"code"`
	Balance    int64  `json:"balance"`
	Requested  int64  `json:"requested"`
	RawMessage string `json:"rawMessage"`
}

// orchestratorDefinition registers a plain Restate service whose handler
// calls the account virtual object INSIDE a handler context — the
// service-to-service lane, distinct from ingress — and classifies the
// failure with DeclaredError. It reports the classification as its success
// result so the raw propagated message survives for the test to inspect.
func orchestratorDefinition() restate.ServiceDefinition {
	return restate.NewService("orchestrator").
		Handler("overdraw", restate.NewServiceHandler(
			func(ctx restate.Context, accountKey string) (declaredReport, error) {
				open, err := json.Marshal(account.Open{Owner: "kevin"})
				if err != nil {
					return declaredReport{}, err
				}
				if _, err := restate.Object[EntityResponse](ctx, "account", accountKey, "execute").
					Request(we.RemoteCommand{
						CommandName: we.CommandNameOf(account.Open{}),
						Payload:     we.Data{Encoding: "application/json", Data: open},
					}); err != nil {
					return declaredReport{}, err
				}

				withdraw, err := json.Marshal(account.Withdraw{Amount: 100})
				if err != nil {
					return declaredReport{}, err
				}
				_, err = restate.Object[EntityResponse](ctx, "account", accountKey, "execute").
					Request(we.RemoteCommand{
						CommandName: we.CommandNameOf(account.Withdraw{}),
						Payload:     we.Data{Encoding: "application/json", Data: withdraw},
					})
				if err == nil {
					return declaredReport{}, restate.TerminalError(errors.New("overdraw unexpectedly succeeded"), http.StatusInternalServerError)
				}

				report := declaredReport{RawMessage: err.Error()}
				declared, ok := DeclaredError(err)
				report.Declared = ok
				if !ok {
					return report, nil
				}
				var rejection we.Rejection
				if errors.As(declared, &rejection) {
					report.Code = rejection.Code
					report.Balance, _ = rejection.Fields["balance"].I64()
					report.Requested, _ = rejection.Fields["requested"].I64()
				}
				return report, nil
			}))
}

// The service-to-service lane, end to end: a declared error raised inside
// service B's handler crosses the runtime to service A's in-handler call as
// a terminal error carrying the frame, and DeclaredError recovers it with
// its fields intact. RawMessage documents empirically what decoration (if
// any) the runtime applies on this path — the assertion on the frame prefix
// holds whether or not a "[<code>] " decoration is present, because
// DeclaredError strips it defensively.
func TestDeclaredErrorRecoversAcrossServiceToServiceCall(t *testing.T) {
	client, _, _ := startEnvironment(t)
	ctx := context.Background()

	report, err := ingress.Service[string, declaredReport](client, "orchestrator", "overdraw").
		Request(ctx, "account:orch-1")
	require.NoError(t, err, "the orchestrator handler itself must succeed")

	assert.True(t, report.Declared,
		"the in-handler failure must classify as declared; raw propagated message: %q", report.RawMessage)
	assert.Equal(t, "account.insufficient-funds", report.Code)
	assert.Equal(t, int64(0), report.Balance)
	assert.Equal(t, int64(100), report.Requested)
	assert.Contains(t, report.RawMessage, errorFramePrefix,
		"the propagated terminal message must carry the encoded frame")
}
```

If the raw message shows the frame arrives with decoration the defensive strip does not cover (something other than an optional leading `"[<code>] "`), that is a design-level finding: stop, record the observed message shape in the bead, and surface it for discussion rather than patching the codec ad hoc.

- [ ] **Step 2: Run the integration test**

Requires Docker running. Run: `mise exec -- go test -tags integration -v ./connectors/werestate/ -run TestDeclaredErrorRecoversAcrossServiceToServiceCall`
Expected: PASS — with Tasks 1–2 in place, the account service frames the rejection, the runtime propagates it, and `DeclaredError` recovers code plus both I64 fields. (This test is new coverage, not a red-green cycle: the production code it exercises already landed in Tasks 1–2; what is unproven is the real runtime's propagation behaviour.)

- [ ] **Step 3: Run the full integration suite**

Run: `mise exec -- go test -tags integration -v ./connectors/werestate/`
Expected: PASS — all pre-existing integration tests (`TestIdempotentExecute`, `TestLoadAndExecuteThroughRuntime`, `TestRejectionRoundTripsAcrossBoundary`, restart tests) still green with the orchestrator bound into the shared environment.

- [ ] **Step 4: Commit**

```bash
jj split . -m "Proved DeclaredError recovers a framed rejection across a real service-to-service Restate call"
```

---

### Task 6: Quality gates and session close

**Files:**
- No source changes expected.

- [ ] **Step 1: Run the gates**

```bash
just lint
just test
just test-integration
```

Expected: lint 0 issues, all tests green. Fix root causes if anything fails — no suppressions.

- [ ] **Step 2: Close the bead and report**

```bash
bd close wee-events-go-t3p --reason="Declared service errors end-to-end: mapError generalised to we.ServiceErrorContract; frame-classification pipeline shared and exported as DeclaredError for in-handler/orchestrator callers; ingress client hardened (Accept header, bounded body reads, transport-lane negative assertions); service-to-service recovery proven against a real containerised Restate runtime."
jj status
```

Report the handoff per the conservative profile: changed files, validation results, and note that commits were created with jj split but nothing was pushed.
