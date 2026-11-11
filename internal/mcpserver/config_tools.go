// get_config / set_config (T178): everything adjustable about a Piumy install,
// readable and writable through two tools instead of one per setting. One
// table (configKeys) is the whole surface — add a key there and both tools
// have it. Lives on the manager profile only (profiles.go).
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/store"
)

const antiBanNote = "solo lectura: freno anti-ban"

// configKey is one adjustable setting. get reports its current value and where
// that value comes from ("kv" = set here or in the dashboard, "default" = never
// touched, the install's own default applies). set validates and writes.
type configKey struct {
	name     string
	typ      string
	desc     string
	enum     []string
	note     string
	readOnly bool
	// applies is "" for a change that takes effect at once, "restart" for one
	// that is only read at startup.
	applies string
	// preflight, when set, runs before set and may return a warning to show
	// next to the result.
	preflight func(d Deps, v string) string
	get       func(d Deps) (value, source string, err error)
	set       func(d Deps, v string) error
}

func kvGet(d Deps, setting string) (string, string, error) {
	v, err := d.Store.KVGet(setting)
	if v == "" {
		return "", "default", err
	}
	return v, "kv", err
}

// boolKey is an on/off switch stored as KV; until it is touched it shows what
// the install defaults to.
func boolKey(name, desc, setting string, defaultOf func(Deps) bool) configKey {
	return configKey{
		name: name, typ: "bool", desc: desc,
		get: func(d Deps) (string, string, error) {
			raw, _, err := kvGet(d, setting)
			if raw == "" || err != nil {
				return strconv.FormatBool(defaultOf(d)), "default", err
			}
			return strconv.FormatBool(raw == "1" || raw == "true"), "kv", nil
		},
		set: func(d Deps, v string) error {
			on, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("must be true or false, got %q", v)
			}
			return d.Store.SetSettingBool(setting, on)
		},
	}
}

// envLimitKey is a send cap Piumy starts with from its environment: shown so
// an agent can answer "what is my limit", never changed from here.
func envLimitKey(name, desc string, pick func(Deps) int) configKey {
	return configKey{
		name: name, typ: "int", desc: desc, readOnly: true, note: antiBanNote,
		get: func(d Deps) (string, string, error) {
			if n := pick(d); n > 0 {
				return strconv.Itoa(n), "env", nil
			}
			return "", "default", nil
		},
	}
}

func textKey(name, desc, setting string) configKey {
	return configKey{
		name: name, typ: "text", desc: desc,
		get: func(d Deps) (string, string, error) { return kvGet(d, setting) },
		set: func(d Deps, v string) error { return d.Store.KVSet(setting, v) },
	}
}

func configLevelDefaultKey(name, desc, setting string) configKey {
	return configKey{
		name: name, typ: "enum", desc: desc, enum: []string{"auto", "confirm", "unattended", "ignored"},
		get: func(d Deps) (string, string, error) {
			_, source, err := kvGet(d, setting)
			if err != nil {
				return "", "", err
			}
			level, err := d.Store.EffectiveConfigLevelDefault(setting)
			return level, source, err
		},
		set: func(d Deps, v string) error {
			if !store.ValidConfigLevelDefault(v) {
				return fmt.Errorf("must be one of auto, confirm, unattended, ignored, got %q", v)
			}
			return d.Store.KVSet(setting, v)
		},
	}
}

func agentDefaultKey(name, desc, setting string) configKey {
	return configKey{
		name: name, typ: "agent_id", desc: desc + " (\"\" clears it; the principal or any registered agent)",
		get: func(d Deps) (string, string, error) { return kvGet(d, setting) },
		set: func(d Deps, v string) error { return d.Store.SetAgentDefault(setting, v, d.PrincipalTerminalID) },
	}
}

