// Package auth implements password hashing, sessions, CSRF and login throttling.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters.
//
// Memory is 64 MiB rather than the 128 MiB some guides suggest. The Jetson Nano
// has 4 GB shared with the desktop and two Chromium processes, and the admin
// surface is reachable only over Tailscale. 64 MiB with t=2 still costs an
// attacker vastly more than bcrypt at any practical cost factor, and the
// concurrency semaphore below is what actually bounds memory use under load.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024 // KiB
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// MinPasswordLength is the shortest accepted password.
//
// Length is the dominant factor in password strength, so this is enforced strictly
// while composition rules are advisory. Mandatory character-class rules push users
// toward predictable substitutions ("Password1!") without adding real entropy.
const MinPasswordLength = 12

// MaxPasswordLength bounds the input to keep hashing time predictable.
const MaxPasswordLength = 1024

var (
	// ErrPasswordMismatch means the supplied password did not verify.
	ErrPasswordMismatch = errors.New("auth: password does not match")
	// ErrInvalidHash means the stored hash string is malformed.
	ErrInvalidHash = errors.New("auth: stored password hash is malformed")
)

// Hasher produces and verifies Argon2id hashes with bounded concurrency.
type Hasher struct {
	// sem bounds simultaneous hashes. Each costs argonMemory, so without it a
	// burst of login attempts is a memory-exhaustion vector on a 4 GB device.
	// Rate limiting does not cover this: those limits are per-IP and per-account,
	// and an attacker varying either could still drive concurrent hashing.
	sem chan struct{}

	time    uint32
	memory  uint32
	threads uint8
	keyLen  uint32
}

// NewHasher builds a Hasher permitting maxConcurrent simultaneous operations.
func NewHasher(maxConcurrent int) *Hasher {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	threads := uint8(runtime.NumCPU())
	if threads > 4 {
		threads = 4 // the Jetson has 4 cores; more parallelism just thrashes
	}
	if threads < 1 {
		threads = 1
	}
	return &Hasher{
		sem:     make(chan struct{}, maxConcurrent),
		time:    argonTime,
		memory:  argonMemory,
		threads: threads,
		keyLen:  argonKeyLen,
	}
}

func (h *Hasher) acquire() { h.sem <- struct{}{} }
func (h *Hasher) release() { <-h.sem }

// Hash returns an encoded Argon2id hash in the standard PHC string format:
//
//	$argon2id$v=19$m=65536,t=2,p=4$<b64 salt>$<b64 hash>
func (h *Hasher) Hash(password string) (string, error) {
	if err := ValidatePasswordStrength(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}

	h.acquire()
	key := argon2.IDKey([]byte(password), salt, h.time, h.memory, h.threads, h.keyLen)
	h.release()

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.memory, h.time, h.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify checks a password against an encoded hash.
//
// Parameters are read from the stored hash rather than from the current config,
// so hashes created under older settings keep verifying after a parameter change.
func (h *Hasher) Verify(password, encoded string) error {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}
	if len(password) > MaxPasswordLength {
		return ErrPasswordMismatch
	}

	h.acquire()
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, uint32(len(want)))
	h.release()

	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NeedsRehash reports whether a stored hash uses weaker parameters than current.
func (h *Hasher) NeedsRehash(encoded string) bool {
	p, _, _, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return p.memory < h.memory || p.time < h.time
}

type hashParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (p hashParams, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, key]
	if len(parts) != 6 || parts[0] != "" {
		return p, nil, nil, ErrInvalidHash
	}
	if parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("%w: unsupported algorithm %q", ErrInvalidHash, parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidHash, version)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return p, nil, nil, ErrInvalidHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if len(salt) == 0 || len(key) == 0 {
		return p, nil, nil, ErrInvalidHash
	}
	return p, salt, key, nil
}

// ValidatePasswordStrength enforces the password policy.
func ValidatePasswordStrength(pw string) error {
	n := len([]rune(pw))
	if n < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(pw) > MaxPasswordLength {
		return fmt.Errorf("password must be at most %d bytes", MaxPasswordLength)
	}
	if strings.TrimSpace(pw) == "" {
		return errors.New("password must not be only whitespace")
	}
	// Reject a single repeated character ("aaaaaaaaaaaa" passes a length check
	// but has almost no entropy).
	first := []rune(pw)[0]
	same := true
	for _, r := range pw {
		if r != first {
			same = false
			break
		}
	}
	if same {
		return errors.New("password must not be a single repeated character")
	}
	if isCommonPassword(pw) {
		return errors.New("password is too common; choose something less predictable")
	}
	return nil
}

// commonPasswords are rejected outright. This is a short list of what people
// actually pick for an internal appliance, not a substitute for a breach corpus.
var commonPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "password123": {}, "passw0rd": {},
	"123456": {}, "12345678": {}, "123456789": {}, "1234567890": {},
	"qwerty": {}, "qwerty123": {}, "letmein": {}, "welcome": {},
	"admin": {}, "administrator": {}, "changeme": {}, "secret": {},
	"beermate": {}, "beermate123": {}, "beermate2026": {}, "displaymanager": {},
	"iloveyou": {}, "sunshine": {}, "princess": {}, "football": {},
	"monkey": {}, "dragon": {}, "abc123": {}, "111111": {},
}

func isCommonPassword(pw string) bool {
	norm := strings.ToLower(strings.TrimSpace(pw))
	if _, bad := commonPasswords[norm]; bad {
		return true
	}
	// Also reject trivial trailing-digit variants, e.g. "beermate2026!".
	trimmed := strings.TrimRightFunc(norm, func(r rune) bool {
		return unicode.IsDigit(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	if trimmed != norm && len(trimmed) >= 5 {
		if _, bad := commonPasswords[trimmed]; bad {
			return true
		}
	}
	return false
}
