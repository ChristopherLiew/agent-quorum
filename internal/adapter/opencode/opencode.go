// Package opencode translates the bridge events sent by the agent-quorum
// OpenCode plugin to the neutral quorum protocol.
//
// OpenCode plugins must be JavaScript modules, so plugin/opencode/index.js
// registers the hooks and forwards each one as a small bridge event on the
// hook's stdin. The bridge schema is owned here, not by OpenCode: the plugin
// maps OpenCode's objects onto it, so OpenCode API drift stays in one
// JavaScript function and every coordination decision stays in Go.
package opencode

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

// Agent is the value of proto.Request.Agent for this adapter.
const Agent = "opencode"

// Bridge event kinds sent by the plugin.
const (
	KindPrompt     = "prompt"     // a user prompt: register and record the note
	KindPermission = "permission" // OpenCode is evaluating a permission
	KindEnd        = "end"        // the session was deleted or the plugin unloaded
)

const genericDeny = "agent-quorum: denied because another agent session holds this file."

var plainJSON = sonic.Config{SortMapKeys: true, EscapeHTML: false, ValidateString: true}.Froze()

// Event is the parsed bridge event.
type Event struct {
	Kind   string
	Action string
	// PID is the process whose life bounds the session, as reported by the
	// plugin. It is 0 when OpenCode runs as a shared background service: that
	// process outlives every session, so the daemon falls back to its idle
	// timeout instead. Parse leaves Req.PID unset, as every adapter does.
	PID    int
	Req    proto.Request
	HasReq bool
}

// Output is what the hook process writes and how it exits.
type Output struct {
	Stdout []byte
	Exit   int
}

// bridgeInput is the JSON object the plugin writes to the hook's stdin.
type bridgeInput struct {
	Event     string   `json:"event"`
	SessionID string   `json:"session_id"` // root session: subagents use their parent's
	Directory string   `json:"directory"`  // the session's location directory
	Action    string   `json:"action"`     // permission action, e.g. "edit" or "shell"
	Resources []string `json:"resources"`  // permission resources: paths or commands
	Prompt    string   `json:"prompt"`
	PID       int      `json:"pid"` // 0 when unknown, see Event.PID
}

// bridgeOutput is the JSON object the plugin applies. At most one field is set.
type bridgeOutput struct {
	Context string `json:"context,omitempty"`
	Deny    string `json:"deny,omitempty"`
}

// Parse turns one bridge event into an Event. It returns an error only for
// input that is not valid JSON. A missing session, an empty prompt or an
// action agent-quorum does not coordinate returns HasReq: false (no opinion).
func Parse(raw []byte) (Event, error) {
	var in bridgeInput
	if err := sonic.ConfigStd.Unmarshal(raw, &in); err != nil {
		return Event{}, err
	}
	ev := Event{Kind: in.Event, Action: in.Action, PID: max(in.PID, 0)}
	if in.SessionID == "" {
		return ev, nil
	}

	dir := ""
	if filepath.IsAbs(in.Directory) {
		dir = filepath.Clean(in.Directory)
	}
	req := proto.Request{V: proto.Version, Agent: Agent, Session: in.SessionID, CWD: dir}

	switch in.Event {
	case KindPrompt:
		if in.Prompt == "" {
			return ev, nil
		}
		// Register on every prompt, as Antigravity does: OpenCode has no
		// session-start hook, and the response carries fresh peer context.
		req.Op, req.Note = proto.OpHello, in.Prompt
	case KindEnd:
		req.Op = proto.OpBye
	case KindPermission:
		switch in.Action {
		case "edit", "write", "patch":
			for _, r := range in.Resources {
				if p := absolutePath(r, dir); p != "" {
					req.Paths = append(req.Paths, p)
				}
			}
			if len(req.Paths) == 0 {
				return ev, nil
			}
			req.Op = proto.OpClaim
		case "shell", "bash":
			// OpenCode's scanner splits a compound command into several
			// resources. The guard splits on newlines too, so one deny among
			// them denies the whole command, as OpenCode itself does.
			var cmds []string
			for _, r := range in.Resources {
				if strings.TrimSpace(r) != "" {
					cmds = append(cmds, r)
				}
			}
			if len(cmds) == 0 {
				return ev, nil
			}
			req.Op, req.Command = proto.OpGuard, strings.Join(cmds, "\n")
		default:
			return ev, nil
		}
	default:
		return ev, nil
	}

	ev.Req, ev.HasReq = req, true
	return ev, nil
}

// absolutePath resolves an edit resource against the session directory.
// Patterns are not paths and are skipped.
func absolutePath(path, dir string) string {
	if path == "" || strings.ContainsAny(path, "*?[") {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, path)
}

// Render turns the daemon's response into the bridge output.
// A permission deny emits {"deny":"..."}; a prompt whose hello returned peer
// context emits {"context":"..."}. Everything else is empty stdout (no
// opinion). There is no allow: the plugin never grants a permission.
func Render(ev Event, r proto.Response) Output {
	if !r.OK {
		return Output{}
	}
	var out bridgeOutput
	switch {
	case ev.Kind == KindPermission && r.Decision == proto.Deny:
		out.Deny = r.Reason
		if out.Deny == "" {
			out.Deny = genericDeny
		}
	case ev.Kind == KindPrompt && r.Context != "":
		out.Context = r.Context
	default:
		return Output{}
	}
	var b bytes.Buffer
	if plainJSON.NewEncoder(&b).Encode(out) != nil {
		return Output{}
	}
	return Output{Stdout: b.Bytes()}
}
