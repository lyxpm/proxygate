// Package response defines the uniform API envelope and error translation.
package response

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/proxygate/proxygate/internal/apperr"
)

// Envelope is the uniform response body.
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Page is the uniform paginated payload.
type Page struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// JSON writes an envelope with the given status.
func JSON(w http.ResponseWriter, status, code int, message string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Code: code, Message: message, Data: data})
}

// OK writes a successful response.
func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, 0, "success", data)
}

// Created writes a 201 response.
func Created(w http.ResponseWriter, data any) {
	JSON(w, http.StatusCreated, 0, "success", data)
}

// Error maps a service error to HTTP status and envelope.
func Error(w http.ResponseWriter, err error) {
	status, code := statusFor(err)
	JSON(w, status, code, apperr.MessageOf(err), nil)
}

func statusFor(err error) (int, int) {
	switch apperr.KindOf(err) {
	case apperr.Invalid:
		return http.StatusBadRequest, 400
	case apperr.NotFound:
		return http.StatusNotFound, 404
	case apperr.Conflict:
		return http.StatusConflict, 409
	case apperr.Unauthorized:
		return http.StatusUnauthorized, 401
	case apperr.Forbidden:
		return http.StatusForbidden, 403
	case apperr.TooMany:
		return http.StatusTooManyRequests, 429
	case apperr.Unavailable:
		return http.StatusServiceUnavailable, 503
	default:
		return http.StatusInternalServerError, 500
	}
}

// Decode reads a JSON body with a size limit.
func Decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return apperr.Invalidf("request body is required")
		}
		return apperr.Invalidf("invalid JSON body: %v", err)
	}
	return nil
}

// PageQuery normalises page/page_size query parameters.
func PageQuery(r *http.Request) (page, pageSize int) {
	page, pageSize = 1, 20
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			page = n
		}
	}
	if page > 1000 {
		page = 1000
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 && n <= 500 {
			pageSize = n
		}
	}
	return page, pageSize
}

func parseInt(v string) (int, error) {
	var n int
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
