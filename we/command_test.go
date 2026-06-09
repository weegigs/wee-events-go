package we

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestCommand struct{}

type TestNamedCommand struct{}

func (TestNamedCommand) TypeName() string {
	return "test:named"
}

func resolvesExplicitName(t *testing.T) {
	assert.Equal(t, CommandName("test:named"), CommandNameOf(TestNamedCommand{}))
}

func resolvesImplicitName(t *testing.T) {
	assert.Equal(t, CommandName("we:test-command"), CommandNameOf(TestCommand{}))
}

func TestCommands(t *testing.T) {
	t.Run("resolves explicit name", resolvesExplicitName)
	t.Run("resolves implicit name", resolvesImplicitName)
}

type remotePayload struct {
	Greeting string `json:"greeting"`
}

func noopPublisher(context.Context, AggregateId, PublishOptions, ...DomainEvent) error {
	return nil
}

// CBOR-S4.R1 - a RemoteCommand whose payload declares an encoding decodes with
// the registered decoder for that encoding and dispatches.
func remoteCommandDecodesDeclaredEncoding(t *testing.T) {
	want := remotePayload{Greeting: "hello"}

	dispatch := func(payload Data, t *testing.T) {
		t.Helper()

		var dispatched remotePayload
		handler := CommandHandlerFunction[struct{}, remotePayload](
			func(_ context.Context, cmd remotePayload, _ Entity[struct{}], _ EventPublisher) error {
				dispatched = cmd
				return nil
			},
		)

		cmd := RemoteCommand{CommandName: "test:remote", Payload: payload}
		err := handler.HandleRemoteCommand(context.Background(), cmd, Entity[struct{}]{}, noopPublisher)
		require.NoError(t, err)
		assert.Equal(t, want, dispatched)
	}

	t.Run("json payload", func(t *testing.T) {
		payload, err := MakeJSONEncoder().Encode(want)
		require.NoError(t, err)
		dispatch(payload, t)
	})

	t.Run("cbor payload", func(t *testing.T) {
		payload, err := MakeCBOREncoder().Encode(want)
		require.NoError(t, err)
		dispatch(payload, t)
	})
}

// CBOR-S4.R2 - a RemoteCommand declaring an unsupported encoding is rejected
// with a typed unknown-encoding error.
func remoteCommandRejectsUnsupportedEncoding(t *testing.T) {
	handler := CommandHandlerFunction[struct{}, remotePayload](
		func(_ context.Context, _ remotePayload, _ Entity[struct{}], _ EventPublisher) error {
			t.Fatal("handler must not run for an unsupported encoding")
			return nil
		},
	)

	cmd := RemoteCommand{
		CommandName: "test:remote",
		Payload:     Data{Encoding: "application/x-unknown", Data: []byte("{}")},
	}
	err := handler.HandleRemoteCommand(context.Background(), cmd, Entity[struct{}]{}, noopPublisher)

	require.Error(t, err)
	var invalid *InvalidEncodingError
	require.True(t, errors.As(err, &invalid), "expected *InvalidEncodingError, got %T", err)
	assert.Equal(t, "application/x-unknown", invalid.Actual)
}

func TestRemoteCommandEncoding(t *testing.T) {
	t.Run("decodes declared encoding (CBOR-S4.R1)", remoteCommandDecodesDeclaredEncoding)
	t.Run("rejects unsupported encoding (CBOR-S4.R2)", remoteCommandRejectsUnsupportedEncoding)
}
