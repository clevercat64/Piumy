// Package whatsmeow: the ONLY package in this repo that imports
// go.mau.fi/whatsmeow (WhatsApp's multi-device protocol, pure Go — no
// external Node process, unlike open-wa/F3). Implements gateway.Gateway
// (the F2 seam) — the core (corepipeline) never imports this package or
// go.mau.fi/whatsmeow directly, only the interface.
//
// Replaces open-wa as of the F5.x connector rewrite (ct-2026-07-10-0420).
// Validated end to end by a standalone smoke (ct-2026-07-10-0338, see
// docs/) before this package was written: connects, GetJoinedGroups lists
// real groups, receives real events.Message, compiles CGO_ENABLED=0
// static, and the device-name anti-ban fix (below) was confirmed against
// a real WhatsApp account.
//
// Anti-ban rule (CLAUDE.md, non-negotiable): this package NEVER sends on
// its own initiative, never auto-replies, never paces/throttles itself —
// all of that (rate limits, human-like delays) lives in the core
// (governor + corepipeline). This adapter only executes what the core
// explicitly asks for (Send/SetTyping/MarkRead), nothing more, nothing
// proactive.
package whatsmeow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	wmeow "go.mau.fi/whatsmeow"
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	wmstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "modernc.org/sqlite" // pure-Go driver, registers itself as "sqlite" — CGO_ENABLED=0

	"piumy-gateway/internal/eventbus"
	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// defaultDeviceName is what shows up in WhatsApp's own "Linked Devices"
// list. Anti-ban (boss catch, 2026-07-10, confirmed against a real
// account): whatsmeow's own library default (store.DeviceProps.Os =
// "whatsmeow") outs the client as unofficial on sight — a real-looking
// name is the fix. Paired with PlatformType=DESKTOP below (ct-2026-07-24,
// WhatsApp's own FAQ: "WhatsApp Desktop syncs more message history than
// WhatsApp Web" — declaring Chrome while asking for Desktop-depth history
// was the contradiction that motivated this). Configurable
// (Config.DeviceName), never clavado beyond this fallback.
const defaultDeviceName = "Piumy (Desktop)"

// inboundBuffer bounds how many undelivered inbound messages queue before
// the event handler starts dropping — same reasoning as openwa.Adapter's
// own inboundBuffer: the handler must never block on a slow/stuck
// pipeline, and a pipeline that falls behind re-syncs on its own via
// store.PendingChats regardless.
const inboundBuffer = 100

// Config configures an Adapter, sourced from env (config.go) — zero
// hardcode.
type Config struct {
	// DBPath is whatsmeow's OWN session store SQLite file — the device
	// identity, encryption keys, and pairing state. Deliberately separate
	// from piumy-gateway's own store.db (chats/messages/rules/etc, F1a) —
	// two very different lifecycles (this one holds WhatsApp's own crypto
	// material, wiping it means re-pairing with a fresh QR).
	DBPath string
	// DeviceName is store.DeviceProps.Os — see defaultDeviceName's doc.
	// Empty falls back to defaultDeviceName.
	DeviceName string

	// Store/Router: piumy-gateway's OWN store + router (not whatsmeow's
	// session store, cfg.DBPath above) — nil-safe, same "optional
	// dependency" convention as openwa.Adapter's own Store/MediaDir. Used
	// to seed joined groups into the chats table right after connecting
	// (handleConnected) and to persist downloaded media (handleMessage).
	// Without them, handleConnected still logs GetJoinedGroups's result
	// (just doesn't persist it), and media is still detected but not saved.
	Store  *store.Store
	Router *router.Manager

	// MediaDir is where downloaded inbound media files are saved (original
	// + low-q JPEG for images). Empty string disables media download —
	// messages with media still flow through (Type=mime, Text=caption) but
	// no file is written and no store.Media row is created.
	MediaDir string
	// LowQJPEGQuality (1-100) is the JPEG quality for the low-res copy
	// saved alongside originals for image media (F4-DESIGN §6). Zero/
	// negative falls back to 60.
	LowQJPEGQuality int

	// Bus/State/Governor: wired for the "silent death" hardening (H6,
	// ct-2026-07-10-0540) — LoggedOut/TemporaryBan/StreamReplaced/
	// ClientOutdated/Disconnected all used to vanish into a raw library log
	// line with no signal anywhere else (handleEvent only covered
	// Connected/Message). All three nil-safe, same optional-dependency
	// convention as Store/Router above: unset just skips that half of
	// handleDisconnect.
	Bus      *eventbus.Bus
	State    *state.Manager
	Governor *governor.Limiter

	// ActionDelayMin/Max bound the anti-ban pacing delay for the contact
	// backfill (ct-2026-07-19-0115, backup Sub 2a — Piumy gateway.go's
	// actionDelay, cherry-picked) — slept once per contact BEFORE each store
	// write, same governor.DelayWindow mechanism corepipeline already uses
	// for dispatch/read pacing. config.Load() (PIUMY_DELAY_ACTION_MIN/MAX)
	// is where the real 1s/4s default lives — this field just carries it
	// through, same as every other Config field here.
	ActionDelayMin time.Duration
	ActionDelayMax time.Duration

	// AvatarRecheckMin/Max bound the randomized window (days-scale, unlike
	// every other *DelayMin/Max above) before a cached avatar is worth
	// asking WhatsApp about again — see avatarRecheckWindow's (avatar.go)
	// own doc. config.Load() (PIUMY_AVATAR_RECHECK_MIN/MAX) is where the
	// real default lives — this field just carries it through, same as
	// ActionDelayMin/Max above.
	AvatarRecheckMin time.Duration
	AvatarRecheckMax time.Duration

	// ReconnectBaseDelay/MaxDelay/StableAfter: see reconnect.go's package
	// doc (T99, ct-2026-08-29-1607) for the whole design. config.Load()
	// (PIUMY_RECONNECT_BASE_DELAY/MAX_DELAY/STABLE_AFTER) carries the real
	// defaults through, same convention as every other Config field here.
	ReconnectBaseDelay   time.Duration
	ReconnectMaxDelay    time.Duration
	ReconnectStableAfter time.Duration
}

