// DB-admin tools (F4-DESIGN §3/§5): rules, is_boss/is_approver, confirmation
// and config levels, drafts, the kill switch and the principal's connector.
// All call store methods already migrated in F1a; the same methods are also
// exposed via REST (restapi) for the owner administering from the dashboard.
//
// T178 (ct-2026-11-10-2330-t178): none of these refuses by who is asking or by
// what dispatch is alive any more — the boss's call, verbatim (2026-08-10):
// "piumy es permisivo, los agentes ias son responsabilidad del ususario, llave
// maestra y cosas así está bien por si un agente es manipulado". What that
// risk means for an owner lives in the manager manual, not in code. The ONE
// brake left is set_kill_switch (levelgate.go's bossOnlyTools): the anti-ban
// stop the boss asked to keep.
package mcpserver

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/capiconn"
	"piumy-gateway/internal/store"
)

func addAdminTools(s *server.MCPServer, d Deps, tracker *agentTracker) {
	// ── set_chat_rules ───────────────────────────────────────────────────
	// T31 (ct-2026-08-06-0244): unconditional, no dispatch level, no chat
	// scoping, no principal check — the boss rejected two prior versions
	// with conditions, verbatim: "No pongas condiciones, que la skill
	// recomiende nada mas... ya es responsabilidad del usuario." The
	// architecture he wants (a separate agent for known vs. unknown
	// contacts) lives as a recommendation in the piumy-operator skill, not
	// as a code gate here.
	s.AddTool(mcp.NewTool("set_chat_rules",
		mcp.WithDescription("Set a chat's rules — the permission the agent operates under for that chat. Unconditional (T31, ct-2026-08-06-0244): any chat_id, any dispatch level, no code-level restriction — the boss's own call, verbatim: \"ya es responsabilidad del usuario\". See the piumy-operator skill for his architecture recommendation (a separate agent for chats you don't already trust) before reaching for this on one. Overwrites the whole field."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("rules", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			rules, err := r.RequireString("rules")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetChatRules(jid, rules); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("rules set"), nil
		})

	// ── set_is_boss ──────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_is_boss",
		mcp.WithDescription("Mark or unmark a chat as the owner (is_boss). A boss chat's dispatches arrive at boss level and it is exempt from the confirmation gate. Same write as POST /api/admin/is-boss."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithBoolean("is_boss", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			isBoss, err := r.RequireBool("is_boss")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetIsBoss(jid, isBoss); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("is_boss set to %v", isBoss)), nil
		})

	// ── set_type_rules ───────────────────────────────────────────────────
	// Group-only since T79 (ct-2026-08-27-2034) removed the "individual"
	// tier (dead since M5) and set_default_rules entirely — chat_type
	// dropped, there's nothing left to choose between.
	s.AddTool(mcp.NewTool("set_type_rules",
		mcp.WithDescription("Set the rules every group inherits unless it has its own (the by-type tier — set_chat_rules writes one chat's own). Overwrites the whole field. Same write as POST /api/admin/type-rules."),
		mcp.WithString("rules", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			rules, err := r.RequireString("rules")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetTypeRules(rules); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("type rules set"), nil
		})

	// ── set_confirmation_mode ────────────────────────────────────────────
	s.AddTool(mcp.NewTool("set_confirmation_mode",
		mcp.WithDescription("Set a chat's confirmation_mode: 'always' or 'discretion' (send_message holds a draft for confirmation first) or 'none' (send_message sends directly)."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("mode", mcp.Required(), mcp.Enum("none", "discretion", "always"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			mode, err := r.RequireString("mode")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetConfirmationMode(jid, mode); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("confirmation_mode set to " + mode), nil
		})

	// ── set_config_level ─────────────────────────────────────────────────
	// Translation layer (config_level unification) over the existing 4
	// fields — TRANSLATES the chosen level to SetIsBoss/SetActive/
	// SetConfirmationMode/SetStatus (store.SetConfigLevel), reusing those
	// setters verbatim. Does not replace set_is_boss/set_confirmation_mode/
	// set_chat_status/set_chat_active — they keep working standalone.
	s.AddTool(mcp.NewTool("set_config_level",
		mcp.WithDescription("Set a chat's unified config level: 'boss' (the owner's chat), 'auto' (agent replies on its own, no confirmation), 'confirm' (drafts wait for confirmation), 'unattended' or 'ignored'. Same write as POST /api/admin/config-level."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithString("level", mcp.Required(), mcp.Enum("boss", "auto", "confirm", "unattended", "ignored"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			level, err := r.RequireString("level")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetConfigLevel(jid, level); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("config_level set to " + level), nil
		})

	// ── approve_draft ────────────────────────────────────────────────────
	s.AddTool(mcp.NewTool("approve_draft",
		mcp.WithDescription("Approve a pending draft, moving it to the outbox to actually send. Optionally override its text first."),
		mcp.WithNumber("id", mcp.Required()),
		mcp.WithString("text_override", mcp.Description("Optional: replace the draft's text before sending"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			textOverride := r.GetString("text_override", "")
			now := time.Now().Unix()
			chatJID, burstMaxTS, sender, ok, err := d.Store.ApproveDraft(int64(id), textOverride, now)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or already resolved"), nil
			}
			// ct-2026-07-13-2243: mark only the burst that was dispatched.
			// burstMaxTS==0 means pre-ct-2243 draft → fall back to now.
			markTS := burstMaxTS
			if markTS == 0 {
				markTS = now
			}
			// T108 (ct-2026-09-01-1413): scoped to the draft's own sender in a
			// group — a group chat is born confirmation_mode="always" (the
			// NORMAL path there), so leaving this whole-chat would reopen the
			// exact defect this contract exists to fix, just one hop later.
			// sender is "" for a 1:1 draft or one created before this field
			// existed — markHandledForDispatch degrades to the pre-T108
			// whole-chat close in both cases, never breaks.
			if err := markHandledForDispatch(d, chatJID, sender, markTS); err != nil {
				log.Printf("approve_draft: mark_handled_before %s: %v", chatJID, err)
			}
			refreshQueue(d.State, d.Store)
			publishDraftChanged(d.Bus)
			return mcp.NewToolResultText("approved, moved to outbox"), nil
		})

	// ── discard_draft ────────────────────────────────────────────────────
	// S12 (ct-2026-07-30-1622): never sends → restricts, same reasoning as
	// "always"/"discretion"/"confirm"/"unattended"/"ignored" — always
	// allowed, no dispatch level required. If a bad actor talks the agent
	// into this, it costs one reply that doesn't go out; annoying, not
	// dangerous — the asymmetry with approve_draft is the whole point.
	s.AddTool(mcp.NewTool("discard_draft",
		mcp.WithDescription("Discard a pending draft — it will never be sent. Always allowed, at any dispatch level: discarding only prevents a send, it never causes one."),
		mcp.WithNumber("id", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ok, err := d.Store.DiscardDraft(int64(id))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or already resolved"), nil
			}
			publishDraftChanged(d.Bus)
			return mcp.NewToolResultText("discarded"), nil
		})

	// ── reject_draft ─────────────────────────────────────────────────────
	// T15 (ct-2026-08-05-123241): unlike discard_draft (final, never sent
	// again), rejecting asks for ANOTHER attempt — reason travels back to
	// the agent attached to the redispatch (capipush.dispatchPayload), up to
	// store.MaxDraftRounds rounds. Never sends → restricts, same reasoning
	// as discard_draft: always allowed, no dispatch level required.
	s.AddTool(mcp.NewTool("reject_draft",
		mcp.WithDescription("Reject a pending draft with a reason and ask for another attempt — unlike discard_draft, this redispatches the agent with the reason attached, up to 3 rounds total for the same chat. Always allowed, at any dispatch level: rejecting only prevents a send, it never causes one."),
		mcp.WithNumber("id", mcp.Required()),
		mcp.WithString("reason", mcp.Required(), mcp.Description("Why this draft is being rejected — travels back to the agent with the redispatch, verbatim"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			reason, err := r.RequireString("reason")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			chatJID, burstMaxTS, round, ok, err := d.Store.RejectDraft(int64(id), reason)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or already resolved"), nil
			}
			publishDraftChanged(d.Bus)
			if round >= store.MaxDraftRounds {
				return mcp.NewToolResultText(fmt.Sprintf("rejected — round %d reached the %d-round cap, no automatic redispatch; edit_draft or discard_draft to resolve it directly", round, store.MaxDraftRounds)), nil
			}
			if err := d.Store.MarkPendingBefore(chatJID, burstMaxTS); err != nil {
				log.Printf("reject_draft: mark_pending_before %s: %v", chatJID, err)
			}
			refreshQueue(d.State, d.Store)
			return mcp.NewToolResultText("rejected — redispatched for another attempt"), nil
		})

	// ── edit_draft ───────────────────────────────────────────────────────
	// T15 (ct-2026-08-05-123241): replaces text in place, no status change —
	// still pending, still needs approve_draft. Never sends → restricts,
	// same reasoning as discard_draft: always allowed, no dispatch level
	// required.
	s.AddTool(mcp.NewTool("edit_draft",
		mcp.WithDescription("Replace a pending draft's text in place, without approving or sending it — it still waits for approve_draft afterward. Always allowed, at any dispatch level."),
		mcp.WithNumber("id", mcp.Required()),
		mcp.WithString("text", mcp.Required(), mcp.Description("New text for the draft"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			id, err := r.RequireInt("id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			text, err := r.RequireString("text")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			ok, err := d.Store.EditDraft(int64(id), text)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if !ok {
				return mcp.NewToolResultError("draft not found or not pending"), nil
			}
			publishDraftChanged(d.Bus)
			return mcp.NewToolResultText("edited"), nil
		})

	// ── set_is_approver ──────────────────────────────────────────────────
	// Aprobador P1 (ct-2026-07-31-0610).
	s.AddTool(mcp.NewTool("set_is_approver",
		mcp.WithDescription("Grant or revoke the approver pin on a chat — lets it see the cross-chat drafts and pending queue (get_drafts/get_pending) and approve or discard them, without being the owner. Same write as POST /api/admin/approver."),
		mcp.WithString("chat_id", mcp.Required()),
		mcp.WithBoolean("is_approver", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			jid, err := r.RequireString("chat_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			approver, err := r.RequireBool("is_approver")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := d.Store.SetIsApprover(jid, approver); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("is_approver set to %v", approver)), nil
		})

	// ── set_kill_switch ──────────────────────────────────────────────────
	// H2+H3 hardening (ct-2026-07-10-0540): governor.SetKill and
	// state.SetMuted existed with no production caller — this is that
	// caller, and it flips BOTH together (they were two divergent flags
	// before this: the governor gate outbound sends, state.Muted only ever
	// mirrored mood for display). true halts corepipeline.processOutbox
	// (checks gov.Killed() already) AND now also state's Muted (added to
	// processOutbox in this same hardening pass) — the drain itself stops,
	// not just new sends being blocked from enqueuing.
	//
	// T19 (ct-2026-08-05-1249): also persists to store.SettingKillSwitch,
	// BEFORE the live governor/state calls — same reasoning as the REST
	// twin (restapi/admin.go's handleSetKillSwitch): main.go's
	// restoreKillSwitch reads this back on the next boot, before the
	// pipeline can send anything, so a restart never silently drops a
	// brake that was set for a real reason. Persisted best-effort (logged,
	// never blocks the emergency stop itself on a DB hiccup).
	s.AddTool(mcp.NewTool("set_kill_switch",
		mcp.WithDescription("OWNER-ONLY. Anti-ban emergency stop. true halts ALL outbound WhatsApp sends immediately (the outbox drain itself stops, nothing queues up to send later once un-killed silently) — use if the account looks at risk of a ban. false resumes normal operation. Survives a gateway restart."),
		mcp.WithBoolean("kill", mcp.Required())),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			kill, err := r.RequireBool("kill")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if d.Store != nil {
				if err := d.Store.SetSettingBool(store.SettingKillSwitch, kill); err != nil {
					log.Printf("mcpserver: persist kill switch: %v", err)
				}
			}
			if d.Governor != nil {
				d.Governor.SetKill(kill)
			}
			if d.State != nil {
				if err := d.State.SetMuted(kill); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
			}
			return mcp.NewToolResultText(fmt.Sprintf("kill switch set to %v", kill)), nil
		})

	// ── set_capi_connector ───────────────────────────────────────────────
	// Re-cablea la antena cAPI en 1 paso (ct-2026-07-18-1638, verbatim boss:
	// "un solo comando pequeñito, que lo haga automáticamente"). Reusa
	// Store.SetPrincipalAgent — el mismo write path que POST
	// /api/admin/agent-update usa para el principal (ct-2026-07-29, agentes
	// paso 3) — sin duplicar la lógica de cableo ni la validación del
	// endpoint (SetPrincipalAgent → isAllowedPrincipalEndpoint es el ÚNICO
	// lugar que decide qué se acepta — S6, ct-2026-07-30-031048: ANTES esta
	// tool forzaba http://127.0.0.1:<puerto> y descartaba la IP del string
	// antes de que esa validación la viera, anulando el fix que la habilita
	// a aceptar rangos privados — bloqueaba justo el caso Raspberry Pi
	// (gateway en la Pi, agente en otra máquina de la LAN) que ese fix vino
	// a habilitar. Ahora el endpoint se arma con la IP tal cual vino en el
	// string, y SetPrincipalAgent decide si se acepta).
	//
	// connector_string pasa a OPCIONAL (agentes paso 3) — antes esta era la
	// ÚNICA forma de tocar al principal por MCP y exigía repegar
	// credenciales completas solo para cambiarle el nombre. name (nuevo)
	// también opcional; se necesita al menos uno de los dos. Omitir un
	// campo == dejarlo como está (mismo contrato que set_agent_capi ya usa
	// para los secundarios) — se lee el estado actual vía
	// Store.PrincipalAgent antes de aplicar el override.
	s.AddTool(mcp.NewTool("set_capi_connector",
		mcp.WithDescription("Re-cablea la antena cAPI del agente principal y/o le cambia el nombre. connector_string: pegá el string tal cual lo imprime capi_credentials ('<ip:puerto> chat_id:<uuid> pin:<base64>') — se parsea, y el endpoint (con la IP tal cual vino) se valida contra el mismo criterio que /api/admin/agent-update: loopback o rango de red privada (RFC1918/RFC4193, link-local) se acepta — incluida la LAN de una Raspberry Pi —, cualquier dirección pública se rechaza. Si pasa, aplica el cambio en caliente + lo persiste. name: nombre de display del principal. Al menos uno de los dos es requerido; el que se omite queda como estaba."),
		mcp.WithString("connector_string", mcp.Description("String pegado tal cual, ej: '192.168.1.50:8787 chat_id:00000000-0000-4000-8000-000000000001 pin:AAAAAAAAAAAAAAAAAAAAAA=='. Omitir para no tocar endpoint/terminal/pin.")),
		mcp.WithString("name", mcp.Description("Nuevo nombre de display del principal. Omitir para no tocar el nombre actual."))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			if d.PrincipalTerminalID == "" {
				return mcp.NewToolResultError("no principal configured (PIUMY_DEFAULT_TERMINAL_ID unset)"), nil
			}
			raw := r.GetString("connector_string", "")
			name := r.GetString("name", "")
			if raw == "" && name == "" {
				return mcp.NewToolResultError("at least one of connector_string or name is required"), nil
			}

			var endpoint, terminalID, pinpass string
			if raw != "" {
				ip, port, tid, pp, err := capiconn.ParseConnectorString(raw)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				endpoint, terminalID, pinpass = "http://"+ip+":"+port, tid, pp
			}
			updated, err := updatePrincipalConnector(d, endpoint, terminalID, pinpass, name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("cAPI connector actualizado — endpoint: %s, terminal_id: %s, name: %s", updated.Endpoint, updated.AntennaTerminalID, updated.Name)), nil
		})
}

// updatePrincipalConnector overwrites whichever of the principal's connector
// fields is non-empty, keeps the rest, persists it and applies it in hot — the
// one write path set_capi_connector and set_agent_capi (when aimed at the
// principal, which has no `agents` row) share. SetPrincipalAgent is where the
// endpoint gets validated.
func updatePrincipalConnector(d Deps, endpoint, terminalID, pinpass, name string) (store.Agent, error) {
	current, _, err := d.Store.PrincipalAgent(d.PrincipalTerminalID)
	if err != nil {
		return store.Agent{}, fmt.Errorf("store: %w", err)
	}
	if endpoint != "" {
		current.Endpoint = endpoint
	}
	if terminalID != "" {
		current.AntennaTerminalID = terminalID
	}
	if pinpass != "" {
		current.Pinpass = pinpass
	}
	if name != "" {
		current.Name = name
	}
	if err := d.Store.SetPrincipalAgent(current.Name, current.Endpoint, current.AntennaTerminalID, current.Pinpass); err != nil {
		return store.Agent{}, err
	}
	if d.Connector != nil {
		d.Connector.SetConfig(current.Endpoint, current.AntennaTerminalID, current.Pinpass)
	}
	return current, nil
}
