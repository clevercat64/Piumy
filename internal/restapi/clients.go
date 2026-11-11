package restapi

import (
	"net/http"
	"strings"
	"time"

	"piumy-gateway/internal/clientsfile"
	"piumy-gateway/internal/store"
)

// handleClients serves GET /api/clients (T172c, ct-2026-11-10-2254): the
// dashboard's Clientes tab. The file's fields pass through as CleverCoder
// wrote them; Piumy adds, per client, the two things only it knows —
// chat_jid (the group that client is) and agent_id (the agent assigned to it).
// Changing that agent is POST /api/admin/agent-assign, unchanged.
func (d Deps) handleClients(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	snap, err := clientsfile.Read(clientsfile.Path(), time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, client := range snap.Clients {
		chatJID, agentID, err := d.groupOfClient(client)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		client["chat_jid"], client["agent_id"] = chatJID, agentID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       snap.Status,
		"generated_at": snap.GeneratedAt,
		"clients":      snap.Clients,
	})
}

// groupOfClient finds the Piumy group a client is, and the agent manually
// assigned to it. CleverCoder keys a client by the first field of the "de:"
// line capipush writes, which is the JID's USER PART (capipush.jidNumber), so
// the key arrives as "<digits>" — or as a full JID if CleverCoder ever
// normalizes it; both resolve. "", "" when Piumy has no such group: a client
// is a group, never a 1:1 chat, and assigning a chat Piumy never saw does
// nothing.
func (d Deps) groupOfClient(client map[string]any) (chatJID, agentID string, err error) {
	key, _ := client["key"].(string)
	if key == "" {
		return "", "", nil
	}
	chatJID = key
	if !strings.Contains(key, "@") {
		chatJID += "@g.us"
	}
	chat, ok, err := d.Store.GetChat(chatJID)
	if err != nil || !ok || !store.IsGroupJID(chatJID) {
		return "", "", err
	}
	agentID, _ = store.AgentExclusiveID(chat.Status)
	return chatJID, agentID, nil
}
