// Package claude translates Claude Code hook JSON into the neutral wire
// request and the neutral response back into what Claude Code expects on
// stdout. Both directions are pure. See docs/DESIGN.md section 9.1.
package claude

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

// Agent is the value of proto.Request.Agent for this adapter.
const Agent = "claude"

// plainJSON is encoding/json compatible but leaves <, > and & unescaped.
var plainJSON = sonic.Config{SortMapKeys: true, EscapeHTML: false, ValidateString: true}.Froze()

const genericDeny = "agent-quorum: denied because another agent session holds this file."

// Event is the parsed hook call.
type Event struct {
	HookEvent string        // "SessionStart", "PreToolUse", "SessionEnd", "UserPromptSubmit", ...
	Tool      string        // tool_name for PreToolUse
	Req       proto.Request // filled when HasReq
	HasReq    bool          // false = no opinion: do not contact the daemon, print nothing
}

// Output is what the hook process writes and how it exits.
type Output struct {
	Stdout []byte // empty = no opinion
	Exit   int    // always 0
}

type hookInput struct {
	SessionID string                 `json:"session_id"`
	CWD       string                 `json:"cwd"`
	HookEvent string                 `json:"hook_event_name"`
	ToolName  string                 `json:"tool_name"`
	ToolInput sonic.NoCopyRawMessage `json:"tool_input"`
	Prompt    string                 `json:"prompt"`
}

// Parse turns one hook payload into an Event. It returns an error only for
// input that is not a JSON object of the expected shape; anything it merely
// has no opinion about comes back with HasReq false. It does not set Req.PID
// and does not normalise paths.
func Parse(raw []byte) (Event, error) {
	var in hookInput
	if err := sonic.ConfigStd.Unmarshal(raw, &in); err != nil {
		return Event{}, err
	}
	ev := Event{HookEvent: in.HookEvent, Tool: in.ToolName}
	if in.SessionID == "" {
		return ev, nil
	}
	req := proto.Request{V: proto.Version, Agent: Agent, Session: in.SessionID, CWD: in.CWD}

	switch in.HookEvent {
	case "SessionStart":
		req.Op = proto.OpHello
	case "SessionEnd":
		req.Op = proto.OpBye
	case "UserPromptSubmit":
		if in.Prompt == "" {
			return ev, nil
		}
		req.Op, req.Note = proto.OpNote, in.Prompt
	case "PreToolUse":
		switch in.ToolName {
		case "Edit", "Write", "MultiEdit", "NotebookEdit":
			p := toolString(in.ToolInput, "file_path")
			if p == "" {
				p = toolString(in.ToolInput, "notebook_path")
			}
			if !isAbs(p) {
				return ev, nil
			}
			req.Op, req.Paths = proto.OpClaim, []string{p}
		case "Bash":
			cmd := toolString(in.ToolInput, "command")
			if cmd == "" {
				return ev, nil
			}
			req.Op, req.Command = proto.OpGuard, cmd
		default:
			return ev, nil
		}
	default:
		return ev, nil
	}

	ev.Req, ev.HasReq = req, true
	return ev, nil
}

// toolString returns the string field key of tool_input, or "" when
// tool_input is not an object or the field is absent or not a string.
func toolString(toolInput sonic.NoCopyRawMessage, key string) string {
	var fields map[string]sonic.NoCopyRawMessage
	if sonic.ConfigStd.Unmarshal(toolInput, &fields) != nil {
		return ""
	}
	var s string
	if sonic.ConfigStd.Unmarshal(fields[key], &s) != nil {
		return ""
	}
	return s
}

func isAbs(p string) bool {
	return strings.HasPrefix(p, "/") || filepath.IsAbs(p)
}

type hookOutput struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

// hookSpecific never carries a permissionDecision other than "deny": a grant
// is expressed by printing nothing, so the normal permission flow continues.
type hookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// Render turns the daemon's answer into hook output. Only a deny on
// PreToolUse and a non-empty Context on SessionStart produce output.
func Render(ev Event, r proto.Response) Output {
	if !r.OK {
		return Output{}
	}
	switch {
	case ev.HookEvent == "PreToolUse" && r.Decision == proto.Deny:
		reason := r.Reason
		if reason == "" {
			reason = genericDeny
		}
		return Output{Stdout: line(hookSpecific{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		})}
	case ev.HookEvent == "SessionStart" && r.Context != "":
		return Output{Stdout: line(hookSpecific{
			HookEventName:     "SessionStart",
			AdditionalContext: r.Context,
		})}
	}
	return Output{}
}

// line encodes h as one line of JSON with a trailing newline, without HTML
// escaping. Encoding these string-only structs cannot fail; if it somehow
// did, nil (no opinion) is the fail-open answer.
func line(h hookSpecific) []byte {
	var buf bytes.Buffer
	enc := plainJSON.NewEncoder(&buf)
	if err := enc.Encode(hookOutput{HookSpecificOutput: h}); err != nil {
		return nil
	}
	return buf.Bytes()
}
