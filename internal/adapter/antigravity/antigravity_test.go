package antigravity

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

const fixtureDir = "../../../testdata/hooks/antigravity"

func req(op string, mod func(*proto.Request)) *proto.Request {
	r := &proto.Request{
		V:       proto.Version,
		Op:      op,
		Agent:   Agent,
		Session: "ag-conv-001",
		CWD:     "/home/user/my-project",
	}
	if mod != nil {
		mod(r)
	}
	return r
}

func paths(p ...string) func(*proto.Request) { return func(r *proto.Request) { r.Paths = p } }
func command(c string) func(*proto.Request)  { return func(r *proto.Request) { r.Command = c } }

var golden = map[string]struct {
	event, tool string
	want        *proto.Request
}{
	"session_start.json":      {event: "SessionStart", tool: "", want: req(proto.OpHello, nil)},
	"stop.json":               {event: "Stop", tool: "", want: req(proto.OpBye, nil)},
	"stop_not_idle.json":      {event: "Stop", tool: "", want: nil},
	"pre_edit.json":           {event: "PreToolUse", tool: "replace_file_content", want: req(proto.OpClaim, paths("/home/user/my-project/internal/auth/token.go"))},
	"pre_edit_relative.json":  {event: "PreToolUse", tool: "multi_replace_file_content", want: req(proto.OpClaim, paths("/home/user/my-project/internal/auth/token.go"))},
	"pre_write.json":          {event: "PreToolUse", tool: "write_to_file", want: req(proto.OpClaim, paths("/home/user/my-project/docs/notes.md"))},
	"pre_command_git.json":    {event: "PreToolUse", tool: "run_command", want: req(proto.OpGuard, command("git stash && git pull --rebase"))},
	"pre_command_nongit.json": {event: "PreToolUse", tool: "run_command", want: req(proto.OpGuard, command("go test ./..."))},
	"pre_invocation.json":     {event: "PreInvocation", tool: "", want: req(proto.OpHello, func(r *proto.Request) { r.Note = "refactor token refresh in internal/auth" })},
	"pre_read.json":           {event: "PreToolUse", tool: "view_file", want: nil},
	"pre_edit_no_path.json":   {event: "PreToolUse", tool: "replace_file_content", want: nil},
}

func TestParseGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("Glob(%s) = %d files, err %v, want at least one fixture", fixtureDir, len(files), err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		name := filepath.Base(f)
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			g, ok := golden[name]
			if !ok {
				t.Fatalf("golden[%s] missing, want an entry for every fixture", name)
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			ev, err := Parse(raw)
			if err != nil {
				t.Fatalf("Parse(%s) err = %v, want nil", name, err)
			}
			if ev.HookEvent != g.event || ev.Tool != g.tool {
				t.Errorf("event/tool = %q/%q, want %q/%q", ev.HookEvent, ev.Tool, g.event, g.tool)
			}
			if g.want == nil {
				if ev.HasReq {
					t.Errorf("Parse(%s).HasReq = true (req %+v), want false", name, ev.Req)
				}
				return
			}
			if !ev.HasReq {
				t.Fatalf("Parse(%s).HasReq = false, want true with req %+v", name, *g.want)
			}
			if !reflect.DeepEqual(ev.Req, *g.want) {
				t.Errorf("Parse(%s).Req = %+v, want %+v", name, ev.Req, *g.want)
			}
			if ev.Req.PID != 0 {
				t.Errorf("Parse(%s).Req.PID = %d, want 0", name, ev.Req.PID)
			}
		})
	}
	for name := range golden {
		if !seen[name] {
			t.Errorf("fixture for golden[%s] missing, want a file in %s", name, fixtureDir)
		}
	}
}

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
			name:    "session start with sessionId",
			input:   `{"sessionId":"s1","cwd":"/repo","hookEventName":"SessionStart"}`,
			wantOp:  proto.OpHello,
			wantReq: true,
		},
		{
			name:    "session start with legacy session_id and hook_event_name",
			input:   `{"session_id":"s1","cwd":"/repo","hook_event_name":"SessionStart"}`,
			wantOp:  proto.OpHello,
			wantReq: true,
		},
		{
			name:        "run_command with CommandLine",
			input:       `{"conversationId":"s1","cwd":"/repo","hookEventName":"PreToolUse","toolCall":{"name":"run_command","args":{"CommandLine":"git status"}}}`,
			wantOp:      proto.OpGuard,
			wantCommand: "git status",
			wantReq:     true,
		},
		{
			name:        "run_command with fallback tool_name and tool_input",
			input:       `{"conversationId":"s1","cwd":"/repo","hookEventName":"PreToolUse","tool_name":"run_command","tool_input":{"command":"git diff"}}`,
			wantOp:      proto.OpGuard,
			wantCommand: "git diff",
			wantReq:     true,
		},
		{
			name:      "edit_file relative path joined with absolute CWD",
			input:     `{"conversationId":"s1","cwd":"/repo","hookEventName":"PreToolUse","toolCall":{"name":"edit_file","args":{"Path":"foo/bar.go"}}}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/foo/bar.go"},
			wantReq:   true,
		},
		{
			name:      "write_to_file TargetFile relative to workspace path",
			input:     `{"conversationId":"s1","workspacePaths":["/repo"],"hookEventName":"PreToolUse","toolCall":{"name":"write_to_file","args":{"TargetFile":"docs/new.txt"}}}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/docs/new.txt"},
			wantReq:   true,
		},
		{
			name:        "run_command uses command Cwd",
			input:       `{"conversationId":"s1","workspacePaths":["/other"],"hookEventName":"PreToolUse","toolCall":{"name":"run_command","args":{"CommandLine":"git status","Cwd":"/repo"}}}`,
			wantOp:      proto.OpGuard,
			wantCommand: "git status",
			wantReq:     true,
		},
		{
			name:  "edit_file relative path with non-absolute CWD",
			input: `{"conversationId":"s1","cwd":"repo","hookEventName":"PreToolUse","toolCall":{"name":"edit_file","args":{"Path":"foo/bar.go"}}}`,
		},
		{
			name:      "write_file with path field",
			input:     `{"conversationId":"s1","cwd":"/repo","hookEventName":"PreToolUse","toolCall":{"name":"write_file","args":{"path":"/repo/new.txt"}}}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/new.txt"},
			wantReq:   true,
		},
		{
			name:      "apply_patch support",
			input:     `{"conversationId":"s1","cwd":"/repo","hookEventName":"PreToolUse","toolCall":{"name":"apply_patch","args":{"command":"*** Update File: a.go\n*** Move to: b.go\n"}}}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/a.go", "/repo/b.go"},
			wantReq:   true,
		},
		{
			name:     "prompt note from UserPromptSubmit",
			input:    `{"conversationId":"s1","cwd":"/repo","hookEventName":"UserPromptSubmit","prompt":"run tests"}`,
			wantOp:   proto.OpHello,
			wantNote: "run tests",
			wantReq:  true,
		},
		{
			name:  "missing session",
			input: `{"cwd":"/repo","hookEventName":"SessionStart"}`,
		},
		{
			name:  "unknown tool",
			input: `{"conversationId":"s1","cwd":"/repo","hookEventName":"PreToolUse","toolCall":{"name":"unknown_tool","args":{}}}`,
		},
		{
			name:  "run_command with empty CommandLine",
			input: `{"conversationId":"s1","cwd":"/repo","hookEventName":"PreToolUse","toolCall":{"name":"run_command","args":{"CommandLine":""}}}`,
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
			name:     "deny with custom reason",
			event:    Event{HookEvent: "PreToolUse"},
			response: proto.Response{OK: true, Decision: proto.Deny, Reason: "held by peer"},
			want:     `{"decision":"deny","reason":"held by peer"}` + "\n",
		},
		{
			name:     "deny with generic reason",
			event:    Event{HookEvent: "PreToolUse"},
			response: proto.Response{OK: true, Decision: proto.Deny},
			want:     `{"decision":"deny","reason":"` + genericDeny + `"}` + "\n",
		},
		{
			name:     "session start context",
			event:    Event{HookEvent: "SessionStart"},
			response: proto.Response{OK: true, Context: "welcome to quorum"},
			want:     `{"injectSteps":[{"systemMessage":{"systemMessage":"welcome to quorum"}}]}` + "\n",
		},
		{
			name:     "pre-invocation refreshes peer context",
			event:    Event{HookEvent: "PreInvocation"},
			response: proto.Response{OK: true, Context: "another session is active"},
			want:     `{"injectSteps":[{"systemMessage":{"systemMessage":"another session is active"}}]}` + "\n",
		},
		{
			name:     "allow pre tool use emits empty stdout",
			event:    Event{HookEvent: "PreToolUse"},
			response: proto.Response{OK: true, Decision: proto.Grant},
			want:     "",
		},
		{
			name:     "session start empty context emits empty stdout",
			event:    Event{HookEvent: "SessionStart"},
			response: proto.Response{OK: true, Context: ""},
			want:     "",
		},
		{
			name:     "error response emits empty stdout",
			event:    Event{HookEvent: "PreToolUse"},
			response: proto.Response{OK: false, Decision: proto.Deny},
			want:     "",
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
