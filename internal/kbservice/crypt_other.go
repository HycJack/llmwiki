//go:build !windows

package kbservice

// Non-Windows fallback: there is no DPAPI, so secrets fall back to a reversible
// base64 marker. This keeps the Config field portable and the tests hermetic on
// any platform; it is not a security boundary.

import "encoding/base64"

func encryptSecret(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	return "b64:" + base64.StdEncoding.EncodeToString([]byte(s)), nil
}

func decryptSecret(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	b := s
	if len(b) > 4 && b[:4] == "b64:" {
		b = b[4:]
	}
	raw, err := base64.StdEncoding.DecodeString(b)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
