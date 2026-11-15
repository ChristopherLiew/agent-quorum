// Package codex translates Codex lifecycle hook JSON to the neutral quorum protocol.
package codex

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
	"github.com/bytedance/sonic"
)

const Agent = "codex"

var plainJSON = sonic.Config{SortMapKeys: true, EscapeHTML: false, ValidateString: true}.Froze()

type Event struct {
	HookEvent string
	Tool      string
	Req       proto.Request
	HasReq    bool
}
type Output struct {
	Stdout []byte
	Exit   int
}
type hookInput struct {
	SessionID string                 `json:"session_id"`
	CWD       string                 `json:"cwd"`
	HookEvent string                 `json:"hook_event_name"`
	ToolName  string                 `json:"tool_name"`
	ToolInput sonic.NoCopyRawMessage `json:"tool_input"`
	Prompt    string                 `json:"prompt"`
}

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
		case "Bash":
			req.Op, req.Command = proto.OpGuard, field(in.ToolInput, "command")
		case "apply_patch":
			patch := field(in.ToolInput, "command")
			for _, line := range strings.Split(patch, "\n") {
				name := ""
				if strings.HasPrefix(line, "*** Update File: ") {
					name = strings.TrimPrefix(line, "*** Update File: ")
				}
				if strings.HasPrefix(line, "*** Add File: ") {
					name = strings.TrimPrefix(line, "*** Add File: ")
				}
				if strings.HasPrefix(line, "*** Delete File: ") {
					name = strings.TrimPrefix(line, "*** Delete File: ")
				}
				if strings.HasPrefix(line, "*** Move to: ") {
					name = strings.TrimPrefix(line, "*** Move to: ")
				}
				if name != "" {
					if !filepath.IsAbs(name) {
						if !filepath.IsAbs(in.CWD) {
							continue
						}
						name = filepath.Join(in.CWD, name)
					}
					req.Paths = append(req.Paths, filepath.Clean(name))
				}
			}
			if len(req.Paths) == 0 {
				return ev, nil
			}
			req.Op = proto.OpClaim
		default:
			return ev, nil
		}
		if req.Op == proto.OpGuard && req.Command == "" {
			return ev, nil
		}
	default:
		return ev, nil
	}
	ev.Req, ev.HasReq = req, true
	return ev, nil
}

func field(raw sonic.NoCopyRawMessage, key string) string {
	var m map[string]sonic.NoCopyRawMessage
	if sonic.ConfigStd.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	if sonic.ConfigStd.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return s
}

type output struct {
	HookSpecific hookSpecific `json:"hookSpecificOutput"`
}
type hookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

func Render(ev Event, r proto.Response) Output {
	if !r.OK {
		return Output{}
	}
	var h hookSpecific
	if ev.HookEvent == "PreToolUse" && r.Decision == proto.Deny {
		h = hookSpecific{HookEventName: "PreToolUse", PermissionDecision: "deny", PermissionDecisionReason: r.Reason}
	}
	if ev.HookEvent == "SessionStart" && r.Context != "" {
		h = hookSpecific{HookEventName: "SessionStart", AdditionalContext: r.Context}
	}
	if h.HookEventName == "" {
		return Output{}
	}
	var b bytes.Buffer
	enc := plainJSON.NewEncoder(&b)
	if enc.Encode(output{HookSpecific: h}) != nil {
		return Output{}
	}
	return Output{Stdout: b.Bytes()}
}
