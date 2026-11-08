// Package apperr defines the error kinds shared by services and translated to
// HTTP status codes by the API layer.
package apperr

import (
	"errors"
	"fmt"
)

// Kind classifies an error.
type Kind string

// Error kinds.
const (
	Invalid      Kind = "invalid"
	NotFound     Kind = "not_found"
	Conflict     Kind = "conflict"
	Unauthorized Kind = "unauthorized"
	Forbidden    Kind = "forbidden"
	TooMany      Kind = "too_many"
	Internal     Kind = "internal"
	Unavailable  Kind = "unavailable"
)

// Error is a service error with a stable kind and an optional field name.
type Error struct {
	Kind    Kind
	Message string
	Field   string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// New creates an error of kind.
func New(kind Kind, msg string) *Error { return &Error{Kind: kind, Message: msg} }

// Invalidf creates a validation error.
func Invalidf(format string, args ...any) *Error {
	return &Error{Kind: Invalid, Message: fmt.Sprintf(format, args...)}
}

// InvalidField creates a validation error tied to a request field.
func InvalidField(field, msg string) *Error {
	return &Error{Kind: Invalid, Message: msg, Field: field}
}

// NotFoundf creates a not-found error.
func NotFoundf(format string, args ...any) *Error {
	return &Error{Kind: NotFound, Message: fmt.Sprintf(format, args...)}
}

// Conflictf creates a conflict error.
func Conflictf(format string, args ...any) *Error {
	return &Error{Kind: Conflict, Message: fmt.Sprintf(format, args...)}
}

// Internalf wraps an internal error.
func Internalf(err error, format string, args ...any) *Error {
	return &Error{Kind: Internal, Message: fmt.Sprintf(format, args...), Err: err}
}

// KindOf returns the kind of err, or Internal.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return Internal
}

// MessageOf returns a user-facing message.
func MessageOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// FieldOf returns the field associated with a validation error.
func FieldOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Field
	}
	return ""
}