// antiBanKey is a pacing knob that stays read-only in v1: raising it past what
// WhatsApp tolerates is the one thing the owner asked to protect ("no cagarla
// con whatsapp espamear su ip"). Flip readOnly here to open one up.
func antiBanKey(name, typ, desc, setting string) configKey {
	return configKey{
		name: name, typ: typ, desc: desc, readOnly: true, note: antiBanNote,
		get: func(d Deps) (string, string, error) { return kvGet(d, setting) },
	}
}

// debounceKey is a capipush wait, in whole seconds. other is the sibling
// setting: the ceiling may not sit below the floor once both are set.
func debounceKey(name, desc, setting, other string, isCeiling bool) configKey {
	return configKey{
		name: name, typ: "seconds", desc: desc,
		get: func(d Deps) (string, string, error) {
			raw, _, err := kvGet(d, setting)
			if raw == "" || err != nil {
				return "", "default", err
			}
			dur, perr := time.ParseDuration(raw)
			if perr != nil {
				return raw, "kv", nil
			}
			return strconv.Itoa(int(dur / time.Second)), "kv", nil
		},
		set: func(d Deps, v string) error {
			secs, err := strconv.Atoi(v)
			if err != nil || secs < 0 {
				return fmt.Errorf("must be a whole number of seconds, 0 or more, got %q", v)
			}
			if raw, _ := d.Store.KVGet(other); raw != "" {
				if o, perr := time.ParseDuration(raw); perr == nil {
					mine := time.Duration(secs) * time.Second
					if (isCeiling && mine < o) || (!isCeiling && mine > o) {
						return fmt.Errorf("the ceiling (max_dispatch_debounce_sec) may not be below the floor (dispatch_debounce_sec)")
					}
				}
			}
			return d.Store.SetSettingDuration(setting, time.Duration(secs)*time.Second)
		},
	}
}

const defaultTerminalEnv = "PIUMY_DEFAULT_TERMINAL_ID"

// defaultTerminalKey is the principal agent's terminal id. It is read once at
// startup and copied into the gate, the level middleware and capipush, so it
// cannot change live: the writer is piumy-config.json, which the next startup
// reads (config.ApplyFileDefaults) — unless the environment sets it, which wins.
func defaultTerminalKey() configKey {
	return configKey{
		name: "default_terminal_id", typ: "text", applies: "restart",
		desc: "Terminal id of the principal agent — it receives the chats no other agent was assigned and works without a dispatch. Read once at startup: a change applies after Piumy restarts",
		get: func(d Deps) (string, string, error) {
			running := d.PrincipalTerminalID
			if d.ConfigFileDir == "" {
				if running == "" {
					return "", "default", nil
				}
				return running, "env", nil
			}
			file := config.FileValue(d.ConfigFileDir, defaultTerminalEnv)
			switch {
			case file != "" && file != running:
				return file, "file (pending restart)", nil
			case running == "":
				return "", "default", nil
			case file == running:
				return running, "file", nil
			}
			return running, "env", nil
		},
		preflight: func(d Deps, v string) string {
			if d.ConfigFileDir == "" {
				return ""
			}
			// The file's own value was copied into the environment at startup, so
			// an environment value that differs from it came from outside.
			if env := os.Getenv(defaultTerminalEnv); env != "" && env != config.FileValue(d.ConfigFileDir, defaultTerminalEnv) {
				return fmt.Sprintf("the environment sets %s=%q and wins over piumy-config.json until that variable is removed", defaultTerminalEnv, env)
			}
			return ""
		},
		set: func(d Deps, v string) error {
			if d.ConfigFileDir == "" {
				return errors.New("piumy-config.json is not reachable from here (no config directory wired)")
			}
			return config.SetFileValue(d.ConfigFileDir, defaultTerminalEnv, v)
		},
	}
}

var errNoRouter = errors.New("router not available")

