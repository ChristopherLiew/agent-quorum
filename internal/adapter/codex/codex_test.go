package codex

import (
	"bytes"
	"testing"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantOp      string
		wantPaths   []string
		wantCommand string
		wantNote    string
		wantReq     bool
	}{
		{
			name:    "session start",
			input:   `{"session_id":"s1","cwd":"/repo","hook_event_name":"SessionStart"}`,
			wantOp:  proto.OpHello,
			wantReq: true,
		},
		{
			name:        "bash command",
			input:       `{"session_id":"s1","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git reset --hard"}}`,
			wantOp:      proto.OpGuard,
			wantCommand: "git reset --hard",
			wantReq:     true,
		},
		{
			name:      "relative patch and move",
			input:     `{"session_id":"s1","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: old.go\n*** Move to: new.go\n*** End Patch"}}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/old.go", "/repo/new.go"},
			wantReq:   true,
		},
		{
			name:     "prompt note",
			input:    `{"session_id":"s1","cwd":"/repo","hook_event_name":"UserPromptSubmit","prompt":"fix the bug"}`,
			wantOp:   proto.OpNote,
			wantNote: "fix the bug",
			wantReq:  true,
		},
		{
			name:  "unrecognized tool",
			input: `{"session_id":"s1","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"Read"}`,
		},
		{
			name:  "relative path without absolute cwd",
			input: `{"session_id":"s1","cwd":"repo","hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: old.go\n*** End Patch"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("Parse() error = %v, want nil", err)
			}
			if got := ev.HasReq; got != tt.wantReq {
				t.Fatalf("HasReq = %t, want %t", got, tt.wantReq)
			}
			if !tt.wantReq {
				return
			}
			if got := ev.Req.Agent; got != Agent {
				t.Errorf("Agent = %q, want %q", got, Agent)
			}
			if got := ev.Req.Op; got != tt.wantOp {
				t.Errorf("Op = %q, want %q", got, tt.wantOp)
			}
			if got := ev.Req.Paths; !equal(got, tt.wantPaths) {
				t.Errorf("Paths = %q, want %q", got, tt.wantPaths)
			}
			if got := ev.Req.Command; got != tt.wantCommand {
				t.Errorf("Command = %q, want %q", got, tt.wantCommand)
			}
			if got := ev.Req.Note; got != tt.wantNote {
				t.Errorf("Note = %q, want %q", got, tt.wantNote)
			}
		})
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte("{")); err == nil {
		t.Fatal("Parse() error = nil, want non-nil")
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name     string
		event    Event
		response proto.Response
		want     string
	}{
		{
			name:     "deny",
			event:    Event{HookEvent: "PreToolUse"},
			response: proto.Response{OK: true, Decision: proto.Deny, Reason: "file is held"},
			want:     `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"file is held"}}` + "\n",
		},
		{
			name:     "session context",
			event:    Event{HookEvent: "SessionStart"},
			response: proto.Response{OK: true, Context: "another session is active"},
			want:     `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"another session is active"}}` + "\n",
		},
		{
			name:     "no opinion for error",
			event:    Event{HookEvent: "PreToolUse"},
			response: proto.Response{OK: false, Decision: proto.Deny},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Render(tt.event, tt.response).Stdout
			if !bytes.Equal(got, []byte(tt.want)) {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
