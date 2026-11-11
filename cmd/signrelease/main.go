// Command signrelease is what the maintainers run to sign a release (T176,
// ct-2026-11-10-2103): it writes SHA256SUMS and SHA256SUMS.sig next to the
// release files, and Piumy refuses to self-update from a release without a
// valid signature.
//
//	signrelease keygen
//	signrelease sign <out-dir> <file>...
//
// The private key is read ONLY from the PIUMY_RELEASE_SIGNING_KEY environment
// variable — never from an argument or a file, so it can be handed over by the
// vault (vault_run) and never lands on disk or in a command line. Nothing here
// ever prints it back.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"piumy-gateway/internal/releasesig"
)

const (
	keyEnv   = "PIUMY_RELEASE_SIGNING_KEY"
	sumsName = "SHA256SUMS"
	sigName  = "SHA256SUMS.sig"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "uso: signrelease keygen | signrelease sign <carpeta-de-salida> <archivo>...")
		return 2
	}
	switch args[0] {
	case "keygen":
		return keygen(stdout, stderr)
	case "sign":
		return sign(args[1:], stdout, stderr, getenv(keyEnv))
	}
	fmt.Fprintf(stderr, "signrelease: comando desconocido %q (keygen o sign)\n", args[0])
	return 2
}

// keygen is for whoever owns the release key: the private half goes to the
// vault and nowhere else; the public half is the one constant in
// internal/autoupdate/pubkey.go.
func keygen(stdout, stderr io.Writer) int {
	pub, priv, err := releasesig.GenerateKey()
	if err != nil {
		fmt.Fprintf(stderr, "signrelease: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "PUBLICA  %s\nPRIVADA  %s\n", pub, priv)
	return 0
}

func sign(args []string, stdout, stderr io.Writer, privateKey string) int {
	if privateKey == "" {
		fmt.Fprintf(stderr, "signrelease: falta la variable %s con la clave privada (córrelo con vault_run)\n", keyEnv)
		return 2
	}
	if len(args) < 2 {
		fmt.Fprintln(stderr, "uso: signrelease sign <carpeta-de-salida> <archivo>...")
		return 2
	}
	key, err := releasesig.ParsePrivateKey(privateKey)
	if err != nil {
		fmt.Fprintf(stderr, "signrelease: %s: %v\n", keyEnv, err)
		return 1
	}

	outDir, files := args[0], args[1:]
	sums := map[string]string{}
	for _, file := range files {
		name := filepath.Base(file)
		if name == sumsName || name == sigName {
			fmt.Fprintf(stderr, "signrelease: %s no se firma a sí mismo\n", name)
			return 1
		}
		if _, dup := sums[name]; dup {
			fmt.Fprintf(stderr, "signrelease: dos archivos se llaman %s: la release no podría distinguirlos\n", name)
			return 1
		}
		sum, err := releasesig.SumFile(file)
		if err != nil {
			fmt.Fprintf(stderr, "signrelease: %v\n", err)
			return 1
		}
		sums[name] = sum
	}
	content := releasesig.FormatSums(sums)
	// What Piumy will parse must parse here too: a name it would refuse
	// (a space, say) is a release that can never self-update.
	if _, err := releasesig.ParseSums(content); err != nil {
		fmt.Fprintf(stderr, "signrelease: %v\n", err)
		return 1
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "signrelease: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(outDir, sumsName), content, 0o644); err != nil {
		fmt.Fprintf(stderr, "signrelease: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(outDir, sigName), releasesig.Sign(key, content), 0o644); err != nil {
		fmt.Fprintf(stderr, "signrelease: %v\n", err)
		return 1
	}
	// The public half, so the signer can check it is the one built into Piumy.
	fmt.Fprintf(stdout, "firmados %d archivos en %s (%s y %s)\nclave pública %s\n",
		len(files), outDir, sumsName, sigName, base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	return 0
}