func routerDefaultModeKey() configKey {
	return configKey{
		name: "router.default_mode", typ: "enum", enum: []string{"auto", "dedicated"},
		desc: "Mode a chat gets when no route says otherwise (router.json)",
		get: func(d Deps) (string, string, error) {
			if d.Router == nil {
				return "", "", errNoRouter
			}
			return d.Router.Snapshot().DefaultMode, "file", nil
		},
		set: func(d Deps, v string) error {
			if d.Router == nil {
				return errNoRouter
			}
			if v != "auto" && v != "dedicated" {
				return fmt.Errorf("must be auto or dedicated, got %q", v)
			}
			return d.Router.Update(func(c *router.Config) { c.DefaultMode = v })
		},
	}
}

const routeKeyPrefix = "router.route:"

// routeBody is a route minus its match, which is the key's own name.
type routeBody struct {
	Mode       string `json:"mode,omitempty"`
	Plugin     string `json:"plugin,omitempty"`
	Model      string `json:"model,omitempty"`
	VIP        bool   `json:"vip,omitempty"`
	TerminalID string `json:"terminal_id,omitempty"`
}

// routeKey is one entry of router.json's routes, addressed by its match (a JID
// or "*"). Any match is valid to set: a route that doesn't exist yet is created.
func routeKey(match string) configKey {
	return configKey{
		name: routeKeyPrefix + match, typ: "json",
		desc: "Route for " + strconv.Quote(match) + " in router.json (a JID, or \"*\" for every chat): JSON {\"mode\":\"auto|dedicated\",\"plugin\":\"\",\"model\":\"\",\"vip\":false,\"terminal_id\":\"\"}; \"\" removes it",
		get: func(d Deps) (string, string, error) {
			if d.Router == nil {
				return "", "", errNoRouter
			}
			for _, r := range d.Router.Snapshot().Routes {
				if r.Match == match {
					b, err := json.Marshal(routeBody{r.Mode, r.Plugin, r.Model, r.VIP, r.TerminalID})
					return string(b), "file", err
				}
			}
			return "", "default", nil
		},
		set: func(d Deps, v string) error {
			if d.Router == nil {
				return errNoRouter
			}
			if match == "" {
				return errors.New(`a route needs a match: a JID, or "*"`)
			}
			var route *router.Route
			if v != "" {
				dec := json.NewDecoder(strings.NewReader(v))
				dec.DisallowUnknownFields()
				var b routeBody
				if err := dec.Decode(&b); err != nil {
					return fmt.Errorf("not a route: %v", err)
				}
				if b.Mode != "" && b.Mode != "auto" && b.Mode != "dedicated" {
					return fmt.Errorf("mode must be auto or dedicated, got %q", b.Mode)
				}
				route = &router.Route{Match: match, Mode: b.Mode, Plugin: b.Plugin, Model: b.Model, VIP: b.VIP, TerminalID: b.TerminalID}
			}
			// A fresh slice every time: Snapshot hands out the live one.
			return d.Router.Update(func(c *router.Config) {
				routes := make([]router.Route, 0, len(c.Routes)+1)
				for _, r := range c.Routes {
					if r.Match != match {
						routes = append(routes, r)
					} else if route != nil {
						routes, route = append(routes, *route), nil
					}
				}
				if route != nil {
					routes = append(routes, *route)
				}
				c.Routes = routes
			})
		},
	}
}

