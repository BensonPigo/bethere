package auth

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// The hash rule is pinned by fixed vectors computed outside Go
// (printf '%s' <token> | sha256sum). A change here changes every stored
// hash and would make every existing link "invalid" (api.md F5).
func TestTokenHashVectors(t *testing.T) {
	for _, v := range []struct{ token, hash string }{
		{"AAAAAAAAAAAAAAAAAAAAAA", "8a5bdb4cc15164126c6ef2668de9dd240d299ce6397a42c95a9411b93d080ed8"},
		{"q83vEjRWeJA_-wEjRWeJqw", "3c5f2a66fe786468df9b4465e006520267b5fb60b0c7ae98c0c9ceaa4c2e1937"},
	} {
		if err := CheckToken(v.token); err != nil {
			t.Fatalf("%s: %v", v.token, err)
		}
		if got := hex.EncodeToString(HashToken(v.token)); got != v.hash {
			t.Errorf("HashToken(%q) = %s, want %s", v.token, got, v.hash)
		}
	}
}

func TestNewTokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != TokenLen || CheckToken(tok) != nil {
			t.Fatalf("NewToken() = %q, not a valid token", tok)
		}
		if seen[tok] {
			t.Fatalf("NewToken repeated %q", tok)
		}
		seen[tok] = true
	}
}

func TestCheckTokenIsStrict(t *testing.T) {
	for _, bad := range []string{
		"",
		"AAAAAAAAAAAAAAAAAAAAA",          // 21
		"AAAAAAAAAAAAAAAAAAAAAAA",        // 23
		"AAAAAAAAAAAAAAAAAAAAAA==",       // padded
		"AAAAAAAAAAAAAAAAAAAA+A",         // standard alphabet
		"AAAAAAAAAAAAAAAAAAAA/A",         // standard alphabet
		"AAAAAAAAAAAAAAAAAAAAAB",         // non-zero trailing bits: not canonical
		"AAAAAAAAAAAAAAAAAAAA A",         // space
		"AAAAAAAAAAAAAAAAAAAAA\n",        // newline
		"ＡAAAAAAAAAAAAAAAAAAAA",          // non-ASCII
		strings.Repeat("A", 21) + "\x00", // NUL
	} {
		if err := CheckToken(bad); !errors.Is(err, ErrTokenFormat) {
			t.Errorf("CheckToken(%q) = %v, want ErrTokenFormat", bad, err)
		}
	}
}

func TestRecoveryCodeVectorAndNormalization(t *testing.T) {
	// Bytes 00..0f in Crockford Base32 and SHA-256 of that ASCII string,
	// computed outside Go.
	const code = "000G40R40M30E209185GR38E1W"
	const want = "d8c08ea28d68d57e2ec74c6df55bf6d638881fec30f9ccf0154f8d81aee36549"
	for _, typed := range []string{
		code,
		"000G-40R4-0M30-E209-185G-R38E-1W",
		"000g 40r4 0m30 e209 185g r38e 1w",
		" 000G-40R4\t0M30-E209-185G-R38E-1W\n",
	} {
		h, err := HashRecoveryCode(typed)
		if err != nil {
			t.Fatalf("%q: %v", typed, err)
		}
		if got := hex.EncodeToString(h); got != want {
			t.Errorf("HashRecoveryCode(%q) = %s, want %s", typed, got, want)
		}
	}
	for _, bad := range []string{
		"",
		code[:25],
		code + "0",
		"000G40R40M30E209185GR38E1O", // O is not in the alphabet
		"000G40R40M30E209185GR38E1U",
		"000G40R40M30E209185GR38E1X", // non-zero trailing bits: not canonical
		"000G_40R40M30E209185GR38E1W",
	} {
		if _, err := HashRecoveryCode(bad); !errors.Is(err, ErrRecoveryCodeFormat) {
			t.Errorf("HashRecoveryCode(%q) = %v, want ErrRecoveryCodeFormat", bad, err)
		}
	}
}

func TestNewRecoveryCodeRoundTrips(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		display, err := NewRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		groups := strings.Split(display, "-")
		if len(groups) != 7 || len(groups[6]) != 2 {
			t.Fatalf("display %q: want six groups of four and one of two", display)
		}
		code, err := NormalizeRecoveryCode(display)
		if err != nil {
			t.Fatalf("normalize %q: %v", display, err)
		}
		if seen[code] {
			t.Fatalf("NewRecoveryCode repeated %q", code)
		}
		seen[code] = true
		lower, err := NormalizeRecoveryCode(strings.ToLower(strings.ReplaceAll(display, "-", " ")))
		if err != nil || lower != code {
			t.Errorf("typed in lower case with spaces: %q (%v), want %q", lower, err, code)
		}
	}
}
