package provider

import "testing"

func TestProviderConstructors(t *testing.T) {
	cases := []struct {
		name string
		cred Credentials
	}{
		{"cloudflare", Credentials{Secret: "api-token"}},
		{"cloudflare", Credentials{ID: "user@example.com", Secret: "global-key"}},
		{"alidns", Credentials{ID: "ak", Secret: "sk"}},
		{"tencentcloud", Credentials{ID: "sid", Secret: "skey"}},
	}
	for _, tc := range cases {
		if _, err := New(tc.name, tc.cred); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := New("unknown", Credentials{}); err == nil {
		t.Fatal("expected unknown provider error")
	}
	if _, err := New("alidns", Credentials{}); err == nil {
		t.Fatal("expected missing credentials error")
	}
	if meta, ok := Lookup("cloudflare"); !ok || meta.Title != "Cloudflare" {
		t.Fatalf("lookup failed: %+v %v", meta, ok)
	}
}