// Adapter implements gateway.Gateway against whatsmeow. The zero value is
// not usable — construct with New.
type Adapter struct {
	// clientPtr holds the live whatsmeow client. A POINTER that can be
	// swapped, not a fixed field (T187, ct-2026-11-11-0953): when WhatsApp
	// logs the account out from another device, whatsmeow DELETES the
	// device from the store (connectionevents.go's Store.Delete on
	// device_removed/401) — it sets Deleted=true, nils the ID and replaces
	// every sub-store with NoopStore{ErrDeviceDeleted}. That client can
	// never connect again (client.go's unlockedConnect returns
	// ErrDeviceDeleted before touching the network), so re-pairing needs a
	// NEW client over a NEW device, swapped in while the background loops
	// (syncLoop, avatarWorkerLoop, mediaBgWorkerLoop) are still reading
	// it. atomic.Pointer makes that swap safe without putting a lock on
	// every read site — the same reason corepipeline's bus uses one. Read it
	// ONLY through a.cli(); the field is named clientPtr so the compiler
	// catches any site that still expects the old fixed field.
	clientPtr atomic.Pointer[wmeow.Client]
	container *sqlstore.Container

	inbound chan gateway.Inbound
	// qrOut is a single, long-lived channel created in New, written by
	// pairLoop and Reconnect (guarded by pairingActive to ensure exactly
	// one writer), and NEVER closed — the reader in main.go ranges over
	// it indefinitely. See Fix 2 (ct-2026-07-23-0047) for the full
	// rationale: no swap, no close, no race.
	qrOut chan string

	// store/router: see Config.Store/Router's doc — nil-safe.
	store  *store.Store
	router *router.Manager

	// bus/state/governor: see Config.Bus/State/Governor's doc — nil-safe.
	bus      *eventbus.Bus
	state    *state.Manager
	governor *governor.Limiter

	// mediaDir/lowQuality: see Config.MediaDir/LowQJPEGQuality's doc.
	mediaDir   string
	lowQuality int

	// mediaInFlight coordinates the two consumers of media_pending
	// (ct-2026-07-21-1437 parte 2's background worker + parte 3's on-demand
	// parallel fetch) so they never download the SAME row at the same
	// time — claimMediaDownload/releaseMediaDownload (media.go). Keyed by
	// chatJID+"\x00"+msgID; in-memory only, reset on restart is fine (a
	// download in flight at process death is never "claimed" once the
	// process comes back).
	mediaInFlight sync.Map

	// actionDelayMin/Max: see Config.ActionDelayMin/Max's doc — read by
	// actionDelay() (sync.go), with a live KV-override checked first.
	actionDelayMin time.Duration
	actionDelayMax time.Duration

	// avatarRecheckMin/Max: see Config.AvatarRecheckMin/Max's doc — read by
	// avatarRecheckWindow() (avatar.go), with a live KV-override checked
	// first, same convention as actionDelayMin/Max above.
	avatarRecheckMin time.Duration
	avatarRecheckMax time.Duration

	// avatarQueue/avatarQueued back RequestAvatar's on-demand paced fetch
	// (avatar.go) — a bounded channel (never blocks the REST request path:
	// a full queue just drops the hint, the next time that chat is viewed
	// re-requests it) plus a de-dupe set so the SAME jid queued twice
	// (e.g. the header AND a list row both viewed in the same page load)
	// only occupies one queue slot. In-memory only, same "reset on
	// restart is fine" reasoning as mediaInFlight above — nothing here is
	// a correctness guarantee, only a cache-warming hint.
	avatarQueue  chan string
	avatarQueued sync.Map

	// runMu guards runCtx: the context passed to Start(), stashed so event
	// handlers that fire after a successful connect (the one-shot contact
	// backfill) can spawn cancellable background work without threading ctx
	// through whatsmeow's own event-callback signature (Piumy gateway.go's
	// runCtx/context(), cherry-picked verbatim — same problem, same fix).
	runMu  sync.Mutex
	runCtx context.Context

	// pairedAtMu guards pairedAt: the moment a FRESH pairing (never a
	// routine reconnect) last completed — stamped once by pairLoop, read by
	// freshPairingSyncWindow() (history.go, M2, ct-2026-07-22-2342) for the
	// dashboard's "still syncing" signal while WhatsApp's passive full-sync
	// push may still be flowing. Zero value on every ordinary reconnect
	// (pairLoop never runs then) — see freshPairingSyncWindow's doc.
	pairedAtMu sync.Mutex
	pairedAt   time.Time

	// historySyncMu guards historySyncStats: the live "is the passive
	// full-sync push flowing right now" signal — items 1+2 of
	// ct-2026-07-23-0047 (visibility + the window's auto-extend). Fed by
	// recordPassiveHistoryActivity (history.go), read by
	// freshPairingSyncWindow/HistorySyncStatus (history.go). Zero value on
	// every process start — deliberately NOT persisted: this is "flowing
	// right now" for the CURRENT sync window, not a historical record.
	historySyncMu    sync.Mutex
	historySyncStats historySyncStats

	// contactsSyncMu guards contactsSyncTimer — the debounced re-trigger for
	// syncContacts (ct-2026-07-31, "no llegan contactos en una instalación
	// nueva"). See scheduleContactsSync's doc (sync.go) for why this exists:
	// the one-shot call at connect races ahead of whatsmeow's own async
	// contact data, and this reacts to the signal that it actually landed.
	// contactsSyncDebounce: zero = contactsSyncDebounceDefault (10s); a
	// test-only override seam, never KV-backed (see its doc, sync.go).
	contactsSyncMu       sync.Mutex
	contactsSyncTimer    *time.Timer
	contactsSyncDebounce time.Duration

	// pairingActiveMu guards pairingActive: ensures exactly ONE pairLoop
	// goroutine writes to qrOut at any time (the one from Start OR the
	// one from Reconnect, never both). Set true at pairLoop entry, false
	// in its defer. Prevented Carrera B (double-loop + send on closed
	// channel) in Fix 2's original swap-based design.
	pairingActiveMu sync.Mutex
	pairingActive   bool

	// qrMu guards qr: the live QR-pairing state for the current round
	// (P2, ct-2026-07-24-0015). Fed by pairLoop on each QRChannelItem,
	// read by HistorySyncStatus-style projection for GET /api/status.
	qrMu sync.Mutex
	qr   qrState

	// reconnectBaseDelay/MaxDelay/StableAfter: see Config's doc — defaulted
	// defensively in New() (same "safe even without config.Load()" pattern
	// as lowQuality above), so reconnect.go's math never sees a zero value.
	reconnectBaseDelay   time.Duration
	reconnectMaxDelay    time.Duration
	reconnectStableAfter time.Duration

	// connectFn connects the LIVE client, indirected so reconnect_test.go
	// can drive the reconnect breaker's fail-open property (T99) without a
	// live WhatsApp session — same "inject the external dependency" seam
	// as agentTracker.pingAgent (mcpserver, T97). New wires it as a closure
	// over a.cli() rather than capturing one client's Connect method: T187
	// swaps the client, and a captured method would keep dialing the
	// retired device forever.
	connectFn func() error

	// reconnectMu guards the WhatsApp reconnect breaker's own state (T99,
	// ct-2026-08-29-1607) — see reconnect.go's package doc for the full
	// design. reconnectGen closes the race between a stale stable-timer
	// firing and a fresh disconnect landing at the same instant (same
	// generation-counter idiom as state.Manager.reactGen) — only the
	// timer that armed the CURRENT generation is allowed to reset
	// reconnectFailures.
	reconnectMu          sync.Mutex
	reconnectFailures    int
	reconnectGen         uint64
	reconnectStableTimer *time.Timer

	stopOnce sync.Once
}

