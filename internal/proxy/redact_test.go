package proxy

import "testing"

func TestRedactRequestURI(t *testing.T) {
	got := redactRequestURI("/ws?token=jwt-secret&access_token=abc&foo=bar")
	if got != "/ws?access_token=%3Credacted%3E&foo=bar&token=%3Credacted%3E" {
		t.Fatalf("unexpected redaction: %q", got)
	}
	if got := redactRequestURI("/api?foo=bar"); got != "/api?foo=bar" {
		t.Fatalf("non-sensitive query changed: %q", got)
	}
}
