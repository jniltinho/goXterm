package store

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestEncryptDecryptRoundTrip builds a synthetic plaintext SQLite image (page size 4096 with the
// 80-byte reserved region per page and the SQLite header on page 1), encrypts it with encryptDB and
// checks decryptDB reproduces it byte for byte — proving the write primitive inverts the read one.
func TestEncryptDecryptRoundTrip(t *testing.T) {
	const pages = 3
	plain := make([]byte, pages*pageSize)
	for p := 0; p < pages; p++ {
		start := p * pageSize
		dataStart := start
		if p == 0 {
			dataStart = start + saltSize // page 1 data follows the 16-byte header
		}
		if _, err := rand.Read(plain[dataStart : start+pageSize-reserve]); err != nil {
			t.Fatal(err)
		}
		// the reserved region (last 80 bytes) stays zero, as SQLCipher/decryptDB expect
	}
	copy(plain[0:], sqliteHeader) // page 1 starts with the SQLite magic

	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}

	enc, err := encryptDB(plain, key, salt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(enc) != len(plain) {
		t.Fatalf("encrypted size %d != %d", len(enc), len(plain))
	}
	if !bytes.Equal(enc[:saltSize], salt) {
		t.Fatal("page 1 does not start with the salt")
	}

	path := filepath.Join(t.TempDir(), "rt.db")
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := decryptDB(path, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("round-trip mismatch: decrypt(encrypt(plain)) != plain")
	}

	// a wrong key must fail the HMAC check, not silently return garbage
	var bad [32]byte
	copy(bad[:], key[:])
	bad[0] ^= 0xff
	if _, err := decryptDB(path, bad); err == nil {
		t.Fatal("decrypt accepted a wrong key")
	}
}
