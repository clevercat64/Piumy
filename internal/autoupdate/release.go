package autoupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"piumy-gateway/internal/releasesig"
)

const (
	// repo is the ONE repository Piumy updates from. It is a constant, not
	// configuration: a different source of updates is a different build.
	repo = "clevercat64/Piumy"

	maxAPIBody  = 1 << 20  // the release JSON
	maxSumsFile = 64 << 10 // SHA256SUMS and its signature
	// maxPackage is a ceiling, not a measurement: the Setup and the tar.gz are
	// ~18 MB today. Raise it here the day a package really needs more.
	maxPackage = 200 << 20

	apiTimeout      = 30 * time.Second
	downloadTimeout = 15 * time.Minute
)

type release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"` // "sha256:<hex>", computed and published by GitHub
}

// rejection is a failure that is the RELEASE's fault — unsigned, hashes that
// disagree. It is counted and held; anything else (the network, the disk) is
// just tried again on the next window.
type rejection struct{ msg string }

func (r *rejection) Error() string { return r.msg }

func reject(format string, args ...any) error { return &rejection{fmt.Sprintf(format, args...)} }

// refuseDowngrade stops a redirect from leaving https, wherever it points: the
// CDN host differs from github.com, and that is fine — the content is checked
// by hash — but never over a connection anyone can edit.
func refuseDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("demasiadas redirecciones")
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("redirección a una conexión sin cifrar")
	}
	return nil
}

// check looks at the latest release and, when it is newer and not held, brings
// it all the way to Ready.
func (u *Updater) check(ctx context.Context) {
	rel, err := u.fetchLatest(ctx)
	if err != nil {
		u.setError(err)
		return
	}
	u.setError(nil)
	version, ok := versionFromTag(rel.TagName)
	if !ok || rel.Draft || rel.Prerelease {
		return
	}
	u.update(func(s *State) { s.Latest, s.CheckedAt = version, u.now().Unix() })
	if !newer(version, u.cfg.CurrentVersion) || u.held(u.load(), version) {
		return
	}

	ready, err := u.fetchPackage(ctx, rel, version)
	var rej *rejection
	switch {
	case errors.As(err, &rej):
		u.fail(version, rej.msg)
	case err != nil:
		u.setError(err)
	default:
		log.Printf("autoupdate: Piumy %s verificado y listo para instalar", version)
		u.update(func(s *State) { s.Ready = ready })
	}
}

