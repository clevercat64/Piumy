// errorLogMiddleware leaves one piumy.log line per tool call the gateway
// answered with an error result — levelGate and validateSend refusals, and
// every failure of the group/profile tools. T170 (ct-2026-09-29-2049): the
// boss's agent could not create/promote in a group and the only trace was
// the agent's own report; Piumy logged nothing, so the literal error was
// unrecoverable. Tool + terminal + reason only — never the call's arguments,
// so no message content reaches the log.
package mcpserver

import (
	"context"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// maxLoggedReasonLen keeps one pathological error text from flooding the log.
const maxLoggedReasonLen = 300

func errorLogMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := next(ctx, req)
			if err == nil && res != nil && res.IsError {
				log.Printf("mcpserver: tool %s terminal=%s error: %s", req.Params.Name, terminalIDFromContext(ctx), errorReason(res))
			}
			return res, err
		}
	}
}

func errorReason(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			if r := []rune(t.Text); len(r) > maxLoggedReasonLen {
				return string(r[:maxLoggedReasonLen]) + "…"
			}
			return t.Text
		}
	}
	return "(no text)"
}
