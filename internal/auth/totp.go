package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const totpPeriod = 30

func generateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

func totpCode(secret string, at time.Time) (string, error) {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(at.Unix() / totpPeriod)
	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(payload[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000), nil
}

func validateTOTP(secret, code string, now time.Time) bool {
	_, ok := validateTOTPStep(secret, code, now)
	return ok
}

func validateTOTPStep(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	if _, err := strconv.Atoi(code); err != nil {
		return 0, false
	}
	for _, delta := range []int64{-1, 0, 1} {
		step := now.Unix()/totpPeriod + delta
		want, err := totpCode(secret, time.Unix(step*totpPeriod, 0))
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func totpURL(issuer, account, secret string) string {
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) +
		"?secret=" + url.QueryEscape(secret) +
		"&issuer=" + url.QueryEscape(issuer) +
		"&algorithm=SHA1&digits=6&period=30"
}