func (u *Updater) fetchLatest(ctx context.Context) (release, error) {
	var rel release
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.apiBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Piumy/"+u.cfg.CurrentVersion)
	resp, err := u.http.Do(req)
	if err != nil {
		return rel, fmt.Errorf("no se pudo consultar la última release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("GitHub respondió %d a la consulta de la última release", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBody)).Decode(&rel); err != nil {
		return rel, fmt.Errorf("la respuesta de GitHub no se entiende: %w", err)
	}
	return rel, nil
}

// fetchPackage runs the whole verification chain for this platform's package:
// signed SHA256SUMS → GitHub's own digest agrees → the downloaded file's
// SHA-256 agrees. Nothing before the signature is trusted, and the file is only
// kept (renamed from .part) once all three hold.
func (u *Updater) fetchPackage(ctx context.Context, rel release, version string) (*Ready, error) {
	if u.pub == nil {
		return nil, errors.New("no hay clave pública de release en esta versión de Piumy: no se puede verificar nada")
	}
	name, ok := packageName(u.goos, u.goarch, version)
	if !ok {
		return nil, fmt.Errorf("no hay paquete de Piumy para %s/%s", u.goos, u.goarch)
	}
	pkg, hasPkg := findAsset(rel, name)
	_, hasSums := findAsset(rel, "SHA256SUMS")
	_, hasSig := findAsset(rel, "SHA256SUMS.sig")
	if !hasSums || !hasSig {
		return nil, reject("la release v%s no está firmada (falta SHA256SUMS o SHA256SUMS.sig)", version)
	}
	if !hasPkg {
		return nil, reject("la release v%s no trae %s", version, name)
	}

	sums, err := u.fetchSmall(ctx, version, "SHA256SUMS")
	if err != nil {
		return nil, err
	}
	sig, err := u.fetchSmall(ctx, version, "SHA256SUMS.sig")
	if err != nil {
		return nil, err
	}
	if err := releasesig.Verify(u.pub, sums, sig); err != nil {
		return nil, reject("firma inválida en la release v%s: %v", version, err)
	}
	table, err := releasesig.ParseSums(sums)
	if err != nil {
		return nil, reject("SHA256SUMS de la release v%s: %v", version, err)
	}
	want, listed := table[name]
	if !listed {
		return nil, reject("SHA256SUMS no lista %s", name)
	}
	// GitHub's own digest of the asset must exist and agree: a second,
	// independent witness, and a way to notice an asset swapped after signing.
	if pkg.Digest != "sha256:"+want {
		return nil, reject("el digest que publica GitHub para %s no coincide con el SHA256SUMS firmado", name)
	}
	if pkg.Size <= 0 || pkg.Size > maxPackage {
		return nil, reject("%s declara un tamaño fuera de lo razonable (%d bytes)", name, pkg.Size)
	}

	dir := filepath.Join(u.updatesDir(), version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	final := filepath.Join(dir, name)
	ready := &Ready{Version: version, File: final, SHA256: want, Sums: string(sums), Sig: string(sig)}
	if sum, err := releasesig.SumFile(final); err == nil && sum == want {
		return ready, nil // already here and right
	}
	if err := u.download(ctx, u.assetURL(version, name), pkg.Size, want, final); err != nil {
		return nil, err
	}
	return ready, nil
}

// verifyReady is the last check before an installer runs, made from what is
// STORED, not from the memory of an earlier look: the stored SHA256SUMS must
// still carry a valid signature, list this platform's package under the name
// this platform installs, with the hash the file has right now — and the file
// must be where the updater put it. A tampered state (a File pointing
// elsewhere, an edited hash, a swapped SHA256SUMS) cannot pass without the
// release key.
func (u *Updater) verifyReady(r *Ready) error {
	if u.pub == nil {
		return errors.New("no hay clave pública de release")
	}
	if !newer(r.Version, u.cfg.CurrentVersion) {
		return fmt.Errorf("la versión %s ya no es más nueva que %s", r.Version, u.cfg.CurrentVersion)
	}
	name, ok := packageName(u.goos, u.goarch, r.Version)
	if !ok || r.File != filepath.Join(u.updatesDir(), r.Version, name) {
		return errors.New("el archivo no es el paquete de esta plataforma en la carpeta de descargas")
	}
	if err := releasesig.Verify(u.pub, []byte(r.Sums), []byte(r.Sig)); err != nil {
		return err
	}
	table, err := releasesig.ParseSums([]byte(r.Sums))
	if err != nil {
		return err
	}
	if table[name] != r.SHA256 {
		return errors.New("el SHA256SUMS firmado no lista este hash para este paquete")
	}
	if sum, err := releasesig.SumFile(r.File); err != nil || sum != r.SHA256 {
		return errors.New("el archivo ya no coincide con su hash firmado")
	}
	return nil
}

func findAsset(rel release, name string) (asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return asset{}, false
}

// assetURL is built here from the pinned repo, the tag and the exact asset
// name. The browser_download_url in the API's answer is never used.
func (u *Updater) assetURL(version, name string) string {
	return u.downloadBase + "/" + repo + "/releases/download/v" + version + "/" + name
}

func (u *Updater) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Piumy/"+u.cfg.CurrentVersion)
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub respondió %d para %s", resp.StatusCode, url)
	}
	return resp, nil
}

// fetchSmall reads SHA256SUMS or its signature, with a hard ceiling.
func (u *Updater) fetchSmall(ctx context.Context, version, name string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	resp, err := u.get(ctx, u.assetURL(version, name))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSumsFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSumsFile {
		return nil, reject("%s de la release v%s pasa de %d bytes", name, version, maxSumsFile)
	}
	return data, nil
}

// download streams the package to final+".part", hashing as it goes, and only
// renames it into place when it is exactly size bytes and hashes to want.
func (u *Updater) download(ctx context.Context, url string, size int64, want, final string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	resp, err := u.get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	part := final + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, size+1))
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		os.Remove(part)
		return fmt.Errorf("la descarga se cortó: %w", err)
	}
	if n != size {
		os.Remove(part)
		return fmt.Errorf("la descarga trae %d bytes y la release declara %d", n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(part)
		return reject("el archivo bajado no coincide con el SHA-256 firmado (trae %s, debía ser %s)", got, want)
	}
	return os.Rename(part, final)
}