// qrState is the live QR-pairing state for the current round. Zero value =
// no round active. P2 (ct-2026-07-24-0015): track current code, its
// per-code expiry (item.Timeout from whatsmeow's qrchan.go), and whether
// the round ended without pairing.
type qrState struct {
	Code      string
	ExpiresAt time.Time
	Expired   bool
	// ErrCode is why this round produced no QR, "" when there is nothing
	// wrong (T187, ct-2026-11-11-0953). A STABLE CODE, never the
	// library's error text: that text is English and changes between
	// whatsmeow versions, so the dashboard's catalog translates the code
	// instead. The raw error still goes to the log, which is where a
	// failure gets diagnosed. Contract with the dashboard (T188): this
	// surfaces as qr_error in GET /api/status.
	ErrCode string
}

// qrErrUnavailable is the ONLY ErrCode value — the pairing round could not
// be opened at all (GetQRChannel or Connect failed). This is the bug the
// boss hit: before T187 the modal said "Generando QR…" forever.
//
// The design review also proposed a session_reset_failed code for "the
// device was deleted and a fresh one could not be built". It does not
// exist because it CANNOT happen: container.NewDevice (sqlstore/
// container.go:212) only generates keys in memory and returns no error —
// the session DB is not touched until the pairing itself persists the
// device, and a failure there surfaces as a failed Connect, i.e. this
// same code. A second code with no producer would only make the dashboard
// translate a string nobody can ever see.
const qrErrUnavailable = "qr_unavailable"

// historySyncStats is Adapter.historySyncStats' value type — see its doc.
type historySyncStats struct {
	Messages    int
	ChatsSeen   map[string]struct{} // set, not a counter — a chat appearing in 2 chunks isn't 2 chats
	LastChunkAt time.Time
}

// context returns the context passed to the current/last Start() call, or
// context.Background() if Start() hasn't run yet.
func (a *Adapter) context() context.Context {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if a.runCtx != nil {
		return a.runCtx
	}
	return context.Background()
}

// cli returns the live whatsmeow client — the ONLY way to read it. See
// Adapter.clientPtr's doc for why it can change under a caller's feet.
// A caller that makes several calls in a row should hold the result in a
// local so it works against ONE client, not two halves of a swap.
func (a *Adapter) cli() *wmeow.Client {
	return a.clientPtr.Load()
}