// configKeys is the whole adjustable surface.
func configKeys() []configKey {
	return []configKey{
		configLevelDefaultKey("config_level_default_new", "Config level a chat from an unknown number starts with", store.SettingConfigLevelDefaultNew),
		configLevelDefaultKey("config_level_default_contact", "Config level a chat from a saved contact starts with", store.SettingConfigLevelDefaultContact),
		textKey("rules_default_new_number", "Rules a chat from an unknown number inherits when it has none of its own", store.SettingRulesDefaultNewNumber),
		textKey("rules_default_contact", "Rules a chat from a saved contact inherits when it has none of its own", store.SettingRulesDefaultContact),
		{
			name: "rules_type_group", typ: "text", desc: "Rules every group inherits unless it has its own",
			get: func(d Deps) (string, string, error) { return kvGet(d, store.SettingRulesTypeGroup) },
			set: func(d Deps, v string) error { return d.Store.SetTypeRules(v) },
		},
		textKey("identity", "What the assistant is OF (a business, a busy person...) — the line every rules tier sits under", store.SettingIdentity),
		agentDefaultKey("agent_default_new_number", "Agent that attends chats from unknown numbers", store.SettingAgentDefaultNewNumber),
		agentDefaultKey("agent_default_contact", "Agent that attends chats from saved contacts", store.SettingAgentDefaultContact),
		agentDefaultKey("agent_default_group", "Agent that attends groups", store.SettingAgentDefaultGroup),
		agentDefaultKey("agent_default_boss", "Agent that attends the owner's chat", store.SettingAgentDefaultBoss),
		debounceKey("dispatch_debounce_sec", "Seconds of silence in a chat before its burst of messages is handed to the agent", store.SettingCapipushDispatchDebounce, store.SettingCapipushMaxDispatchDebounce, false),
		debounceKey("max_dispatch_debounce_sec", "Ceiling in seconds: a burst goes out by then even if the chat stays active", store.SettingCapipushMaxDispatchDebounce, store.SettingCapipushDispatchDebounce, true),
		boolKey("drift_reports", "Whether Piumy may send the maintainers a short anonymous note when its picture of the world differs from reality", store.SettingDriftReports, func(d Deps) bool { return d.DriftReportsDefault }),
		boolKey("auto_update", "Whether Piumy may download and install a newer release by itself, when nothing is in flight", store.SettingAutoUpdate, func(d Deps) bool { return d.AutoUpdateDefault }),
		{
			name: "language", typ: "text", desc: "Language of the dashboard, tray and notices (\"\" = follow the OS)", note: "the tray menu picks a change up on the next restart",
			get: func(d Deps) (string, string, error) { return kvGet(d, store.SettingLanguage) },
			set: func(d Deps, v string) error {
				if v != "" && !i18n.Valid(i18n.Lang(v)) {
					return fmt.Errorf("unsupported language %q", v)
				}
				return d.Store.KVSet(store.SettingLanguage, v)
			},
		},
		{
			name: "dashboard_recovery_email", typ: "text", desc: "Address the dashboard's password recovery sends its code to",
			get: func(d Deps) (string, string, error) { return kvGet(d, store.SettingDashRecoveryEmail) },
			set: func(d Deps, v string) error {
				if !store.ValidRecoveryEmail(v) {
					return fmt.Errorf("not an email address: %q", v)
				}
				return d.Store.KVSet(store.SettingDashRecoveryEmail, v)
			},
		},
		defaultTerminalKey(),
		routerDefaultModeKey(),
		envLimitKey("rate_limit_per_min", "Messages Piumy may send per minute — the anti-ban governor's cap", func(d Deps) int { return d.RateLimitPerMin }),
		envLimitKey("rate_limit_per_day", "Messages Piumy may send per day — the anti-ban governor's cap", func(d Deps) int { return d.RateLimitPerDay }),
		antiBanKey("delay_dispatch_min", "duration", "Shortest random wait before the agent's reply is dispatched", store.SettingDispatchDelayMin),
		antiBanKey("delay_dispatch_max", "duration", "Longest random wait before the agent's reply is dispatched", store.SettingDispatchDelayMax),
		antiBanKey("delay_read_min", "duration", "Shortest random wait before an inbound message is marked read", store.SettingReadDelayMin),
		antiBanKey("delay_read_max", "duration", "Longest random wait before an inbound message is marked read", store.SettingReadDelayMax),
		antiBanKey("delay_action_min", "duration", "Shortest random wait between WhatsApp account actions", store.SettingActionDelayMin),
		antiBanKey("delay_action_max", "duration", "Longest random wait between WhatsApp account actions", store.SettingActionDelayMax),
		antiBanKey("delay_chunk_min", "duration", "Shortest wait between the pieces of one long reply", store.SettingChunkDelayMin),
		antiBanKey("delay_chunk_max", "duration", "Longest wait between the pieces of one long reply", store.SettingChunkDelayMax),
		antiBanKey("chunk_max_len", "int", "Characters at which a long reply is split into pieces", store.SettingChunkMaxLen),
	}
}

