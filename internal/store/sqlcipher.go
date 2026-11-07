package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"os"

	"golang.org/x/crypto/pbkdf2"
)

// Pure-Go SQLCipher 4 page decryption, so the store needs no cgo/SQLCipher library (the plaintext is
// then read with modernc.org/sqlite). Defaults match the Rust store: page size 4096, AES-256-CBC,
// HMAC-SHA512, per-page IV, HMAC key derived from the raw key (PBKDF2-HMAC-SHA512, 2 iterations,
// salt masked with 0x3a). The raw 32-byte key is the AES key (no PBKDF2 for the key itself).
const (
	pageSize = 4096
	ivSize   = 16
	hmacSize = 64
	reserve  = ivSize + hmacSize // 80, a multiple of the AES block size
	saltSize = 16
	saltMask = 0x3a
	fastIter = 2
)

// sqliteHeader is the 16-byte magic SQLCipher replaces with the salt on page 1; we restore it.
var sqliteHeader = []byte("SQLite format 3\x00")

// decryptDB returns the plaintext SQLite image of a SQLCipher 4 database encrypted with the raw key.
func decryptDB(path string, key [32]byte) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < pageSize || len(raw)%pageSize != 0 {
		return nil, errors.New("not a SQLCipher database (unexpected size)")
	}
	salt := raw[:saltSize]
	hmacSalt := make([]byte, saltSize)
	for i := range salt {
		hmacSalt[i] = salt[i] ^ saltMask
	}
	hmacKey := pbkdf2.Key(key[:], hmacSalt, fastIter, 32, sha512.New)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(raw))
	pages := len(raw) / pageSize
	for i := 0; i < pages; i++ {
		page := raw[i*pageSize : (i+1)*pageSize]
		pgno := uint32(i + 1)
		// SQLCipher leaves wholly unused pages as zero; pass them through.
		if isZero(page) {
			out = append(out, page...)
			continue
		}
		offset := 0
		if pgno == 1 {
			offset = saltSize // the salt replaces the 16-byte header on page 1
		}
		iv := page[pageSize-reserve : pageSize-reserve+ivSize]
		storedHMAC := page[pageSize-hmacSize:]
		// HMAC covers the encrypted payload and the IV, then the page number (little-endian u32).
		mac := hmac.New(sha512.New, hmacKey)
		mac.Write(page[offset : pageSize-hmacSize])
		var pg [4]byte
		binary.LittleEndian.PutUint32(pg[:], pgno)
		mac.Write(pg[:])
		if subtle.ConstantTimeCompare(mac.Sum(nil), storedHMAC) != 1 {
			return nil, errors.New("wrong master password or not a rustXterm database")
		}
		ct := page[offset : pageSize-reserve]
		pt := make([]byte, len(ct))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
		if pgno == 1 {
			out = append(out, sqliteHeader...)
		}
		out = append(out, pt...)
		out = append(out, make([]byte, reserve)...) // reserved region (SQLite ignores it)
	}
	return out, nil
}

// encryptDB is the inverse of decryptDB: it turns a plaintext SQLite image (page size 4096 with an
// 80-byte reserved region per page, as SQLCipher leaves and modernc preserves) back into a SQLCipher
// 4 file, reusing the original 16-byte salt so the same key keeps decrypting it. Each non-zero page
// gets a fresh random IV; the HMAC covers the ciphertext, IV and page number (little-endian u32).
func encryptDB(plain []byte, key [32]byte, salt []byte) ([]byte, error) {
	if len(plain) < pageSize || len(plain)%pageSize != 0 {
		return nil, errors.New("plaintext image has unexpected size")
	}
	if len(salt) != saltSize {
		return nil, errors.New("salt must be 16 bytes")
	}
	hmacSalt := make([]byte, saltSize)
	for i := range salt {
		hmacSalt[i] = salt[i] ^ saltMask
	}
	hmacKey := pbkdf2.Key(key[:], hmacSalt, fastIter, 32, sha512.New)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(plain))
	pages := len(plain) / pageSize
	for i := 0; i < pages; i++ {
		page := plain[i*pageSize : (i+1)*pageSize]
		pgno := uint32(i + 1)
		if isZero(page) { // unused pages stay zero, as decryptDB passes them through
			out = append(out, page...)
			continue
		}
		offset := 0
		if pgno == 1 {
			offset = saltSize // the salt takes the place of the 16-byte header on page 1
		}
		data := page[offset : pageSize-reserve] // real bytes; a multiple of the AES block size
		iv := make([]byte, ivSize)
		if _, err := rand.Read(iv); err != nil {
			return nil, err
		}
		ct := make([]byte, len(data))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, data)

		enc := make([]byte, 0, pageSize)
		if pgno == 1 {
			enc = append(enc, salt...)
		}
		enc = append(enc, ct...)
		enc = append(enc, iv...)
		mac := hmac.New(sha512.New, hmacKey)
		mac.Write(enc[offset:]) // ciphertext + IV (the salt on page 1 is not covered)
		var pg [4]byte
		binary.LittleEndian.PutUint32(pg[:], pgno)
		mac.Write(pg[:])
		enc = append(enc, mac.Sum(nil)...)
		if len(enc) != pageSize {
			return nil, errors.New("encrypted page size mismatch")
		}
		out = append(out, enc...)
	}
	return out, nil
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