// setClient swaps in the live client. Called by New and by
// resetDeletedDevice (T187); tests use it to wire a hand-built Adapter.
func (a *Adapter) setClient(c *wmeow.Client) {
	a.clientPtr.Store(c)
}

// newWAClient builds a whatsmeow client with THIS adapter's full
// configuration — the single place it lives (T187, ct-2026-11-11-0953).
// Both New and resetDeletedDevice go through here, so a re-paired session
// can never silently lose a flag the first one had: that divergence is
// exactly the bug class this function exists to prevent.
//
// Not included here on purpose: wmstore.DeviceProps (Os, PlatformType,
// RequireFullSync, HistorySyncConfig) is a PACKAGE-level var, not
// per-client — New sets it once before any pairing, so a client built
// later inherits it already. See New's own comments for that constraint.
func newWAClient(deviceStore *wmstore.Device, logger waLog.Logger, handler func(any)) *wmeow.Client {
	client := wmeow.NewClient(deviceStore, logger)
	// Anti-"1 tick" fix (ct-2026-07-15, boss report): whatsmeow only sends
	// delivery receipts with type="inactive" (transmitted, but never
	// rendered as the second gray tick) unless the client is marked
	// online — see go.mau.fi/whatsmeow's receipt.go doc on
	// SetForceActiveDeliveryReceipts. This flips receipts to the normal
	// type WITHOUT broadcasting an "online" presence to contacts
	// (SendPresence would do that too, but this account should not look
	// always-online — anti-ban, same spirit as governor's human pacing).
	client.SetForceActiveDeliveryReceipts(true)
	// T99 (ct-2026-08-29-1607): whatsmeow's own auto-reconnect resets its
	// failure counter on every successful HANDSHAKE
	// (connectionevents.go's handleConnectSuccess), even one that drops
	// seconds later — a connect-fall-connect-fall cycle retries with ~0
	// delay forever (the pattern that cost OpenClaw a 72h account
	// restriction). Turned off here; reconnect.go's scheduleReconnect
	// takes over with its OWN counter, which only resets after a
	// connection survives reconnectStableAfter — see that file's package
	// doc for the whole design. Verified before this change: LoggedOut/
	// StreamReplaced/ClientOutdated/device_removed already call
	// whatsmeow's own expectDisconnect() and never depended on this flag
	// to stay terminal — turning it off doesn't touch them.
	client.EnableAutoReconnect = false
	client.AddEventHandler(handler)
	return client
}