// allConfigKeys is the table plus one key per route router.json has today.
func allConfigKeys(d Deps) []configKey {
	keys := configKeys()
	if d.Router != nil {
		for _, r := range d.Router.Snapshot().Routes {
			keys = append(keys, routeKey(r.Match))
		}
	}
	return keys
}

func findConfigKey(name string) (configKey, bool) {
	for _, k := range configKeys() {
		if k.name == name {
			return k, true
		}
	}
	if match, ok := strings.CutPrefix(name, routeKeyPrefix); ok {
		return routeKey(match), true
	}
	return configKey{}, false
}

func configKeyNames() string {
	var names []string
	for _, k := range configKeys() {
		names = append(names, k.name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ") + ", " + routeKeyPrefix + "<match>"
}

type configEntry struct {
	Key         string   `json:"key"`
	Value       string   `json:"value"`
	Source      string   `json:"source"`
	Type        string   `json:"type"`
	Enum        []string `json:"enum,omitempty"`
	Writable    bool     `json:"writable"`
	Note        string   `json:"note,omitempty"`
	Description string   `json:"description"`
}

func addConfigTools(s *server.MCPServer, d Deps, tracker *agentTracker) {
	s.AddTool(mcp.NewTool("get_config",
		mcp.WithDescription("Everything adjustable about this Piumy install: each setting's current value, where it comes from (source: kv = set here or in the dashboard, default = never touched, so the install's own default applies), its type, and whether set_config can change it. Pass prefix to list only the keys that start with it."),
		mcp.WithString("prefix", mcp.Description("Optional: only keys starting with this, e.g. \"agent_default\""))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			prefix := r.GetString("prefix", "")
			entries := []configEntry{}
			for _, k := range allConfigKeys(d) {
				if !strings.HasPrefix(k.name, prefix) {
					continue
				}
				value, source, err := k.get(d)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("%s: %v", k.name, err)), nil
				}
				entries = append(entries, configEntry{
					Key: k.name, Value: value, Source: source, Type: k.typ, Enum: k.enum,
					Writable: !k.readOnly, Note: k.note, Description: k.desc,
				})
			}
			return jsonResult(map[string]any{"keys": entries})
		})

	s.AddTool(mcp.NewTool("set_config",
		mcp.WithDescription("Change one setting from get_config. value is always a string (\"true\"/\"false\", seconds as a number, \"\" to clear). The response says the old and new value and whether the change applies live. An unknown key lists the valid ones."),
		mcp.WithString("key", mcp.Required(), mcp.Description("A key from get_config")),
		mcp.WithString("value", mcp.Required(), mcp.Description("The new value, as a string; \"\" clears it where clearing makes sense"))),
		func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tracker.seen(ctx)
			name, err := r.RequireString("key")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			value, ok := r.GetArguments()["value"].(string)
			if !ok {
				return mcp.NewToolResultError(`value is required, as a string ("" to clear)`), nil
			}
			k, found := findConfigKey(name)
			if !found {
				return mcp.NewToolResultError(fmt.Sprintf("unknown key %q — valid keys: %s", name, configKeyNames())), nil
			}
			if k.readOnly {
				return mcp.NewToolResultError(fmt.Sprintf("%s: %s", k.name, k.note)), nil
			}
			old, _, err := k.get(d)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("%s: %v", k.name, err)), nil
			}
			note := k.note
			if k.preflight != nil {
				if warning := k.preflight(d, value); warning != "" {
					note = warning
				}
			}
			if err := k.set(d, value); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("%s: %v", k.name, err)), nil
			}
			applied := "live"
			if k.applies != "" {
				applied = k.applies
			}
			out := map[string]string{"key": k.name, "old": old, "new": value, "applied": applied}
			if note != "" {
				out["note"] = note
			}
			return jsonResult(out)
		})
}
