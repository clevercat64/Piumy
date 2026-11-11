package mcpserver

import (
	"net/http"

	"github.com/mark3labs/mcp-go/server"
)

// Handler serves the three MCP endpoints behind ONE bearer key — the key is
// the owner's identity here (auth.go), and an agent may connect to either
// profile or both with the same credential.
func Handler(set Set, mcpKey string) http.Handler {
	mux := http.NewServeMux()
	for path, srv := range map[string]*server.MCPServer{
		PathAll:      set.All,
		PathOperator: set.Operator,
		PathManager:  set.Manager,
	} {
		mux.Handle(path, server.NewStreamableHTTPServer(srv,
			server.WithHTTPContextFunc(ExtractTerminalID),
			server.WithEndpointPath(path)))
	}
	return RequireBearerToken(mcpKey, mux)
}
