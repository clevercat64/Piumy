package autoupdate

// releasePublicKey is the ed25519 public key (base64) Piumy verifies every
// release's SHA256SUMS.sig with. The matching private key lives only in the
// maintainers' vault and is used by cmd/signrelease (PIUMY_RELEASE_SIGNING_KEY).
//
// Generated 2026-11-11 with `go run ./cmd/signrelease keygen` (vault module
// "Piumy firma"). Changing it means every installation built with the old one
// can no longer verify a release: they update by hand once. An empty value
// still looks for updates and shows them, but installs nothing.
// signrelease prints the public half it signs with: it must equal this.
const releasePublicKey = "vIy1s0pFZS2mlRh66sstSGCqcIKIg6ZT2sLc5LhRCow="
