package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// keys.v1 envelope (Rust crates/store/src/envelope.rs): the 256-bit database key wrapped under the
// master password (Argon2id + XChaCha20-Poly1305) and optionally under a random key in the keyring.
const (
	envelopeFile = "keys.v1"
	aadMaster    = "rustxterm keys.v1 master"
	aadKeyring   = "rustxterm keys.v1 keyring"
)

type envelope struct {
	Version uint32 `json:"version"`
	Kdf     struct {
		Alg  string `json:"alg"`
		MKiB uint32 `json:"m_kib"`
		T    uint32 `json:"t"`
		P    uint32 `json:"p"`
		Salt string `json:"salt"`
	} `json:"kdf"`
	Wraps []struct {
		Kind  string `json:"kind"`
		ID    string `json:"id,omitempty"`
		Nonce string `json:"nonce"`
		Ct    string `json:"ct"`
	} `json:"wraps"`
}

// dbKey is the raw 32-byte SQLCipher key.
type dbKey [32]byte

func readEnvelope(dir string) (*envelope, error) {
	raw, err := os.ReadFile(filepath.Join(dir, envelopeFile))
	if err != nil {
		return nil, err
	}
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("key envelope: not valid JSON: %w", err)
	}
	if e.Version != 1 || e.Kdf.Alg != "argon2id" {
		return nil, errors.New("key envelope: unsupported version or kdf")
	}
	return &e, nil
}

// pwCache keeps the last successful password unlock in memory: Argon2id (256 MiB, t=3) on every store open made each
// GUI action (edit, move, reload: several opens each) take seconds. Keyed by a hash of the directory, the envelope
// bytes and the password, so another store, a changed envelope or another password misses.
var pwCache struct {
	sync.Mutex
	id  [32]byte
	key dbKey
	ok  bool
}

// unlockWithPassword derives the wrapping key with Argon2id and opens the master wrap.
func unlockWithPassword(dir, password string) (dbKey, error) {
	var key dbKey
	e, err := readEnvelope(dir)
	if err != nil {
		return key, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, envelopeFile))
	if err != nil {
		return key, err
	}
	h := sha256.New()
	for _, part := range [][]byte{[]byte(dir), raw, []byte(password)} {
		h.Write([]byte(fmt.Sprint(len(part), ":")))
		h.Write(part)
	}
	var id [32]byte
	h.Sum(id[:0])
	// held across the derivation, so concurrent opens (Folders + Sessions) derive once
	pwCache.Lock()
	defer pwCache.Unlock()
	if pwCache.ok && pwCache.id == id {
		return pwCache.key, nil
	}
	key, err = unlockMaster(e, password)
	if err == nil {
		pwCache.id, pwCache.key, pwCache.ok = id, key, true
	}
	return key, err
}

func unlockMaster(e *envelope, password string) (dbKey, error) {
	var key dbKey
	salt, err := base64.StdEncoding.DecodeString(e.Kdf.Salt)
	if err != nil || len(salt) != 16 {
		return key, errors.New("key envelope: salt must be 16 bytes")
	}
	// Argon2id, version 0x13, 32-byte output (matches Rust argon2::Params / Version::V0x13).
	wrapping := argon2.IDKey([]byte(password), salt, e.Kdf.T, e.Kdf.MKiB, uint8(e.Kdf.P), 32)
	for _, w := range e.Wraps {
		if w.Kind != "master" {
			continue
		}
		return openWrap(w.Nonce, w.Ct, wrapping, []byte(aadMaster))
	}
	return key, errors.New("key envelope: no master wrap")
}

// unlockWithKeyring opens the keyring wrap using the per-launch random wrapping key stored in the
// Secret Service. Returns ok=false when there is no keyring wrap or the keyring is unavailable.
func unlockWithKeyring(dir string, kr keyring) (dbKey, bool, error) {
	var key dbKey
	e, err := readEnvelope(dir)
	if err != nil {
		return key, false, err
	}
	for _, w := range e.Wraps {
		if w.Kind != "keyring" || w.ID == "" {
			continue
		}
		wrapping, ok, err := kr.get(w.ID)
		if err != nil || !ok || len(wrapping) != 32 {
			return key, false, err
		}
		key, err = openWrap(w.Nonce, w.Ct, wrapping, []byte(aadKeyring))
		return key, err == nil, err
	}
	return key, false, nil
}

func openWrap(nonceB64, ctB64 string, wrapping, aad []byte) (dbKey, error) {
	var key dbKey
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return key, errors.New("key envelope: malformed wrap nonce")
	}
	ct, err := base64.StdEncoding.DecodeString(ctB64)
	if err != nil {
		return key, errors.New("key envelope: malformed wrap ciphertext")
	}
	aead, err := chacha20poly1305.NewX(wrapping)
	if err != nil {
		return key, err
	}
	plain, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return key, errors.New("wrong master password")
	}
	if len(plain) != 32 {
		return key, errors.New("key envelope: malformed wrap payload")
	}
	copy(key[:], plain)
	return key, nil
}
