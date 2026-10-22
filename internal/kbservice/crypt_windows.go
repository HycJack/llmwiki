//go:build windows

package kbservice

import (
	"encoding/base64"
	"syscall"
	"unsafe"
)

// Windows-only: encrypt/decrypt secrets with the user's DPAPI so an OpenAI API
// key is no longer stored as plaintext on disk. Non-Windows builds fall back to
// a reversible base64 marker (see crypt_other.go) so the field stays portable.

var (
	crypt32              = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect   = crypt32.NewProc("CryptUnprotectData")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree        = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	size uint32
	data *byte
}

func newBlob(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	ptr := unsafe.Pointer(&b[0])
	return &dataBlob{size: uint32(len(b)), data: (*byte)(ptr)}
}

func blobBytes(b *dataBlob) []byte {
	if b == nil || b.data == nil || b.size == 0 {
		return nil
	}
	return unsafe.Slice(b.data, int(b.size))
}

// encryptSecret returns a base64 DPAPI-encrypted payload for s.
func encryptSecret(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	in := newBlob([]byte(s))
	var out dataBlob
	r1, _, e := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r1 == 0 {
		return "", e
	}
	enc := blobBytes(&out)
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return base64.StdEncoding.EncodeToString(enc), nil
}

// decryptSecret reverses encryptSecret.
func decryptSecret(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	in := newBlob(raw)
	var out dataBlob
	r1, _, e := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r1 == 0 {
		return "", e
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return string(blobBytes(&out)), nil
}
