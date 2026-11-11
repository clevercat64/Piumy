package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"piumy-gateway/internal/releasesig"
)

// runTool calls the tool the way main does, with a throwaway key in the
// environment it is handed (never a real one).
func runTool(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut, func(k string) string { return env[k] })
	return code, out.String(), errOut.String()
}

// writeFiles makes the files to sign; "abc" hashes to the FIPS 180-2 test
// vector, so the expected digest does not come from the code under test.
func writeFiles(t *testing.T) (abc, empty string) {
	t.Helper()
	dir := t.TempDir()
	abc, empty = filepath.Join(dir, "Piumy-9.9.9.tar.gz"), filepath.Join(dir, "Piumy-Setup-9.9.9.exe")
	if err := os.WriteFile(abc, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return abc, empty
}

const (
	sha256OfABC   = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	sha256OfEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func TestSignWritesSumsAndAVerifiableSignature(t *testing.T) {
	pub, priv, err := releasesig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	abc, empty := writeFiles(t)
	out := filepath.Join(t.TempDir(), "release")

	code, stdout, stderr := runTool(t, map[string]string{keyEnv: priv}, "sign", out, abc, empty)

	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(out, "SHA256SUMS.sig"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := releasesig.ParseSums(sums)
	if err != nil || parsed["Piumy-9.9.9.tar.gz"] != sha256OfABC || parsed["Piumy-Setup-9.9.9.exe"] != sha256OfEmpty {
		t.Fatalf("SHA256SUMS = %q (%v), want the known digests of the two files", sums, err)
	}
	// The same check Piumy makes, with the public half the tool printed.
	pubKey, err := releasesig.ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := releasesig.Verify(pubKey, sums, sig); err != nil {
		t.Errorf("Piumy would refuse this release: %v", err)
	}
	if !strings.Contains(stdout, pub) {
		t.Errorf("stdout = %q, want it to show the public key so the signer can compare it with the built-in one", stdout)
	}
	if strings.Contains(stdout+stderr, priv) {
		t.Error("the tool printed the private key")
	}
}

func TestSignRefusesWithoutAKeyAndWritesNothing(t *testing.T) {
	abc, _ := writeFiles(t)
	out := filepath.Join(t.TempDir(), "release")

	code, _, stderr := runTool(t, nil, "sign", out, abc)

	if code == 0 || !strings.Contains(stderr, keyEnv) {
		t.Errorf("exit %d, stderr %q: want a refusal that names %s", code, stderr, keyEnv)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("it created the output directory without a key")
	}
}

func TestSignRefusesABadKeyWithoutEchoingIt(t *testing.T) {
	abc, _ := writeFiles(t)
	secret := "no-soy-una-clave-valida-pero-tampoco-debo-repetirme"

	code, stdout, stderr := runTool(t, map[string]string{keyEnv: secret}, "sign", t.TempDir(), abc)

	if code == 0 {
		t.Fatal("signed with a bad key")
	}
	if strings.Contains(stdout+stderr, secret) {
		t.Errorf("the tool echoed the key: %q", stdout+stderr)
	}
}

func TestSignRefusesWhatPiumyCouldNotUse(t *testing.T) {
	_, priv, _ := releasesig.GenerateKey()
	env := map[string]string{keyEnv: priv}
	abc, _ := writeFiles(t)
	dupDir := t.TempDir()
	dup := filepath.Join(dupDir, filepath.Base(abc))
	if err := os.WriteFile(dup, []byte("otro"), 0o600); err != nil {
		t.Fatal(err)
	}
	spaced := filepath.Join(t.TempDir(), "con espacio.exe")
	if err := os.WriteFile(spaced, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	selfish := filepath.Join(t.TempDir(), "SHA256SUMS")
	if err := os.WriteFile(selfish, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]string{
		"two files with the same name": {abc, dup},
		"a name Piumy would refuse":    {spaced},
		"the sums file itself":         {selfish},
		"a file that doesn't exist":    {filepath.Join(t.TempDir(), "no-existe")},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "release")
			code, _, _ := runTool(t, env, append([]string{"sign", out}, files...)...)
			if code == 0 {
				t.Error("signed it, want a refusal")
			}
			if _, err := os.Stat(filepath.Join(out, "SHA256SUMS.sig")); err == nil {
				t.Error("left a signature behind for a release that was refused")
			}
		})
	}
}

func TestKeygenMakesAPairThatSigns(t *testing.T) {
	code, stdout, stderr := runTool(t, nil, "keygen")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	fields := strings.Fields(stdout)
	if len(fields) != 4 || fields[0] != "PUBLICA" || fields[2] != "PRIVADA" {
		t.Fatalf("keygen output = %q", stdout)
	}
	if _, err := releasesig.ParsePublicKey(fields[1]); err != nil {
		t.Errorf("the public key doesn't parse: %v", err)
	}
	abc, _ := writeFiles(t)
	if code, _, stderr := runTool(t, map[string]string{keyEnv: fields[3]}, "sign", t.TempDir(), abc); code != 0 {
		t.Errorf("the generated private key can't sign: %s", stderr)
	}
}

func TestUnknownCommandAndNoArgsAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"firmar"}} {
		if code, _, _ := runTool(t, nil, args...); code == 0 {
			t.Errorf("run(%v) = 0, want a usage error", args)
		}
	}
}
