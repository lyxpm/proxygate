package applog

import (
	"net/url"
	"strings"
)

var sensitiveQueryKeys = map[string]bool{
	"token":         true,
	"access_token":  true,
	"refresh_token": true,
	"id_token":      true,
	"api_key":       true,
	"apikey":        true,
	"key":           true,
	"secret":        true,
	"password":      true,
	"passwd":        true,
	"signature":     true,
	"sig":           true,
	"jwt":           true,
	"ticket":        true,
	"session":       true,
}

// RedactRequestURI removes credentials carried in query strings before a
// request URI is persisted or returned by the log APIs.
func RedactRequestURI(raw string) string {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	changed := false
	for key := range q {
		if sensitiveQueryKeys[strings.ToLower(key)] {
			q.Set(key, "<redacted>")
			changed = true
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.RequestURI()
}
