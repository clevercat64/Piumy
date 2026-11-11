// Package releasesig is the format and the signature of a release's
// SHA256SUMS (T176, ct-2026-11-10-2103): the release publishes SHA256SUMS plus
// SHA256SUMS.sig, an ed25519 signature over it. Piumy ships the public key and
// verifies the signature before it trusts a single hash in the file; the
// maintainers' signing tool (cmd/signrelease) is the only thing that signs.
// Both sides use THIS package, so the format cannot drift between them.
//
// Standard library only: nothing here knows about Piumy.
package releasesig

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// signedPrefix is prepended to what gets signed, so a signature made here can
// never be replayed as a signature of some other message under the same key.
const signedPrefix = "piumy-release-sums-v1\n"

// FormatSums writes name→sha256 in the `sha256sum` layout ("<hex>  <name>"),
// one line per file, sorted by name so the output is reproducible.
func FormatSums(sums map[string]string) []byte {
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[name], name)
	}
	return []byte(b.String())
}

// ParseSums reads what FormatSums writes, strictly: this file is what the
// signature vouches for, so anything odd (a path, an uppercase or short hash, a
// blank line, a repeated name) is an error, never silently skipped.
func ParseSums(data []byte) (map[string]string, error) {
	text := string(data)
	if !strings.HasSuffix(text, "\n") {
		return nil, errors.New("releasesig: SHA256SUMS must end with a newline")
	}
	sums := map[string]string{}
	for i, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || !validHash(sum) || !validName(name) {
			return nil, fmt.Errorf("releasesig: SHA256SUMS line %d is not \"<64 lowercase hex>  <file name>\"", i+1)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("releasesig: SHA256SUMS lists %q twice", name)
		}
		sums[name] = sum
	}
	return sums, nil
}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validName accepts a plain file name only: it becomes part of a download URL
// and a path on disk, so no separators, no dot-names, no spaces or controls.
func validName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if c <= ' ' || c == 0x7f || c == '/' || c == '\\' {
			return false
		}
	}
	return true
}

// Sign returns the content of SHA256SUMS.sig: the base64 of the ed25519
// signature over the sums, plus a newline.
func Sign(key ed25519.PrivateKey, sums []byte) []byte {
	sig := ed25519.Sign(key, append([]byte(signedPrefix), sums...))
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

// Verify checks that sig (the content of SHA256SUMS.sig) is a valid signature
// of sums under pub.
func Verify(pub ed25519.PublicKey, sums, sig []byte) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("releasesig: SHA256SUMS.sig is not a base64 ed25519 signature")
	}
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("releasesig: no valid public key to verify with")
	}
	if !ed25519.Verify(pub, append([]byte(signedPrefix), sums...), raw) {
		return errors.New("releasesig: the signature does not match SHA256SUMS")
	}
	return nil
}

// ParsePublicKey reads the base64 of a 32-byte ed25519 public key.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("releasesig: not a base64 ed25519 public key (32 bytes)")
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivateKey reads the base64 of an ed25519 private key: the 32-byte seed
// or the 64-byte form. Errors never repeat the input: it is a secret.
func ParsePrivateKey(b64 string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, errors.New("releasesig: the private key is not valid base64")
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		key := ed25519.PrivateKey(raw)
		if !key.Equal(ed25519.NewKeyFromSeed(raw[:ed25519.SeedSize])) {
			return nil, errors.New("releasesig: the private key's public half does not match its seed")
		}
		return key, nil
	}
	return nil, errors.New("releasesig: the private key must be 32 or 64 bytes of base64")
}

// GenerateKey makes a fresh key pair, both halves as base64 (private = seed).
func GenerateKey() (public, private string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv.Seed()), nil
}

// SumFile is the SHA-256 of a file, lowercase hex.
func SumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
