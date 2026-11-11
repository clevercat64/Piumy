// gateway_action (T178): the three one-off things the dashboard can do to the
// WhatsApp connection and its data that are not settings — reconnect,
// disconnect, reset. Their REST twins are POST /api/admin/reconnect|
// disconnect|reset. Manager profile only (profiles.go).
package mcpserver

import (
	"context"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/mediautil"
)

// Disconnecter logs the WhatsApp session out — same shape as
// restapi.Disconnecter, defined here so neither surface imports the other.
type Disconnecter interface {
	Logout(ctx context.Context) error
}

// Reconnecter starts a new pairing flow after a logout (restapi.Reconnecter).
type Reconnecter interface {
	Reconnect(ctx context.Context) error
}

// Resetter re-syncs contacts and groups after a reset (restapi.Resetter).
type Resetter interface {
	KickResync()
}

func addGatewayActionTool(s *server.MCPServer, d Deps, tracker *agentTracker) {
	s.AddTool(mcp.NewTool("gateway_action",
		mcp.WithDescription("Run a one-off action on the WhatsApp connection or its data — not a setting (those are set_config). reconnect: start a new pairing (QR) flow after a disconnect, or report that the number is already paired. disconnect: log this WhatsApp session out — Piumy stops receiving and sending until the number is paired again with a QR. reset: WIPE every chat, message, group membership, media file, draft and queued outgoing message so Piumy starts from zero, then re-sync from WhatsApp; it KEEPS settings, registered agents, the dashboard password and the usage counters, and never touches the WhatsApp session itself. reset cannot be undone."),
		mcp.WithString("action", mcp.Required(), mcp.Enum("reconnect", "disconnect", "reset"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			action, err := r.RequireString("action")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			var status string
			switch action {
			case "reconnect":
				if d.Reconnecter == nil {
					return mcp.NewToolResultError("reconnect: gateway not available"), nil
				}
				if err := d.Reconnecter.Reconnect(ctx); err != nil {
					return mcp.NewToolResultError("reconnect: " + err.Error()), nil
				}
				status = "reconnecting"
			case "disconnect":
				if d.Disconnecter == nil {
					return mcp.NewToolResultError("disconnect: gateway not available"), nil
				}
				if err := d.Disconnecter.Logout(ctx); err != nil {
					return mcp.NewToolResultError("disconnect: " + err.Error()), nil
				}
				status = "disconnected"
			case "reset":
				if err := d.Store.ResetMessagingData(); err != nil {
					return mcp.NewToolResultError("reset: " + err.Error()), nil
				}
				if d.MediaDir != "" {
					if err := mediautil.ClearDir(d.MediaDir); err != nil {
						log.Printf("mcpserver: reset: clear media dir %s: %v", d.MediaDir, err)
					}
				}
				if d.Resetter != nil {
					d.Resetter.KickResync()
				}
				status = "reset"
			default:
				return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
			}
			return jsonResult(map[string]string{"action": action, "status": status})
		})
}
