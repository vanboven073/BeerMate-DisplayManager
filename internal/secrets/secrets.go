// Package secrets provides authenticated encryption for third-party credentials
// held at rest (social API tokens, KPI endpoint authentication).
//
// The key lives in /etc/beermate-display-manager/secret.key with mode 0600, is
// generated at install time, and is never committed or included in a downloadable
// backup. A stolen database therefore yields ciphertext only.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

var (
	// ErrKeyMissing is returned when the key file does not exist.
	ErrKeyMissing = errors.New("secrets: key file does not exist")
	// ErrDecrypt is returned for any decryption failure. It is deliberately
	// non-specific: distinguishing "wrong key" from "corrupt ciphertext" from
	// "tampered tag" would hand an attacker an oracle.
	ErrDecrypt = errors.New("secrets: decryption failed")
)

// Box performs AEAD encryption with a fixed key.
type Box struct {
	aead cipher.AEAD
}

// NewBox builds a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Encrypt seals plaintext, returning the ciphertext and the nonce used.
//
// The nonce is random per call. GCM nonce reuse under one key is catastrophic
// (it leaks the authentication subkey), so the nonce is never derived from a
// counter or from the plaintext.
func (b *Box) Encrypt(plaintext []byte, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	ciphertext = b.aead.Seal(nil, nonce, plaintext, aad)
	return ciphertext, nonce, nil
}

// Decrypt opens a ciphertext produced by Encrypt with the same aad.
func (b *Box) Decrypt(ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(nonce) != b.aead.NonceSize() {
		return nil, ErrDecrypt
	}
	out, err := b.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

// EncryptString is a convenience wrapper returning base64 for logging-free storage.
func (b *Box) EncryptString(s string, aad []byte) (ct, nonce string, err error) {
	c, n, err := b.Encrypt([]byte(s), aad)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(c), base64.StdEncoding.EncodeToString(n), nil
}

// DecryptString reverses EncryptString.
func (b *Box) DecryptString(ct, nonce string, aad []byte) (string, error) {
	c, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		return "", ErrDecrypt
	}
	n, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return "", ErrDecrypt
	}
	out, err := b.Decrypt(c, n, aad)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GenerateKey returns a fresh random 32-byte key.
func GenerateKey() ([]byte, error) {
	k := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		return nil, fmt.Errorf("secrets: generate key: %w", err)
	}
	return k, nil
}

// LoadKey reads a base64 key file.
func LoadKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrKeyMissing
		}
		return nil, fmt.Errorf("secrets: read key: %w", err)
	}
	return decodeKey(raw)
}

func decodeKey(raw []byte) ([]byte, error) {
	s := trimSpace(string(raw))
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("secrets: key file is not valid base64: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets: key must decode to %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}

// LoadOrCreateKey reads the key at path, generating it if absent.
//
// The file is created with mode 0600 via O_EXCL so a concurrent installer cannot
// race two different keys into place — losing that race would silently make every
// previously stored credential undecryptable.
func LoadOrCreateKey(path string) ([]byte, error) {
	key, err := LoadKey(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrKeyMissing) {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("secrets: create key dir: %w", err)
	}
	newKey, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(newKey) + "\n"

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Another process won the race; use whatever it wrote.
			return LoadKey(path)
		}
		return nil, fmt.Errorf("secrets: create key file: %w", err)
	}
	if _, err := f.WriteString(encoded); err != nil {
		f.Close()
		return nil, fmt.Errorf("secrets: write key file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secrets: close key file: %w", err)
	}
	// Re-assert permissions: the process umask can widen the O_CREATE mode.
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secrets: chmod key file: %w", err)
	}
	return newKey, nil
}

// CheckKeyPermissions verifies the key file is not reachable by other users.
// Reported as a health warning rather than a fatal error so a permission slip
// does not take the display offline.
//
// World bits are what matter. Group read is expected on a real install: the
// installer writes the key as root:beermate with mode 0640 so the service account
// can read a file it does not own, which is a tighter arrangement than making the
// service the owner. Group *write* is not — that would let the group replace the
// key and silently orphan every stored credential.
func CheckKeyPermissions(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := fi.Mode().Perm()
	if mode&0o007 != 0 {
		return fmt.Errorf("secrets: %s has mode %04o; it must not be accessible to other users "+
			"(expected 0600, or 0640 owned by the service group)", path, mode)
	}
	if mode&0o020 != 0 {
		return fmt.Errorf("secrets: %s has mode %04o; it must not be group-writable", path, mode)
	}
	return nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
