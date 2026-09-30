package mcpserver

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// T170 DoD: a refused tool call leaves a line in the log with tool +
// reason, and never the message text.
func TestRefusedToolCallIsLogged(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-log-refusal")

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true})

	got := buf.String()
	if !strings.Contains(got, "tool set_kill_switch") || !strings.Contains(got, "terminal=term-log-refusal") || !strings.Contains(got, "boss-only") {
		t.Errorf("log after a refused call = %q, want tool, terminal and the refusal reason", got)
	}
}

func TestSuccessfulToolCallIsNotLoggedAsError(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	callTool(t, withTerminalID(ctx, "term-log-ok"), srv, "get_status", map[string]any{})

	if strings.Contains(buf.String(), "tool get_status") {
		t.Errorf("log after a successful call = %q, want no error line", buf.String())
	}
}