// New builds an Adapter: opens whatsmeow's own session store (modernc.org/
// sqlite, CGO_ENABLED=0) and constructs the client. Does not connect —
// call Start().
func New(ctx context.Context, cfg Config) (*Adapter, error) {
	deviceName := cfg.DeviceName
	if deviceName == "" {
		deviceName = defaultDeviceName
	}
	// store.DeviceProps is a package-level var in go.mau.fi/whatsmeow/store
	// (not per-Client) — it must be set before the FIRST pairing (it's sent
	// as part of registration), which is why this happens here in New,
	// before any GetQRChannel/Connect call downstream.
	wmstore.DeviceProps.Os = proto.String(deviceName)
	// DESKTOP, not CHROME (ct-2026-07-24): WhatsApp's own FAQ says "WhatsApp
	// Desktop syncs more message history than WhatsApp Web" — declaring
	// Chrome (a Web client) while asking for Desktop-grade history depth
	// (FullSyncDaysLimit=365 below) was internally inconsistent. Only takes
	// effect on a FRESH pairing (same constraint as the full-sync flags
	// below) — must ship before the boss's next QR scan.
	wmstore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
	// M1 (ct-2026-07-22-2342): full-sync flags, ONLY effective on a FRESH
	// pairing (same "before the first pairing" reasoning as Os/PlatformType
	// above) — the boss's re-pareo needs these set BEFORE scanning the new
	// QR, so this deploy has to ship them ahead of that. Today's default
	// (RequireFullSync=false, store/clientpayload.go:156) is why the
	// original pairing (2026-07-10, 8 days before history.go existed —
	// ct-2026-07-22-2332's own finding) never got a real history push.
	// Downloading more history is NOT an anti-ban risk (confirmed via web
	// research before this contract) — only bulk OUTBOUND sending is.
	wmstore.DeviceProps.RequireFullSync = proto.Bool(true)
	// ponytail: 365 days is what WhatsApp Desktop itself requests (the
	// contract's own research) — a documented, real-world ceiling, not a
	// guess. The phone caps the actual payload regardless of what we ask
	// for, so asking higher wouldn't necessarily bring more; if a future
	// re-pareo turns out to need deeper history, this is the one line to
	// raise.
	wmstore.DeviceProps.HistorySyncConfig.FullSyncDaysLimit = proto.Uint32(365)
	// ponytail: matches StorageQuotaMb's own already-generous 10GB ceiling
	// (library default, untouched) rather than picking an unrelated number
	// — one consistent size budget for the whole sync, not two different
	// guesses.
	wmstore.DeviceProps.HistorySyncConfig.FullSyncSizeMbLimit = proto.Uint32(10240)

	logger := waLog.Stdout("whatsmeow", "WARN", true)

	// MkdirAll's own directory first (T169, ct-2026-09-19-1433): cfg.DBPath
	// now usually resolves under config.DataDir(), which may not exist yet
	// on a genuinely fresh install — sqlite never creates a missing parent,
	// only the file. Same pattern store.Open (internal/store/schema.go)
	// applies to its own DB now, and every other writer in this codebase
	// (mediautil, sessionbackup, state, agentconnect, gwlog) already uses
	// for its own directory.
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("whatsmeow: session db directory: %w", err)
	}
	// sqlstore.New's convenience constructor opens its own *sql.DB with no
	// way to reach it afterward — we open it ourselves instead, so
	// SetMaxOpenConns(1) below can actually be applied. modernc.org/sqlite's
	// pragma syntax differs from mattn/go-sqlite3's (?_foreign_keys=on):
	// it's ?_pragma=foreign_keys(1) — confirmed by reading modernc's own
	// source during the smoke (ct-2026-07-10-0338), not assumed.
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", cfg.DBPath))
	if err != nil {
		return nil, fmt.Errorf("whatsmeow: open session db: %w", err)
	}
	// The SQLITE_BUSY ("database is locked") errors seen in the smoke test
	// under real load (history sync + message decrypt hitting the store
	// concurrently right after connecting) are modernc.org/sqlite plus
	// database/sql's default multi-connection pool — SQLite itself has no
	// real concurrent-writer story. One connection serializes all access
	// and removes the whole error class; this store is low-throughput
	// (session/crypto state, not the message history) so serializing has
	// no real cost.
	db.SetMaxOpenConns(1)
	// Close this connection on EVERY failure path below (T193,
	// ct-2026-11-11-1039). A New that returns an error hands back no
	// Adapter, so nobody else can ever close it — and on Windows the
	// leaked handle keeps the session FILE locked, which is exactly what
	// would block restoring one of premigration.go's copies over it.
	// A defer instead of a Close() at each return, deliberately: this
	// started as three separate leaks (the upgrade and get-device paths
	// below had it since F5), so the pattern that only works when the next
	// person remembers is the pattern that already failed here.
	success := false
	defer func() {
		if !success {
			db.Close()
		}
	}()

	container := sqlstore.NewWithDB(db, "sqlite", logger)
	// T193 (ct-2026-11-11-1039): copy the session DB BEFORE the library
	// migrates it — see premigration.go for why that migration is the one
	// irreversible step of a self-update. A failure here deliberately
	// refuses to start: migrating without the copy is what costs the owner
	// their session, and "Piumy did not start" is the cheaper failure. The
	// mood is best-effort (this process is about to stop, so the log and
	// the returned error are the channels that actually carry the reason;
	// Speech is left to state.Manager, which derives it from the mood).
	if copied, err := backupBeforeMigration(ctx, db, cfg.DBPath, premigrationKeep(cfg.Store)); err != nil {
		log.Printf("whatsmeow: NO se migra el esquema de la sesión — %v", err)
		if cfg.State != nil {
			_ = cfg.State.UpdateMood(func(s *state.Status) { s.Mood = "error" })
		}
		return nil, err
	} else if copied != "" {
		log.Printf("whatsmeow: sesión respaldada antes de migrar el esquema → %s", copied)
	}
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("whatsmeow: upgrade session store: %w", err)
	}
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("whatsmeow: get device: %w", err)
	}
	// T25 (ct-2026-08-05-1833): sin esto, un arranque con una sesión previa
	// exitosa NO deja ni una línea en el log hasta la primera actividad
	// real — indistinguible de "nunca se llamó" o "falló antes de poder
	// loguear" (el propio síntoma reportado). GetFirstDevice (biblioteca)
	// crea un dispositivo nuevo en memoria si no encuentra ninguno en
	// cfg.DBPath — sin guardarlo (NewDevice no persiste solo); Store.ID
	// queda nil hasta el pareo real. Loguear esto es lo mínimo para que la
	// PRÓXIMA corrida diga, sin ambigüedad, si encontró la sesión o no.
	if deviceStore.ID != nil {
		log.Printf("whatsmeow: sesión previa encontrada en %s (jid %s)", cfg.DBPath, deviceStore.ID)
	} else {
		log.Printf("whatsmeow: sin sesión previa en %s — instalación limpia o archivo sin dispositivo pareado", cfg.DBPath)
	}

	lowQ := cfg.LowQJPEGQuality
	if lowQ <= 0 {
		lowQ = 60
	}
	// reconnectBaseDelay/MaxDelay/StableAfter: defaults match
	// corepipeline/outbox.go's exponentialBackoff base (5s) — MaxDelay
	// deliberately does NOT match its 1h ceiling, see reconnect.go's
	// package doc for why this breaker needs a shorter one (it never gives
	// up, so the ceiling is a permanent worst-case wait, not "how long
	// before giving up").
	reconnectBase := cfg.ReconnectBaseDelay
	if reconnectBase <= 0 {
		reconnectBase = 5 * time.Second
	}
	reconnectMax := cfg.ReconnectMaxDelay
	if reconnectMax <= 0 || reconnectMax < reconnectBase {
		reconnectMax = 5 * time.Minute
	}
	reconnectStable := cfg.ReconnectStableAfter
	if reconnectStable <= 0 {
		reconnectStable = 60 * time.Second
	}
	a := &Adapter{
		container:            container,
		inbound:              make(chan gateway.Inbound, inboundBuffer),
		qrOut:                make(chan string, 4),
		store:                cfg.Store,
		router:               cfg.Router,
		bus:                  cfg.Bus,
		state:                cfg.State,
		governor:             cfg.Governor,
		mediaDir:             cfg.MediaDir,
		lowQuality:           lowQ,
		actionDelayMin:       cfg.ActionDelayMin,
		actionDelayMax:       cfg.ActionDelayMax,
		avatarRecheckMin:     cfg.AvatarRecheckMin,
		avatarRecheckMax:     cfg.AvatarRecheckMax,
		avatarQueue:          make(chan string, avatarQueueCap),
		reconnectBaseDelay:   reconnectBase,
		reconnectMaxDelay:    reconnectMax,
		reconnectStableAfter: reconnectStable,
	}
	// The client is built AFTER the Adapter exists: newWAClient needs
	// a.handleEvent, and a.cli() has to be live before any handler fires.
	a.setClient(newWAClient(deviceStore, logger, a.handleEvent))
	// connectFn resolves the client on EVERY call instead of capturing
	// today's one (T187): resetDeletedDevice swaps the client, and a
	// captured client.Connect would keep dialing the deleted device
	// forever. reconnect_test.go still overrides this same seam.
	a.connectFn = func() error { return a.cli().Connect() }
	// From here the Adapter owns the connection: Stop closes the container,
	// which closes this db. The defer above must not close it too.
	success = true
	return a, nil
}

