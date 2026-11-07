package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// HasKeyring reports whether the envelope in dir has a keyring wrap.
func HasKeyring(dir string) bool {
	e, err := readEnvelope(dir)
	if err != nil {
		return false
	}
	for _, w := range e.Wraps {
		if w.Kind == "keyring" {
			return true
		}
	}
	return false
}

// ChangePassword re-wraps the database key under a new master password (fresh salt, same KDF
// parameters), as Rust envelope::change_password; the database itself is not touched.
// The current key comes from the master password or, when unlocked silently, the keyring.
func ChangePassword(dir, current string, kr Keyring, next string) error {
	if next == "" {
		return errors.New("the master password cannot be empty")
	}
	key, err := keyFor(dir, current, kr)
	if err != nil {
		return err
	}
	env, err := readRawEnvelope(dir)
	if err != nil {
		return err
	}
	kdf, _ := env["kdf"].(map[string]any)
	m, t, p := uint32(kdfMKiB), uint32(kdfT), uint8(kdfP)
	if kdf != nil {
		if v, ok := kdf["m_kib"].(float64); ok {
			m = uint32(v)
		}
		if v, ok := kdf["t"].(float64); ok {
			t = uint32(v)
		}
		if v, ok := kdf["p"].(float64); ok {
			p = uint8(v)
		}
	}
	salt := make([]byte, 16)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	aead, err := chacha20poly1305.NewX(argon2.IDKey([]byte(next), salt, t, m, p, 32))
	if err != nil {
		return err
	}
	b64 := base64.StdEncoding.EncodeToString
	env["kdf"] = map[string]any{"alg": "argon2id", "m_kib": m, "t": t, "p": p, "salt": b64(salt)}
	setWrap(env, "master", map[string]any{"kind": "master", "nonce": b64(nonce), "ct": b64(aead.Seal(nil, nonce, key[:], []byte(aadMaster)))})
	return writeRawEnvelope(dir, env)
}

// SetKeyring adds (on) or removes the keyring wrap; the master password proves access to the key.
// The previous keyring secret is deleted afterwards (best effort), as Rust envelope::set_keyring.
func SetKeyring(dir, password string, kr Keyring, on bool) error {
	key, err := keyFor(dir, password, kr)
	if err != nil {
		return err
	}
	env, err := readRawEnvelope(dir)
	if err != nil {
		return err
	}
	oldID := ""
	if w := getWrap(env, "keyring"); w != nil {
		oldID, _ = w["id"].(string)
	}
	var wrap map[string]any
	if on {
		if wrap, err = keyringWrap(kr, key); err != nil {
			return err
		}
	}
	setWrap(env, "keyring", wrap)
	if err := writeRawEnvelope(dir, env); err != nil {
		return err
	}
	if oldID != "" && kr != nil {
		_ = kr.delete(oldID)
	}
	return nil
}

// keyFor unlocks the database key with the master password, else through the keyring wrap.
func keyFor(dir, password string, kr Keyring) (dbKey, error) {
	if password != "" || kr == nil {
		return unlockWithPassword(dir, password)
	}
	key, ok, err := unlockWithKeyring(dir, kr)
	if err != nil || !ok {
		return key, errors.New("enter the master password first")
	}
	return key, nil
}

func readRawEnvelope(dir string) (map[string]any, error) {
	if _, err := readEnvelope(dir); err != nil { // validates version and kdf
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, envelopeFile))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(raw, &m)
}

func wraps(env map[string]any) []any { w, _ := env["wraps"].([]any); return w }

func getWrap(env map[string]any, kind string) map[string]any {
	for _, w := range wraps(env) {
		if m, ok := w.(map[string]any); ok && m["kind"] == kind {
			return m
		}
	}
	return nil
}

// setWrap replaces (or with nil removes) the wrap of a kind, keeping the others in order.
func setWrap(env map[string]any, kind string, wrap map[string]any) {
	var out []any
	replaced := false
	for _, w := range wraps(env) {
		if m, ok := w.(map[string]any); ok && m["kind"] == kind {
			if wrap != nil && !replaced {
				out = append(out, wrap)
				replaced = true
			}
			continue
		}
		out = append(out, w)
	}
	if wrap != nil && !replaced {
		out = append(out, wrap)
	}
	env["wraps"] = out
}

func writeRawEnvelope(dir string, env map[string]any) error {
	js, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, envelopeFile+".tmp")
	if err := os.WriteFile(tmp, js, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, envelopeFile))
}
