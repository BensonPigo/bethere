// Package auth makes and checks BeThere's credentials: role tokens and
// event recovery codes (access-control.md sections 2 and 3, tech-stack.md
// T6, api.md A2).
//
// The server and every subcommand hash credentials with the functions
// here and nowhere else: a second, different rule would make every link
// look invalid and wipe every device's offline copy (api.md F5).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
	"unicode"
)

const (
	// randomBytes is the entropy of a token and of a recovery code (128 bits).
	randomBytes = 16
	// TokenLen is the length of a token: 16 bytes in base64url, no padding.
	TokenLen = 22
	// recoveryCodeLen is 16 bytes in Crockford Base32, no padding.
	recoveryCodeLen = 26
	// recoveryGroup is how many characters are shown between hyphens.
	recoveryGroup = 4
)

var (
	ErrTokenFormat        = errors.New("token is not 22 base64url characters encoding 16 bytes")
	ErrRecoveryCodeFormat = errors.New("recovery code is not 26 Crockford Base32 characters encoding 16 bytes")
)

var tokenEncoding = base64.RawURLEncoding.Strict()

// crockford is Crockford's Base32 alphabet (no I, L, O, U), unpadded.
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewToken returns a fresh token: 16 bytes from crypto/rand in base64url
// without padding, 22 characters.
func NewToken() (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenEncoding.EncodeToString(b), nil
}

// CheckToken reports whether s is exactly a token as NewToken makes it:
// 22 characters of the base64url alphabet that decode, strictly, to 16
// bytes. Anything else is a malformed request, never an invalid link
// (api.md section 1).
func CheckToken(s string) error {
	if len(s) != TokenLen {
		return ErrTokenFormat
	}
	b, err := tokenEncoding.DecodeString(s)
	if err != nil || len(b) != randomBytes {
		return ErrTokenFormat
	}
	return nil
}

// HashToken is the stored and looked-up form of a token: SHA-256 of the
// token's 22 ASCII characters themselves, not of the decoded bytes (A2).
// The caller checks the format first.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// NewRecoveryCode returns a fresh recovery code for display: 16 bytes from
// crypto/rand in Crockford Base32 (26 characters), grouped by four with
// hyphens, for example "7K3M-9QXR-...-T4".
func NewRecoveryCode() (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := crockford.EncodeToString(b)
	var sb strings.Builder
	for i := 0; i < len(code); i += recoveryGroup {
		if i > 0 {
			sb.WriteByte('-')
		}
		sb.WriteString(code[i:min(i+recoveryGroup, len(code))])
	}
	return sb.String(), nil
}

// NormalizeRecoveryCode turns a recovery code as typed from paper into the
// form that is hashed. The normalization is exactly:
//
//  1. remove every hyphen ('-') and every Unicode white-space character;
//  2. upper-case ASCII letters.
//
// The result must then be 26 characters of the Crockford alphabet that
// decode, strictly, to 16 bytes; otherwise ErrRecoveryCodeFormat.
// Crockford's look-alike substitutions (O→0, I/L→1) are not applied: a
// generated code never contains those letters, so a code that does is
// mistyped and fails the format check instead of matching by accident.
func NormalizeRecoveryCode(s string) (string, error) {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == '-' || unicode.IsSpace(r):
			continue
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r - 'a' + 'A')
		default:
			sb.WriteRune(r)
		}
	}
	code := sb.String()
	if len(code) != recoveryCodeLen {
		return "", ErrRecoveryCodeFormat
	}
	if b, err := crockford.DecodeString(code); err != nil || len(b) != randomBytes || crockford.EncodeToString(b) != code {
		return "", ErrRecoveryCodeFormat
	}
	return code, nil
}

// HashRecoveryCode is the stored and looked-up form of a recovery code:
// SHA-256 of its normalized form (NormalizeRecoveryCode) as ASCII.
func HashRecoveryCode(s string) ([]byte, error) {
	code, err := NormalizeRecoveryCode(s)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(code))
	return h[:], nil
}
