package lsp

import (
	"errors"
	"log/slog"
)

// expectedError marks an error as part of normal operation: invalid client
// settings, a rejected config file. Logging treats it as a warning, so
// expected failures do not surface as errors in the client's log channel.
type expectedError struct{ err error }

func (e *expectedError) Error() string { return e.err.Error() }
func (e *expectedError) Unwrap() error { return e.err }

// Expected wraps err as expected; nil stays nil.
func Expected(err error) error {
	if err == nil {
		return nil
	}

	return &expectedError{err}
}

// logError logs err: at warning level when expected (or wrapping an
// expected error), error otherwise. msg describes the operation; args are
// the slog key-value pairs.
func logError(msg string, err error, args ...any) {
	args = append(args, "err", err)

	if expected(err) {
		slog.Warn(msg, args...)

		return
	}

	slog.Error(msg, args...)
}

func expected(err error) bool {
	var e *expectedError

	return errors.As(err, &e)
}
