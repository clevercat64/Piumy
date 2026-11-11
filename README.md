# piumy-gateway

The engine behind [Piumy](https://piumy.app): your WhatsApp, answered by your own AI agents over MCP — routed, stored, and under your rules.

Everything runs on your machine. There are no servers in between.

## Getting it

You do not need this repository to use Piumy. Download the file for your system from **[the latest release](https://github.com/clevercat64/Piumy/releases/latest)**:

| System | File |
|---|---|
| Windows | `Piumy-Setup-<version>.exe` — one double click |
| Linux (PC, Raspberry Pi) | `Piumy-<version>-linux-amd64.tar.gz` (PC), `-linux-arm64.tar.gz` (64-bit Raspberry Pi OS), `-linux-armv7.tar.gz` (32-bit Raspberry Pi OS) |
| macOS | `Piumy-<version>-darwin-arm64.tar.gz` (Apple silicon), `-darwin-amd64.tar.gz` (Intel) |

This repository is the source: for reading it, auditing it, or building it yourself.

### Linux and macOS

> **Tested on a real Linux for updating only; not on a Mac at all.** The update path — `install.sh` over an existing install, with no terminal — was run on a real Linux (Ubuntu 24.04, systemd 255, as a normal user): it replaced the program, restarted the user service and kept the keys. A stand-in service replaced the real Piumy binary in that test, so no WhatsApp account was touched. Everything else is only checked against simulated `systemctl` and `launchctl` (`installer/posix/install_test.sh`); what a real machine has not confirmed yet is listed at the end of this section. If something fails, please say so.

```sh
tar xzf Piumy-<version>-<system>-<arch>.tar.gz
cd Piumy-<version>-<system>-<arch>
sh install.sh
```

No administrator rights: run it as yourself, **not** with `sudo`. It does what the Windows installer does:

- Puts the program and its data in one folder — `~/.local/share/piumy` on Linux, `~/Library/Application Support/Piumy` on macOS. That is where Piumy itself looks for them.
- On a first install, generates the keys (`piumy-config.json`, next to the program) and asks for the dashboard password. For an unattended install, set `PIUMY_DASHBOARD_PASSWORD` instead; with neither, the dashboard keeps the factory password `admin` / `piumy` and says so until you change it. The installer never writes the password to disk (Piumy keeps only a hash of it).
- Starts Piumy when you log in: a systemd user service named `piumy` on Linux, a launch agent named `app.piumy.gateway` on macOS.
- Prints the dashboard address, `http://127.0.0.1:8092/dashboard/`. Open it and press **Connect QR** to link your WhatsApp.

**To update**, extract the new package and run `sh install.sh` again. It replaces the program and nothing else: the keys, the history, the rules and the WhatsApp session are never touched, and if it cannot read the existing keys it stops before changing anything.

**Day to day**

| | Linux | macOS |
|---|---|---|
| Status | `systemctl --user status piumy` | `launchctl print gui/$(id -u)/app.piumy.gateway` |
| Restart | `systemctl --user restart piumy` | `launchctl kickstart -k gui/$(id -u)/app.piumy.gateway` |
| Stop | `systemctl --user stop piumy` | `launchctl bootout gui/$(id -u)/app.piumy.gateway` |
| Service log | `journalctl --user -u piumy` | `~/Library/Application Support/Piumy/logs/launchd.log` |

Piumy's own log is `logs/piumy.log` inside the data folder, on both systems.

**To uninstall** — this keeps your data:

```sh
# Linux
systemctl --user disable --now piumy
rm ~/.config/systemd/user/piumy.service ~/.local/share/piumy/piumy-gateway

# macOS
launchctl bootout gui/$(id -u)/app.piumy.gateway
rm ~/Library/LaunchAgents/app.piumy.gateway.plist "$HOME/Library/Application Support/Piumy/piumy-gateway"
```

The data folder holds the message history, the rules and the WhatsApp session. Deleting it cannot be undone, so the uninstall above leaves it; remove it by hand only if you really want everything gone.

Good to know:

- There is no tray icon or window outside Windows — the dashboard is the interface.
- Outside Windows nothing stops a second copy of Piumy from starting. If you ever ran it by hand, close it before updating, and never run two against the same data folder.
- On a server or a Raspberry Pi without a login session, the service only runs while you are logged in. To start it at boot: `loginctl enable-linger $USER`.
- By default Piumy listens on all network interfaces (agents on `8091`, dashboard on `8092`). Both ask for a key or password; to keep the dashboard to this machine only, add `"PIUMY_REST_ADDR": "127.0.0.1:8092"` to `piumy-config.json` and restart.
- Not confirmed on a real machine: a **fresh** install (its interactive prompts need a terminal), the real Piumy binary under the service, `shellcheck`, and everything on macOS — that launchd accepts the service file, the macOS commands in the table above, and the quarantine attribute. On Linux, systemd did accept the service file this installer writes.

## What it is

A single Go binary that sits between WhatsApp and your agents:

- **WhatsApp** — [whatsmeow](https://github.com/tulir/whatsmeow) embedded in-process. No browser, no Node, no external client. `CGO_ENABLED=0`.
- **Agents** — an MCP server they connect to, plus a dispatch channel that pushes incoming messages to them.
- **Storage** — SQLite (pure Go), holding history, per-chat rules, memory and the outbox.
- **A dashboard** on localhost to see and steer all of it.

An agent cannot act on its own: every action must belong to a real incoming message it is currently handling. That gate lives in the code, not in a prompt.

## Building

```bash
go build ./...
go test ./...
```

`./build-all.sh` builds every platform from one machine (all `CGO_ENABLED=0`) into `dist/`: one `Piumy-<version>-<system>-<arch>.tar.gz` per Linux and macOS target — the program plus `installer/posix/install.sh` — and, on Windows, `Piumy-Setup-<version>.exe` built from `installer/windows/` with Inno Setup.

## Layout

| Path | What |
|---|---|
| `internal/whatsmeow` | the WhatsApp client adapter |
| `internal/corepipeline` | what happens to a message from arrival to answer |
| `internal/mcpserver` | the tools agents call, and the gate that limits them |
| `internal/capipush` | dispatching messages out to agents |
| `internal/store` | SQLite: chats, messages, rules, memory, drafts |
| `internal/dashboard`, `internal/restapi` | the local dashboard and its API |
| `docs/MANUAL.md` | the map: every package and its exported surface |

Agent-facing manuals live in `internal/mcpserver/manuals/` and are embedded in the binary — an agent reads them over MCP with `get_manual`, so they cannot drift from the build.

## Contributing

Two rules carry more weight than the rest:

- **No personal data in the repository — not even test data.** No phone numbers, WhatsApp identifiers, emails or real names; no chat, contact or history dumps. When a test needs an identifier, use the `555` prefix (`555000001@s.whatsapp.net`). A pre-commit hook enforces this: enable it with `git config core.hooksPath .githooks`.
- **The hard limits belong in the code**, never in a prompt or a manual. A rule an agent can be talked out of is not a rule.

## License

GNU Affero General Public License v3.0 — see [LICENSE](LICENSE).
