package capipush

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

// CleverCoder opens an antenna on the first free port of 8787..8806 — the
// same range it walks itself to find one (skill capi-protocol, "Send"), so a
// terminal's port can differ from one session to the next. Ceiling, not a
// tunable: it is CleverCoder's own constant, and a change on its side is a
// change here.
const (
	cleverPortFirst = 8787
	cleverPortLast  = 8806

	// discoverProbeTimeout: CleverCoder answers /terminals in milliseconds and
	// a closed port refuses at once; the bound is for a port held by
	// something that accepts and never answers, which must not stall the
	// dispatch sweep that called us.
	discoverProbeTimeout = 500 * time.Millisecond
)

func cleverPorts() []int {
	ports := make([]int, 0, cleverPortLast-cleverPortFirst+1)
	for p := cleverPortFirst; p <= cleverPortLast; p++ {
		ports = append(ports, p)
	}
	return ports
}

// terminalListing is CleverCoder's GET /terminals on one of its antenna
// ports. NOT in external-agent-protocol.md — measured live and reported by
// CleverCoder's side (T177) — so every mismatch (a different app, a missing
// field, a 404, garbage) reads as "not here" and the injector stays exactly
// as it was before the discovery existed.
type terminalListing struct {
	App       string `json:"app"`
	Terminals []struct {
		ChatID string `json:"chat_id"`
	} `json:"terminals"`
}

// discoverLoopback looks for the antenna of terminalID on this machine's
// loopback ports and returns the endpoint that serves it. Only GET
// /terminals, never a handshake: a probing handshake could open an approval
// in CleverCoder's window, and the LAN is never touched. A chat_id is unique,
// so a hit is the terminal — it is either on that port or it is not.
func discoverLoopback(ports []int, terminalID string) (endpoint string, ok bool) {
	if terminalID == "" {
		return "", false
	}
	client := &http.Client{
		Timeout:   discoverProbeTimeout,
		Transport: &http.Transport{DisableKeepAlives: true}, // no idle connection left on a port we only glanced at
	}
	for _, port := range ports {
		if servesTerminal(client, port, terminalID) {
			return "http://127.0.0.1:" + strconv.Itoa(port), true
		}
	}
	return "", false
}

func servesTerminal(client *http.Client, port int, terminalID string) bool {
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/terminals")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var listing terminalListing
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listing); err != nil || listing.App != "clevercoder-capi" {
		return false
	}
	for _, t := range listing.Terminals {
		if t.ChatID == terminalID {
			return true
		}
	}
	return false
}
