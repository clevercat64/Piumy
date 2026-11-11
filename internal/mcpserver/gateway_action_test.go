package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"piumy-gateway/internal/store"
)

type fakeDisconnecter struct {
	called bool
	err    error
}

func (f *fakeDisconnecter) Logout(context.Context) error { f.called = true; return f.err }

type fakeReconnecter struct {
	called bool
	err    error
}

func (f *fakeReconnecter) Reconnect(context.Context) error { f.called = true; return f.err }

type fakeResetter struct{ kicked bool }

func (f *fakeResetter) KickResync() { f.kicked = true }

func TestGatewayActionReconnectAndDisconnect(t *testing.T) {
	dis, rec := &fakeDisconnecter{}, &fakeReconnecter{}
	srv, ctx, _ := configServer(t, func(d *Deps) { d.Disconnecter, d.Reconnecter = dis, rec })

	text, isErr := configCall(t, ctx, srv, "gateway_action", map[string]any{"action": "disconnect"})
	if isErr || !dis.called || !strings.Contains(text, "disconnected") {
		t.Errorf("disconnect = %s (error=%v, called=%v)", text, isErr, dis.called)
	}
	text, isErr = configCall(t, ctx, srv, "gateway_action", map[string]any{"action": "reconnect"})
	if isErr || !rec.called || !strings.Contains(text, "reconnecting") {
		t.Errorf("reconnect = %s (error=%v, called=%v)", text, isErr, rec.called)
	}
}

func TestGatewayActionSurfacesTheGatewaysOwnError(t *testing.T) {
	srv, ctx, _ := configServer(t, func(d *Deps) { d.Reconnecter = &fakeReconnecter{err: errors.New("already paired")} })
	text, isErr := configCall(t, ctx, srv, "gateway_action", map[string]any{"action": "reconnect"})
	if !isErr || !strings.Contains(text, "already paired") {
		t.Errorf("reconnect error = %s (error=%v), want the gateway's own message", text, isErr)
	}
}

func TestGatewayActionWithoutAGatewayRefusesClearly(t *testing.T) {
	srv, ctx, _ := configServer(t, nil)
	for _, action := range []string{"reconnect", "disconnect"} {
		text, isErr := configCall(t, ctx, srv, "gateway_action", map[string]any{"action": action})
		if !isErr || !strings.Contains(text, "not available") {
			t.Errorf("%s with no gateway wired = %s (error=%v)", action, text, isErr)
		}
	}
	if text, isErr := configCall(t, ctx, srv, "gateway_action", map[string]any{"action": "explode"}); !isErr || !strings.Contains(text, "unknown action") {
		t.Errorf("unknown action = %s (error=%v)", text, isErr)
	}
}

// reset wipes the messaging data and the media files, keeps what the owner
// configured, and asks the gateway to re-sync — the same contract as the
// dashboard's POST /api/admin/reset.
func TestGatewayActionResetWipesDataButKeepsConfiguration(t *testing.T) {
	var st *store.Store
	mediaDir := t.TempDir()
	res := &fakeResetter{}
	srv, ctx, _ := configServer(t, func(d *Deps) { st, d.MediaDir, d.Resetter = d.Store, mediaDir, res })

	chat := "555000001@s.whatsapp.net"
	if err := st.TouchChat(chat, "Contacto Uno", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDraft(chat, "hola", "m", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet("identity", "una tienda"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgent(store.Agent{AgentID: "agente-a", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "huerfano.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	text, isErr := configCall(t, ctx, srv, "gateway_action", map[string]any{"action": "reset"})
	if isErr || !strings.Contains(text, `"reset"`) {
		t.Fatalf("reset = %s (error=%v)", text, isErr)
	}
	if chats, err := st.ListChats(10); err != nil || len(chats) != 0 {
		t.Errorf("chats after reset = %v err=%v, want none", chats, err)
	}
	if drafts, err := st.PendingDrafts(10); err != nil || len(drafts) != 0 {
		t.Errorf("drafts after reset = %v err=%v, want none", drafts, err)
	}
	if got, _ := st.KVGet("identity"); got != "una tienda" {
		t.Errorf("a setting did not survive the reset: %q", got)
	}
	if _, ok, _ := st.GetAgent("agente-a"); !ok {
		t.Error("a registered agent did not survive the reset")
	}
	if entries, err := os.ReadDir(mediaDir); err != nil || len(entries) != 0 {
		t.Errorf("media dir after reset = %v err=%v, want it emptied but still there", entries, err)
	}
	if !res.kicked {
		t.Error("the gateway was never asked to re-sync")
	}
}

func TestGatewayActionLivesOnTheManagerProfileOnly(t *testing.T) {
	set := newTestSet(t)
	if _, ok := set.Manager.ListTools()["gateway_action"]; !ok {
		t.Error("manager lacks gateway_action")
	}
	if _, ok := set.Operator.ListTools()["gateway_action"]; ok {
		t.Error("operator has gateway_action, it must not")
	}
}
