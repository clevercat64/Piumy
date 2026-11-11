// Perfiles del MCP: el mismo registro de tools visto de tres maneras. Un agente
// que atiende chats conecta a /mcp/operator y no ve ninguna tool de
// configuración; uno que configura Piumy conecta a /mcp/manager. /mcp sigue
// sirviendo todo, para no romper a los agentes ya conectados (T178).
package mcpserver

import (
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/version"
)

// Set is the full registry plus its two derived views.
type Set struct {
	All      *server.MCPServer // PathAll: every tool
	Operator *server.MCPServer // PathOperator: answer chats
	Manager  *server.MCPServer // PathManager: configure Piumy
}

// Endpoint paths — one place, shared with the mount (mount.go) and with
// agent-connect.json via main.go.
const (
	PathAll      = "/mcp"
	PathOperator = "/mcp/operator"
	PathManager  = "/mcp/manager"
)

func nameSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// operatorTools: attending chats — reading, replying, the gate ritual, drafts,
// and the per-chat triage the operator manual already tells an agent to use
// (rules/status/active/mode), so splitting the surface drops nobody's workflow.
var operatorTools = nameSet(
	"get_status", "get_manual", "get_decision_policy",
	"get_instructions", "unlock", "remember", "skip",
	"send_message", "draft", "silent_act", "send_to_boss",
	"list_chats", "get_chat", "get_messages", "get_queue", "get_pending", "get_outbox",
	"get_drafts", "get_media", "get_media_full", "get_chat_groups", "resolve_chat",
	"mark_handled", "escalate", "claim_chat", "release_chat",
	"set_chat_memory", "set_chat_context",
	"approve_draft", "discard_draft", "reject_draft", "edit_draft",
	"set_chat_rules", "set_chat_status", "set_chat_active", "set_mode",
)

// managerTools: configuring Piumy — who is boss/approver, confirmation and
// config levels, agents and the principal, groups and profile, the kill
// switch. list_chats/get_chat ride along read-only: a manager has to find the
// chat it is about to configure.
var managerTools = nameSet(
	"get_status", "get_manual", "list_chats", "get_chat",
	"get_config", "set_config", "gateway_action",
	"set_is_boss", "set_is_approver", "set_type_rules", "set_confirmation_mode", "set_config_level",
	"set_chat_rules", "set_chat_status", "set_chat_active", "set_mode",
	"set_kill_switch", "reset_dashboard_password",
	"set_capi_connector", "register_agent", "set_agent_capi", "list_agents", "delete_agent",
	"promote_to_principal", "assign_chat_to_agent",
	"create_group", "add_participant", "promote_group_admin",
	"set_group_icon", "set_group_description", "set_profile_photo", "set_profile_status",
)

// deriveProfile copies the named tools out of all into a fresh server. The
// middlewares are per-call (mcp-go applies them when a tool runs), so the
// derived server needs the same chain as all to enforce the same rules.
func deriveProfile(all *server.MCPServer, name string, names map[string]bool, middlewares []server.ToolHandlerMiddleware) *server.MCPServer {
	s := server.NewMCPServer(name, version.Version, server.WithToolCapabilities(true))
	s.Use(middlewares...)
	for tool, entry := range all.ListTools() {
		if names[tool] {
			s.AddTools(*entry)
		}
	}
	return s
}