// QRChannel returns the QR pairing codes to show for first-time login. See
// Adapter.qrOut's doc comment for why this is safe to call at any time.
func (a *Adapter) QRChannel(ctx context.Context) (<-chan string, error) {
	return a.qrOut, nil
}

// Start connects the client. If the store already has a device ID
// (paired in a previous run), this is synchronous and fast — Connect
// alone. If it doesn't (first-time login), Start returns IMMEDIATELY and
// drives the whole QR pairing loop in a background goroutine instead of
// blocking the caller.
//
// This matters beyond style: corepipeline.Controller.Start calls
// gw.Start(ctx) synchronously, and only AFTER it returns does it launch
// the HTTP servers and the pipeline loops (main.go). A blocking Start on
// a fresh/unpaired session (audit finding, ct-2026-07-10-0420 review)
// would hang the ENTIRE process at boot until someone scans — no MCP/REST,
// no pusher/autoreply/backup, and worse: ctx cancellation (Ctrl+C) would
// never be observed because main() itself would still be stuck inside
// ctrl.Start(), never reaching <-ctx.Done(). openwa.Adapter's own Start
// never blocked this way, which is why Controller was written assuming it.
func (a *Adapter) Start(ctx context.Context) error {
	a.runMu.Lock()
	a.runCtx = ctx
	a.runMu.Unlock()
	// syncLoop (ct-2026-07-19-0115, backup Sub 2a): the periodic contact
	// re-sweep — independent of pairing state below, same as Piumy's Start
	// launching syncLoop unconditionally alongside its other background loops.
	go a.syncLoop(ctx)
	// mediaBgWorkerLoop (ct-2026-07-21-1437 parte 2): the gradual background
	// media backfill — same "launch unconditionally alongside Start's other
	// background loops" convention as syncLoop above.
	go a.mediaBgWorkerLoop(ctx)
	// reconcileIdentitiesSweepLoop (S13, ct-2026-07-30-1835): a no-op while
	// store.SettingIdentityAutoReconcile is false (the default) — see its
	// own doc. Launched unconditionally, same convention as the loops above.
	go a.reconcileIdentitiesSweepLoop(ctx)
	// avatarWorkerLoop (T17 Parte 3, ct-2026-08-05-1240): drains
	// RequestAvatar's queue, one paced fetch at a time — idle (blocked on
	// the empty channel) until the dashboard actually asks for one. Same
	// "launch unconditionally" convention as the loops above; it does
	// nothing on its own.
	go a.avatarWorkerLoop(ctx)

	if a.cli().Store.ID != nil {
		// T25: mismo motivo que el log de New() — Connect() no logueaba
		// nada al ser llamado, ni al pedirlo ni al fallar sincrónicamente
		// (los eventos post-conexión sí loguean, ver handleEvent/
		// handleDisconnect — esto cubre el hueco ANTES de que exista un
		// evento).
		log.Printf("whatsmeow: conectando sesión existente (%s)...", a.cli().Store.ID)
		return a.connectOrScheduleRetry(ctx)
	}
	log.Println("whatsmeow: sin sesión previa — esperando click en \"Conectar QR\" (Reconnect)")
	// P2 (ct-2026-07-24-0015): sin sesion, NO arrancamos pairLoop al
	// inicio — esperamos al click de "Conectar QR" (Reconnect). Asi
	// evitamos rondas QR desperdiciadas y el spam que el boss reporto.
	return nil
}

