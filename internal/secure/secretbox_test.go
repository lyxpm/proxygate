package secure

import "testing"

func TestBoxSealOpen(t *testing.T) {
	key := make([]byte, 32)
	box, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("cloudflare-token"), "dns-provider:7")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "cloudflare-token" {
		t.Fatal("secret was not encrypted")
	}
	plain, err := box.Open(sealed, "dns-provider:7")
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "cloudflare-token" {
		t.Fatalf("round-trip mismatch: %q", plain)
	}
	if _, err := box.Open(sealed, "dns-provider:8"); err == nil {
		t.Fatal("associated data mismatch should fail")
	}
}

func TestRandomPassword(t *testing.T) {
	pw, err := RandomPassword(18)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 18 {
		t.Fatalf("length=%d", len(pw))
	}
	tok, err := RandomToken(16)
	if err != nil || len(tok) < 20 {
		t.Fatalf("token=%q err=%v", tok, err)
	}
}
