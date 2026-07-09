package we

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REJECT-S1.R1 - the framework provides a Rejection value type carrying a code,
// a message, and structured fields from the closed scalar set, and it satisfies
// the error interface.
func rejectionCarriesCodeMessageFields(t *testing.T) {
	rejection := MakeRejection("customer.cancelled", "customer is already cancelled",
		map[string]ErrorField{"state": MakeTextField("cancelled")})

	assert.Equal(t, "customer.cancelled", rejection.Code)
	assert.Equal(t, "customer is already cancelled", rejection.Message)
	state, ok := rejection.Fields["state"].Text()
	require.True(t, ok)
	assert.Equal(t, "cancelled", state)

	var asError error = rejection
	assert.Equal(t, "customer.cancelled: customer is already cancelled", asError.Error())
}

// REJECT-S1.R1 - a Rejection constructed without fields is still a valid error.
func rejectionWithoutFieldsIsValid(t *testing.T) {
	rejection := MakeRejection("order.closed", "order is closed", nil)

	assert.Nil(t, rejection.Fields)
	assert.Equal(t, "order.closed: order is closed", rejection.Error())
}

// REJECT-S1.R2, REJECT-S4.R1 - a Rejection returned as a plain error is
// recoverable through the error chain via errors.As, even when wrapped.
func rejectionRecoverableViaErrorsAs(t *testing.T) {
	original := MakeRejection("order.closed", "order is closed",
		map[string]ErrorField{"reason": MakeTextField("closed")})

	var err error = original
	err = fmt.Errorf("dispatch failed: %w", err)

	var recovered Rejection
	require.True(t, errors.As(err, &recovered), "expected to recover a Rejection, got %T", err)
	assert.Equal(t, "order.closed", recovered.Code)
	assert.Equal(t, "order is closed", recovered.Message)
	reason, ok := recovered.Fields["reason"].Text()
	require.True(t, ok)
	assert.Equal(t, "closed", reason)
}

// A Rejection is a declared service error: its frame carries the same code,
// message, and fields, so it round-trips any conformant boundary losslessly.
func rejectionSatisfiesServiceErrorContract(t *testing.T) {
	var _ ServiceErrorContract = Rejection{}

	rejection := MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]ErrorField{"balance": MakeI64Field(0), "requested": MakeI64Field(100)})

	frame := rejection.ToErrorFrame()
	assert.Equal(t, ErrorFrame{
		Code:    "account.insufficient-funds",
		Message: "insufficient funds",
		Fields: map[string]ErrorField{
			"balance":   MakeI64Field(0),
			"requested": MakeI64Field(100),
		},
	}, frame)
}

// REJECT-S4.R1 - an infrastructure error is not recoverable as a Rejection.
func infrastructureErrorIsNotARejection(t *testing.T) {
	err := errors.New("dynamodb is down")

	var recovered Rejection
	assert.False(t, errors.As(err, &recovered), "an infrastructure error must not classify as a Rejection")
}

// terminal classifies a command result the way a connector (e.g. Restate,
// feature 03) would: a Rejection — and an inbound DecodeError — are terminal
// (the answer is final, do not retry), while any other error is retryable
// infrastructure. This mirrors the errors.As branch the spec requires the
// taxonomy to expose (REJECT-S4.R1).
func terminal(err error) bool {
	var rejection Rejection
	if errors.As(err, &rejection) {
		return true
	}

	var decode *DecodeError
	return errors.As(err, &decode)
}

// REJECT-S4.R1, REJECT-S4.R2 - a Rejection is classified as terminal via
// errors.As without a sealed union.
func rejectionIsTerminal(t *testing.T) {
	err := fmt.Errorf("dispatch: %w", MakeRejection("order.closed", "closed", nil))
	assert.True(t, terminal(err), "a Rejection must be classified as terminal")
}

// REJECT-S4.R1, REJECT-S4.R2 - an inbound-decode failure is terminal: retrying
// a malformed request cannot succeed.
func decodeFailureIsTerminal(t *testing.T) {
	err := CommandDecodeFailed("order.place", errors.New("malformed"))
	assert.True(t, terminal(err), "an inbound-decode failure must be classified as terminal")
}

// REJECT-S4.R1, REJECT-S4.R3 - an infrastructure error is classified as
// retryable (not terminal); RevisionConflict is in this lane too.
func infrastructureErrorIsRetryable(t *testing.T) {
	assert.False(t, terminal(errors.New("dynamodb is down")), "a store error must be retryable")
	assert.False(t, terminal(RevisionConflict), "a revision conflict must be retryable, never a terminal rejection")
}

func TestRejection(t *testing.T) {
	t.Run("carries code, message and fields (REJECT-S1.R1)", rejectionCarriesCodeMessageFields)
	t.Run("is valid without fields (REJECT-S1.R1)", rejectionWithoutFieldsIsValid)
	t.Run("recoverable via errors.As (REJECT-S1.R2, REJECT-S4.R1)", rejectionRecoverableViaErrorsAs)
	t.Run("satisfies ServiceErrorContract", rejectionSatisfiesServiceErrorContract)
	t.Run("infrastructure error is not a rejection (REJECT-S4.R1)", infrastructureErrorIsNotARejection)
	t.Run("rejection is terminal (REJECT-S4.R1, REJECT-S4.R2)", rejectionIsTerminal)
	t.Run("decode failure is terminal (REJECT-S4.R1, REJECT-S4.R2)", decodeFailureIsTerminal)
	t.Run("infrastructure error is retryable (REJECT-S4.R1, REJECT-S4.R3)", infrastructureErrorIsRetryable)
}