// pairLoop drives ONE QR pairing round (P2, ct-2026-07-24-0015): request
// a code round, publish each code onto qrOut tracking its expiry, and
// when the round ends without pairing (QRChannelTimeout) mark Expired and
// return — no auto-retry. The caller (Reconnect) decides when to try
// again. Runs until paired or ctx is cancelled (Controller.Stop cancels
// this same ctx before calling gw.Stop() — see corepipeline.Controller).
func (a *Adapter) pairLoop(ctx context.Context) {
	a.pairingActiveMu.Lock()
	a.pairingActive = true
	a.pairingActiveMu.Unlock()
	defer func() {
		a.pairingActiveMu.Lock()
		a.pairingActive = false
		a.pairingActiveMu.Unlock()
	}()
	if a.cli().Store.ID != nil || ctx.Err() != nil {
		return
	}
	// T187 (ct-2026-11-11-0953): repair the device BEFORE asking for a QR.
	// A device WhatsApp deleted still passes the ID==nil guard above (its
	// own Delete nils the ID), and GetQRChannel hands back a channel for
	// it anyway — qrchan.go never looks at Deleted. The failure landed
	// only on Connect, which is why this looked like "Generando QR…"
	// forever until Piumy was restarted.
	a.resetDeletedDevice()
	qrChan, err := a.cli().GetQRChannel(ctx)
	if err != nil {
		a.setQRError(qrErrUnavailable, fmt.Errorf("get QR channel: %w", err))
		return
	}
	// No ErrDeviceDeleted retry here, and that is measured, not an
	// oversight (T187 — the design review asked for one). For Connect to
	// hit a deleted device despite the repair above, the logout would have
	// to land between the two. It cannot: whatsmeow only deletes the
	// device when the SERVER sends a 401/device_removed, which needs a
	// live connection of a PAIRED device — and inside pairLoop the device
	// is unpaired by definition. Were it paired, GetQRChannel would
	// already have returned ErrQRStoreContainsID above and this line
	// would never run. If one ever did slip through, it is no longer
	// silent: the error below reaches the dashboard, and the owner's
	// retry opens a fresh round that the repair above handles.
	if err := a.connectFn(); err != nil {
		a.setQRError(qrErrUnavailable, fmt.Errorf("connect: %w", err))
		return
	}
	for item := range qrChan {
		if item.Event != "code" {
			continue
		}
		a.qrMu.Lock()
		a.qr.Code = item.Code
		a.qr.ExpiresAt = time.Now().Add(item.Timeout)
		a.qr.Expired = false
		a.qrMu.Unlock()
		select {
		case a.qrOut <- item.Code:
		case <-ctx.Done():
			return
		}
	}
	// Round ended without pairing (QRChannelTimeout). Mark expired so the
	// frontend shows "QR expirado" + Refresh button instead of a stale code.
	if a.cli().Store.ID == nil {
		log.Println("whatsmeow: QR round ended without pairing — marked expired")
		a.cli().Disconnect()
		a.qrMu.Lock()
		a.qr.Expired = true
		a.qrMu.Unlock()
		return
	}
	// A FRESH pairing just completed. M2 (ct-2026-07-22-2342): stamp
	// pairedAt so the dashboard's "still syncing" signal
	// (HistorySyncStatus, see freshPairingSyncWindow) knows a passive
	// full-sync push may be in flight.
	a.pairedAtMu.Lock()
	a.pairedAt = time.Now()
	a.pairedAtMu.Unlock()
}

// Logout unlinks the current WhatsApp session (M3, ct-2026-07-22-2342, the
// dashboard's "Desconectar" button): sends the unlink request, disconnects,
// and deletes the local device store — client.Store.ID becomes nil, so the
// NEXT pairing (pairLoop, next process start) is a FRESH QR, which is the
// only way M1's full-sync flags (RequireFullSync et al — set once, before
// pairing) actually apply. Deliberately NOT Disconnect(): that only drops
// the socket, the session survives, and a restart just resumes it — no
// fresh pairing, M1's flags never take effect. client.Logout is itself
// nil-safe (whatsmeow's own convention, mirrors Disconnect), so this needs
// no extra guard.
func (a *Adapter) Logout(ctx context.Context) error {
	if err := a.cli().Logout(ctx); err != nil {
		return err
	}
	// Fix 1 salvedad (ct-2026-07-23-0047fixes-ux-del-boton-desconectar, el
	// boss probó el flujo): a CODE-initiated Logout emits NO whatsmeow event
	// at all — both Logout and the Disconnect() it calls internally say so
	// in their own doc comments — unlike an EXTERNAL logout (events.LoggedOut,
	// handleDisconnect in inbound.go). Without this, state.Manager would keep
	// reporting "connected" after a live Desconectar click, and GET /api/qr's
	// "ya conectado" message would stay wrong until a restart. Update (not
	// UpdateMood): WAConnected is a plain status field here, not the face.
	if a.state != nil {
		_ = a.state.Update(func(s *state.Status) { s.WAConnected = false })
	}
	// Fix 2 (ct-2026-07-23-0047): reset the passive-sync progress signal so
	// a subsequent Reconnect doesn't inherit stale message/chat counts from
	// the old sync window. Logout always precedes a valid Reconnect, so
	// resetting here (not in Reconnect) is sufficient and simpler.
	a.historySyncMu.Lock()
	a.historySyncStats = historySyncStats{}
	a.historySyncMu.Unlock()
	return nil
}

