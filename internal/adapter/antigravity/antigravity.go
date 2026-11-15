// Package antigravity translates Antigravity lifecycle hook JSON to the neutral quorum protocol.
package antigravity

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

// Agent is the value of proto.Request.Agent for this adapter.
const Agent = "antigravity"

var plainJSON = sonic.Config{SortMapKeys: true, EscapeHTML: false, ValidateString: true}.Froze()

const genericDeny = "agent-quorum: denied because another agent session holds this file."

// Event is the parsed Antigravity hook call.
type Event struct {
	HookEvent string
	Tool      string
	Req       proto.Request
	HasReq    bool
}

// Output is what the hook process writes and how it exits.
type Output struct {
	Stdout []byte
	Exit   int
}

type hookInput struct {
	ConversationID string                 `json:"conversationId"`
	SessionID      string                 `json:"sessionId"`
	LegacySession  string                 `json:"session_id"`
	WorkspacePaths []string               `json:"workspacePaths"`
	CWD            string                 `json:"cwd"`
	HookEvent      string                 `json:"hookEventName"`
	LegacyEvent    string                 `json:"hook_event_name"`
	LastUserInput  string                 `json:"lastUserInput"`
	LegacyPrompt   string                 `json:"prompt"`
	FullyIdle      bool                   `json:"fullyIdle"`
	ToolCall       *toolCall              `json:"toolCall"`
	ToolName       string                 `json:"tool_name"`
	ToolInput      sonic.NoCopyRawMessage `json:"tool_input"`
}

type toolCall struct {
	ID   string                 `json:"id"`
	Name string                 `json:"name"`
	Args sonic.NoCopyRawMessage `json:"args"`
}

// Parse turns one Antigravity hook payload into an Event. It returns an error
// only for input that is not valid JSON. Missing sessions or unhandled tools
// return HasReq: false (no opinion).
func Parse(raw []byte) (Event, error) {
	var in hookInput
	if err := sonic.ConfigStd.Unmarshal(raw, &in); err != nil {
		return Event{}, err
	}

	sessionID := in.ConversationID
	if sessionID == "" {
		sessionID = in.SessionID
	}
	if sessionID == "" {
		sessionID = in.LegacySession
	}

	hookEvent := in.HookEvent
	if hookEvent == "" {
		hookEvent = in.LegacyEvent
	}

	toolName := in.ToolName
	toolArgs := in.ToolInput
	if in.ToolCall != nil {
		if in.ToolCall.Name != "" {
			toolName = in.ToolCall.Name
		}
		if len(in.ToolCall.Args) > 0 {
			toolArgs = in.ToolCall.Args
		}
	}

	ev := Event{HookEvent: hookEvent, Tool: toolName}
	if sessionID == "" {
		return ev, nil
	}

	cwd := workingDirectory(in, toolArgs, toolName)
	req := proto.Request{V: proto.Version, Agent: Agent, Session: sessionID, CWD: cwd}

	switch hookEvent {
	case "SessionStart":
		req.Op = proto.OpHello
	case "Stop":
		if !in.FullyIdle {
			return ev, nil
		}
		req.Op = proto.OpBye
	case "SessionEnd":
		req.Op = proto.OpBye
	case "PreInvocation", "UserPromptSubmit":
		prompt := in.LastUserInput
		if prompt == "" {
			prompt = in.LegacyPrompt
		}
		if prompt == "" {
			return ev, nil
		}
		// Re-register on each prompt. Antigravity's Stop event ends the current
		// run, and a later prompt in the same conversation must register again.
		req.Op, req.Note = proto.OpHello, prompt
	case "PreToolUse":
		switch toolName {
		case "run_command", "Bash", "bash":
			cmd := field(toolArgs, "CommandLine")
			if cmd == "" {
				cmd = field(toolArgs, "command")
			}
			if cmd == "" {
				return ev, nil
			}
			req.Op, req.Command = proto.OpGuard, cmd
		case "write_to_file", "replace_file_content", "multi_replace_file_content",
			"write_file", "edit_file":
			p := firstField(toolArgs, "TargetFile", "AbsolutePath", "Path", "path", "file_path")
			if p == "" {
				return ev, nil
			}
			p = absolutePath(p, cwd)
			if p == "" {
				return ev, nil
			}
			req.Op, req.Paths = proto.OpClaim, []string{p}
		case "Edit", "Write", "MultiEdit", "NotebookEdit":
			p := firstField(toolArgs, "file_path", "notebook_path", "Path", "path")
			if p == "" {
				return ev, nil
			}
			p = absolutePath(p, cwd)
			if p == "" {
				return ev, nil
			}
			req.Op, req.Paths = proto.OpClaim, []string{p}
		case "apply_patch":
			patch := field(toolArgs, "command")
			if patch == "" {
				patch = field(toolArgs, "patch")
			}
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
					p := absolutePath(name, cwd)
					if p != "" {
						req.Paths = append(req.Paths, p)
					}
				}
			}
			if len(req.Paths) == 0 {
				return ev, nil
			}
			req.Op = proto.OpClaim
		default:
			return ev, nil
		}
	default:
		return ev, nil
	}

	ev.Req, ev.HasReq = req, true
	return ev, nil
}

func workingDirectory(in hookInput, args sonic.NoCopyRawMessage, tool string) string {
	if tool == "run_command" {
		if cwd := firstField(args, "Cwd", "cwd"); filepath.IsAbs(cwd) {
			return filepath.Clean(cwd)
		}
	}
	if filepath.IsAbs(in.CWD) {
		return filepath.Clean(in.CWD)
	}
	for _, p := range in.WorkspacePaths {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
	}
	return ""
}

func absolutePath(path, cwd string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if !filepath.IsAbs(cwd) {
		return ""
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

func firstField(raw sonic.NoCopyRawMessage, keys ...string) string {
	for _, key := range keys {
		if value := field(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func field(raw sonic.NoCopyRawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]sonic.NoCopyRawMessage
	if sonic.ConfigStd.Unmarshal(raw, &m) != nil {
		return ""
	}
	val, ok := m[key]
	if !ok || len(val) == 0 {
		return ""
	}
	var s string
	if sonic.ConfigStd.Unmarshal(val, &s) != nil {
		return ""
	}
	return s
}

type denyOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type sessionStartOutput struct {
	InjectSteps []injectedStep `json:"injectSteps"`
}

type injectedStep struct {
	SystemMessage systemMessage `json:"systemMessage"`
}

type systemMessage struct {
	SystemMessage string `json:"systemMessage"`
}

// Render turns the daemon's response into Antigravity hook stdout.
// A PreToolUse deny emits {"decision":"deny","reason":"..."}.
// A SessionStart with context injects a system message.
// All other cases return empty stdout (no opinion).
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
		var b bytes.Buffer
		enc := plainJSON.NewEncoder(&b)
		if enc.Encode(denyOutput{Decision: "deny", Reason: reason}) != nil {
			return Output{}
		}
		return Output{Stdout: b.Bytes()}
	case (ev.HookEvent == "SessionStart" || ev.HookEvent == "PreInvocation") && r.Context != "":
		var b bytes.Buffer
		enc := plainJSON.NewEncoder(&b)
		out := sessionStartOutput{
			InjectSteps: []injectedStep{
				{
					SystemMessage: systemMessage{SystemMessage: r.Context},
				},
			},
		}
		if enc.Encode(out) != nil {
			return Output{}
		}
		return Output{Stdout: b.Bytes()}
	}
	return Output{}
}
