package releasesig

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testKey is the throwaway pair every test signs with — never a real key.
func testKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
}

const (
	hashA = "b7dbdad9153bf0a5cdf0d2e8d1bd4c47771a301c20ce46907ee20eb69eba6701"
	hashB = "ba564ec94b1392f28407335239339c40319194adc092991f9792ddb4f98c3ff6"
)

func sampleSums() []byte {
	return FormatSums(map[string]string{
		"Piumy-Setup-9.9.9.exe":          hashA,
		"Piumy-9.9.9-linux-amd64.tar.gz": hashB,
	})
}

func TestSignThenVerify(t *testing.T) {
	key := testKey()
	sums := sampleSums()
	sig := Sign(key, sums)

	if err := Verify(key.Public().(ed25519.PublicKey), sums, sig); err != nil {
		t.Fatalf("Verify of a fresh signature: %v", err)
	}
}

func TestVerifyRejectsWhatWasNotSignedByThisKey(t *testing.T) {
	key := testKey()
	pub := key.Public().(ed25519.PublicKey)
	sums := sampleSums()
	sig := Sign(key, sums)
	otherPub := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize)).Public().(ed25519.PublicKey)

	// A signature over the bare sums, made without the prefix: valid ed25519,
	// but not a release signature — it must not pass.
	bare := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, sums)) + "\n")

	cases := map[string]struct {
		pub  ed25519.PublicKey
		sums []byte
		sig  []byte
	}{
		"tampered sums":              {pub, bytes.Replace(sums, []byte("b7dbda"), []byte("b7dbdb"), 1), sig},
		"another key":                {otherPub, sums, sig},
		"signature of the bare sums": {pub, sums, bare},
		"not base64":                 {pub, sums, []byte("???")},
		"wrong length":               {pub, sums, []byte(base64.StdEncoding.EncodeToString([]byte("corta")))},
		"no public key at all":       {nil, sums, sig},
		"empty signature":            {pub, sums, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Verify(tc.pub, tc.sums, tc.sig); err == nil {
				t.Error("Verify accepted it, want an error")
			}
		})
	}
}

func TestSumsRoundTripSorted(t *testing.T) {
	got, err := ParseSums(sampleSums())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["Piumy-Setup-9.9.9.exe"] != hashA || got["Piumy-9.9.9-linux-amd64.tar.gz"] != hashB {
		t.Errorf("ParseSums = %v", got)
	}
	lines := strings.Split(strings.TrimSpace(string(sampleSums())), "\n")
	if !strings.HasSuffix(lines[0], "Piumy-9.9.9-linux-amd64.tar.gz") {
		t.Errorf("first line = %q, want the names sorted so the file is reproducible", lines[0])
	}
}

// The file is what the signature vouches for: anything odd is an error.
func TestParseSumsIsStrict(t *testing.T) {
	cases := map[string]string{
		"empty":               "",
		"no final newline":    hashA + "  a.exe",
		"one space":           hashA + " a.exe\n",
		"short hash":          hashA[:63] + "  a.exe\n",
		"uppercase hash":      strings.ToUpper(hashA) + "  a.exe\n",
		"not hex":             strings.Repeat("g", 64) + "  a.exe\n",
		"blank line":          hashA + "  a.exe\n\n",
		"a path":              hashA + "  dir/a.exe\n",
		"a windows path":      hashA + `  dir\a.exe` + "\n",
		"dot dot":             hashA + "  ..\n",
		"binary marker star":  hashA + " *a.exe\n",
		"name with a space":   hashA + "  a b.exe\n",
		"the same name twice": hashA + "  a.exe\n" + hashB + "  a.exe\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseSums([]byte(content)); err == nil {
				t.Errorf("ParseSums(%q) = %v, want an error", content, got)
			}
		})
	}
}

func TestParsePrivateKey(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	full := testKey()

	fromSeed, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(seed))
	if err != nil || !fromSeed.Equal(full) {
		t.Errorf("seed form: %v, %v", fromSeed, err)
	}
	fromFull, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(full))
	if err != nil || !fromFull.Equal(full) {
		t.Errorf("64-byte form: %v, %v", fromFull, err)
	}

	mismatched := append([]byte{}, full...)
	mismatched[40] ^= 0xff // the public half no longer belongs to the seed
	secret := "esto-es-un-secreto-que-no-debe-repetirse"
	for name, in := range map[string]string{
		"not base64":        secret,
		"wrong length":      base64.StdEncoding.EncodeToString([]byte(secret)),
		"inconsistent pair": base64.StdEncoding.EncodeToString(mismatched),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePrivateKey(in)
			if err == nil {
				t.Fatal("accepted, want an error")
			}
			if strings.Contains(err.Error(), in) || strings.Contains(err.Error(), secret) {
				t.Errorf("the error repeats the secret: %v", err)
			}
		})
	}
}

func TestGenerateKeyProducesAUsablePair(t *testing.T) {
	pubB64, privB64, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(pubB64)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivateKey(privB64)
	if err != nil {
		t.Fatal(err)
	}
	sums := sampleSums()
	if err := Verify(pub, sums, Sign(priv, sums)); err != nil {
		t.Errorf("a generated pair does not sign and verify: %v", err)
	}
}

func TestParsePublicKeyRejectsWrongSize(t *testing.T) {
	if _, err := ParsePublicKey(base64.StdEncoding.EncodeToString([]byte("corta"))); err == nil {
		t.Error("accepted a short public key")
	}
	if _, err := ParsePublicKey(""); err == nil {
		t.Error("accepted an empty public key — that is how an unconfigured build must stay closed")
	}
}

// SumFile is compared against sha256sum's own answer for a known input, not
// against the function under test.
func TestSumFileMatchesAKnownDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := SumFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const sha256OfABC = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" // FIPS 180-2 test vector
	if got != sha256OfABC {
		t.Errorf("SumFile = %s, want %s", got, sha256OfABC)
	}
}
