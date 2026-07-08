package sqlite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/avast/retry-go/v4"
)

const remoteAPIRetryAttempts = 3

type remoteRetryableError struct {
	err error
}

func (e *remoteRetryableError) Error() string { return e.err.Error() }
func (e *remoteRetryableError) Unwrap() error { return e.err }

func retryableRemoteError(err error) error {
	if err == nil {
		return nil
	}
	return &remoteRetryableError{err: err}
}

func retryableRemoteStatusError(operation string, status int) error {
	return retryableRemoteError(fmt.Errorf("sqlite: %s returned status %d", operation, status))
}

func withRemoteAPIRetry(ctx context.Context, operation func() error) error {
	err := retry.Do(
		operation,
		retry.Context(ctx),
		retry.Attempts(remoteAPIRetryAttempts),
		retry.Delay(50*time.Millisecond),
		retry.MaxDelay(250*time.Millisecond),
		retry.DelayType(retry.BackOffDelay),
		retry.RetryIf(func(err error) bool {
			var retryable *remoteRetryableError
			return errors.As(err, &retryable)
		}),
		retry.LastErrorOnly(true),
		retry.WrapContextErrorWithLastError(true),
	)
	if err == nil {
		return nil
	}
	var retryable *remoteRetryableError
	if errors.As(err, &retryable) {
		return retryable.err
	}
	return err
}

func isRemoteAPIRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}
