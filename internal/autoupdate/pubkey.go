package autoupdate

// releasePublicKey is the ed25519 public key (base64) Piumy verifies every
// release's SHA256SUMS.sig with. The matching private key lives only in the
// maintainers' vault and is used by cmd/signrelease (PIUMY_RELEASE_SIGNING_KEY).
//
// EMPTY until the key pair is generated (`go run ./cmd/signrelease keygen`):
// with no key Piumy still looks for updates and shows them, but can verify
// nothing, so it installs nothing — the safe way to be unconfigured.
// signrelease prints the public half it signs with: it must equal this.
const releasePublicKey = ""