// Reconnect kicks off a new pairing flow after a Logout — re-enters
// pairLoop with the same long-lived qrOut, so the existing reader in
// main.go receives fresh QR codes without reopening anything. Guards:
//   - Store.ID != nil → "already paired" (real state, not cosmetic)
//   - pairingActive → another pairLoop is already running (from Start or
//     a prior Reconnect), the QR is already being generated — nothing to do.
//
// Resets pairedAt so freshPairingSyncWindow works for the new pairing, and
// clears historySyncStats so GET /api/status doesn't report stale counts
// from the previous sync window.
func (a *Adapter) Reconnect(ctx context.Context) error {
	if a.cli().Store.ID != nil {
		return errors.New("already paired")
	}
	a.pairingActiveMu.Lock()
	if a.pairingActive {
		a.pairingActiveMu.Unlock()
		return nil
	}
	a.pairingActive = true
	a.pairingActiveMu.Unlock()

	a.pairedAtMu.Lock()
	a.pairedAt = time.Time{}
	a.pairedAtMu.Unlock()

	a.historySyncMu.Lock()
	a.historySyncStats = historySyncStats{}
	a.historySyncMu.Unlock()

	// P2 (ct-2026-07-24-0015): reset QR state so the frontend doesn't
	// inherit stale code/expiry from a prior expired round.
	a.qrMu.Lock()
	a.qr = qrState{}
	a.qrMu.Unlock()

	go a.pairLoop(a.context())
	return nil
}

// QRState returns the live QR-pairing state for GET /api/status (P2,
// ct-2026-07-24-0015): current code, its expiry, whether the last round
// ended without pairing, and why no QR came out (errCode, T187 — see
// qrState.ErrCode).
//
// All four come from ONE read under the same mutex, deliberately: a
// separate QRError() accessor could be served a different round than the
// code it is reported next to.
func (a *Adapter) QRState() (code string, expiresAt time.Time, expired bool, errCode string) {
	a.qrMu.Lock()
	defer a.qrMu.Unlock()
	return a.qr.Code, a.qr.ExpiresAt, a.qr.Expired, a.qr.ErrCode
}

// setQRError records why the current pairing round produced no QR, so the
// dashboard can say it instead of spinning forever (T187). The raw error
// goes to the log; only the stable code reaches the wire.
func (a *Adapter) setQRError(code string, err error) {
	if err != nil {
		log.Printf("whatsmeow: QR unavailable (%s): %v", code, err)
	}
	a.qrMu.Lock()
	a.qr.ErrCode = code
	a.qrMu.Unlock()
}

// resetDeletedDevice repairs the one state a re-pairing cannot survive
// (T187, ct-2026-11-11-0953): WhatsApp logged the account out from another
// device, so whatsmeow DELETED this device from the store — Deleted=true,
// ID=nil, every sub-store replaced by NoopStore{ErrDeviceDeleted}
// (store.Device.Delete). Such a client returns ErrDeviceDeleted from
// Connect before it even reaches the network, which is why "Conectar QR"
// used to hang until Piumy was restarted: a restart called GetFirstDevice,
// which builds a fresh device, and that is precisely what this does
// without one.
//
// A no-op when the device is healthy, so callers can call it
// unconditionally before pairing. The old client is retired — handlers
// removed first so its in-flight QRChannel handler can't push events into
// an adapter that has already moved on, then disconnected.
//
// Returns nothing: container.NewDevice only generates keys in memory and
// cannot fail (see qrErrUnavailable's doc for the code that therefore
// does not exist). Reports whether it actually swapped the client, which
// is what the test asserts on.
func (a *Adapter) resetDeletedDevice() bool {
	old := a.cli()
	if old == nil || !old.Store.Deleted {
		return false
	}
	log.Println("whatsmeow: el dispositivo fue borrado por WhatsApp — creando uno nuevo para el pareo")
	// Same constructor as boot, on purpose: see newWAClient's doc.
	fresh := newWAClient(a.container.NewDevice(), waLog.Stdout("whatsmeow", "WARN", true), a.handleEvent)
	a.setClient(fresh)
	old.RemoveEventHandlers()
	old.Disconnect()
	return true
}

// Stop disconnects, closes whatsmeow's own session store (releases the
// SQLite file — otherwise it stays open, and locked, until process exit),
// and closes Inbound() — idempotent, matching gateway.Gateway's contract.
func (a *Adapter) Stop() {
	a.cli().Disconnect()
	a.stopOnce.Do(func() {
		if err := a.container.Close(); err != nil {
			log.Printf("whatsmeow: close session store: %v", err)
		}
		close(a.inbound)
	})
}

// Connected delegates to the client's own tracked state — no reason to
// duplicate it in a second bool that could drift.
func (a *Adapter) Connected() bool {
	return a.cli().IsConnected()
}

// Paired reports whether a WhatsApp account is linked to this session store
// (Store.ID set) — NOT whether the socket is up right now, which Connected
// answers. Read at boot (S4, ct-2026-09-23-1908) to tell a brand-new account
// that still needs its QR apart from a linked one that is merely reconnecting.
func (a *Adapter) Paired() bool {
	return a.cli().Store.ID != nil
}

// Inbound streams messages the event handler maps from whatsmeow events.
func (a *Adapter) Inbound() <-chan gateway.Inbound {
	return a.inbound
}

// killSwitchActive mirrors corepipeline.Pipeline.killSwitchActive
// (outbox.go) — same two-flag check (H2+H3: set_kill_switch flips both
// together, but they stay two independent fields). Nil-safe here since
// Governor/State are OPTIONAL Adapter deps (Config's doc), unlike
// corepipeline where both are guaranteed non-nil. Shared by both
// background workers that must stop sending while killed/muted —
// mediaworker.go and mediabgworker.go.
func (a *Adapter) killSwitchActive() bool {
	if a.governor != nil && a.governor.Killed() {
		return true
	}
	if a.state != nil && a.state.Snapshot().Muted {
		return true
	}
	return false
}
