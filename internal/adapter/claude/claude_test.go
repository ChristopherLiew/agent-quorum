package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

const fixtureDir = "../../../testdata/hooks/claude"

func req(op string, mod func(*proto.Request)) *proto.Request {
	r := &proto.Request{V: proto.Version, Op: op, Agent: "claude", Session: "abc123", CWD: "/home/user/my-project"}
	if mod != nil {
		mod(r)
	}
	return r
}

func paths(p ...string) func(*proto.Request) { return func(r *proto.Request) { r.Paths = p } }
func command(c string) func(*proto.Request)  { return func(r *proto.Request) { r.Command = c } }

// golden maps every fixture to its expected request; nil means no opinion.
var golden = map[string]struct {
	event, tool string
	want        *proto.Request
}{
	"session_start.json":      {event: "SessionStart", tool: "", want: req(proto.OpHello, nil)},
	"session_end.json":        {event: "SessionEnd", tool: "", want: req(proto.OpBye, nil)},
	"pre_edit.json":           {event: "PreToolUse", tool: "Edit", want: req(proto.OpClaim, paths("/home/user/my-project/internal/auth/token.go"))},
	"pre_edit_subagent.json":  {event: "PreToolUse", tool: "Edit", want: req(proto.OpClaim, paths("/home/user/my-project/internal/auth/token.go"))},
	"pre_write.json":          {event: "PreToolUse", tool: "Write", want: req(proto.OpClaim, paths("/home/user/my-project/docs/notes.md"))},
	"pre_notebook_edit.json":  {event: "PreToolUse", tool: "NotebookEdit", want: req(proto.OpClaim, paths("/home/user/my-project/analysis/explore.ipynb"))},
	"pre_bash_git.json":       {event: "PreToolUse", tool: "Bash", want: req(proto.OpGuard, command("git stash && git pull --rebase"))},
	"pre_bash_nongit.json":    {event: "PreToolUse", tool: "Bash", want: req(proto.OpGuard, command("go test ./..."))},
	"user_prompt_submit.json": {event: "UserPromptSubmit", tool: "", want: req(proto.OpNote, func(r *proto.Request) { r.Note = "refactor token refresh in internal/auth" })},
	"pre_read.json":           {event: "PreToolUse", tool: "Read", want: nil},
	"pre_edit_no_path.json":   {event: "PreToolUse", tool: "Edit", want: nil},
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

const common = `"session_id":"s1","cwd":"/repo","hook_event_name":`

func TestParseNoOpinion(t *testing.T) {
	cases := map[string]string{
		"unknown event":        `{` + common + `"Stop"}`,
		"post tool use":        `{` + common + `"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"/repo/a.go"}}`,
		"unknown tool":         `{` + common + `"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/repo/a.go"}}`,
		"missing file_path":    `{` + common + `"PreToolUse","tool_name":"Edit","tool_input":{"old_string":"a"}}`,
		"empty file_path":      `{` + common + `"PreToolUse","tool_name":"Write","tool_input":{"file_path":""}}`,
		"relative file_path":   `{` + common + `"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"internal/a.go"}}`,
		"relative notebook":    `{` + common + `"PreToolUse","tool_name":"NotebookEdit","tool_input":{"notebook_path":"nb.ipynb"}}`,
		"non-string file_path": `{` + common + `"PreToolUse","tool_name":"Edit","tool_input":{"file_path":42}}`,
		"missing tool_input":   `{` + common + `"PreToolUse","tool_name":"Edit"}`,
		"tool_input not obj":   `{` + common + `"PreToolUse","tool_name":"Edit","tool_input":"x"}`,
		"empty command":        `{` + common + `"PreToolUse","tool_name":"Bash","tool_input":{"command":""}}`,
		"missing command":      `{` + common + `"PreToolUse","tool_name":"Bash","tool_input":{}}`,
		"empty prompt":         `{` + common + `"UserPromptSubmit","prompt":""}`,
		"missing session_id":   `{"cwd":"/repo","hook_event_name":"SessionStart","source":"startup"}`,
		"empty session_id":     `{"session_id":"","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/repo/a.go"}}`,
		"empty object":         `{}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			ev, err := Parse([]byte(raw))
			if err != nil {
				t.Fatalf("Parse(%q) err = %v, want nil", raw, err)
			}
			if ev.HasReq {
				t.Errorf("Parse(%q).HasReq = true (req %+v), want false", raw, ev.Req)
			}
			if out := Render(ev, proto.Response{OK: true, Decision: proto.Grant}); len(out.Stdout) != 0 || out.Exit != 0 {
				t.Errorf("Render(no opinion) = %q exit %d, want empty and 0", out.Stdout, out.Exit)
			}
		})
	}
}

func TestParseVariants(t *testing.T) {
	// MultiEdit claims like Edit; file_path wins over notebook_path.
	ev, err := Parse([]byte(`{` + common + `"PreToolUse","tool_name":"MultiEdit","tool_input":{"file_path":"/repo/a.go","notebook_path":"/repo/b.ipynb"}}`))
	if err != nil || !ev.HasReq || ev.Req.Op != proto.OpClaim || !reflect.DeepEqual(ev.Req.Paths, []string{"/repo/a.go"}) {
		t.Errorf("Parse(MultiEdit) = %+v, %v, want claim of [/repo/a.go] and nil error", ev, err)
	}
	// Every SessionStart source is a hello.
	for _, src := range []string{"startup", "resume", "clear", "compact"} {
		ev, err := Parse([]byte(`{` + common + `"SessionStart","source":"` + src + `"}`))
		if err != nil || !ev.HasReq || ev.Req.Op != proto.OpHello {
			t.Errorf("Parse(SessionStart %s) = %+v, %v, want hello and nil error", src, ev, err)
		}
	}
	// The prompt is passed through untruncated.
	long := strings.Repeat("x", 10000)
	ev, err = Parse([]byte(`{` + common + `"UserPromptSubmit","prompt":"` + long + `"}`))
	if err != nil || !ev.HasReq || ev.Req.Note != long {
		t.Errorf("Parse(long prompt) = err %v, note len %d, want nil and %d", err, len(ev.Req.Note), len(long))
	}
}

func TestParseErrors(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":      "",
		"whitespace": "  \n",
		"truncated":  `{"session_id":"abc`,
		"not json":   "hello",
		"array":      `[1,2]`,
		"wrong type": `{"session_id":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			ev, err := Parse([]byte(raw))
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, want error", raw, ev)
			}
			if ev.HasReq {
				t.Errorf("Parse(%q).HasReq = true, want false on error", raw)
			}
		})
	}
}

var (
	preEdit = Event{HookEvent: "PreToolUse", Tool: "Edit", HasReq: true}
	preBash = Event{HookEvent: "PreToolUse", Tool: "Bash", HasReq: true}
	start   = Event{HookEvent: "SessionStart", HasReq: true}
	end     = Event{HookEvent: "SessionEnd", HasReq: true}
	prompt  = Event{HookEvent: "UserPromptSubmit", HasReq: true}
)

func TestRenderGolden(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		r    proto.Response
		want string
	}{
		{name: "grant", ev: preEdit, r: proto.Response{OK: true, Decision: proto.Grant}, want: ""},
		{name: "grant with stray reason", ev: preEdit, r: proto.Response{OK: true, Decision: proto.Grant, Reason: "x", Context: "y"}, want: ""},
		{name: "no decision", ev: preEdit, r: proto.Response{OK: true}, want: ""},
		{name: "deny", ev: preEdit, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "agent-quorum: /repo/auth.go is being edited by another agent session (claude:9f2c)."},
			want: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"agent-quorum: /repo/auth.go is being edited by another agent session (claude:9f2c)."}}` + "\n"},
		{name: "deny guard", ev: preBash, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "agent-quorum: do not stash."},
			want: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"agent-quorum: do not stash."}}` + "\n"},
		{name: "deny html chars", ev: preEdit, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "a <b> & c"},
			want: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"a <b> & c"}}` + "\n"},
		{name: "deny empty reason", ev: preEdit, r: proto.Response{OK: true, Decision: proto.Deny},
			want: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"agent-quorum: denied because another agent session holds this file."}}` + "\n"},
		{name: "deny ignores context", ev: preEdit, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "r", Context: "c"},
			want: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"r"}}` + "\n"},
		{name: "deny but not ok", ev: preEdit, r: proto.Response{OK: false, Error: "boom", Decision: proto.Deny, Reason: "r"}, want: ""},
		{name: "not ok", ev: preEdit, r: proto.Response{OK: false, Error: "unknown op"}, want: ""},
		{name: "hello with context", ev: start, r: proto.Response{OK: true, Context: "agent-quorum: 1 other session <claude:1a7e> is active."},
			want: `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"agent-quorum: 1 other session <claude:1a7e> is active."}}` + "\n"},
		{name: "hello without context", ev: start, r: proto.Response{OK: true}, want: ""},
		{name: "hello not ok", ev: start, r: proto.Response{OK: false, Context: "c"}, want: ""},
		{name: "hello with deny", ev: start, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "r"}, want: ""},
		{name: "bye", ev: end, r: proto.Response{OK: true}, want: ""},
		{name: "bye with deny and context", ev: end, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "r", Context: "c"}, want: ""},
		{name: "note", ev: prompt, r: proto.Response{OK: true}, want: ""},
		{name: "note with deny and context", ev: prompt, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "r", Context: "c"}, want: ""},
		{name: "unknown event deny", ev: Event{HookEvent: "PostToolUse"}, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "r", Context: "c"}, want: ""},
		{name: "zero event", ev: Event{}, r: proto.Response{OK: true, Decision: proto.Deny, Reason: "r", Context: "c"}, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := Render(c.ev, c.r)
			if string(out.Stdout) != c.want {
				t.Errorf("Render(%s).Stdout = %q, want %q", c.name, out.Stdout, c.want)
			}
			if out.Exit != 0 {
				t.Errorf("Render(%s).Exit = %d, want 0", c.name, out.Exit)
			}
		})
	}
}

type rendered struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
		AdditionalContext        string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func assertOneLine(t *testing.T, b []byte) {
	t.Helper()
	if !bytes.HasSuffix(b, []byte("\n")) || bytes.Count(b, []byte("\n")) != 1 || bytes.ContainsRune(b, '\r') {
		t.Errorf("output = %q, want exactly one newline-terminated line", b)
	}
	if !sonic.ConfigStd.Valid(b) {
		t.Errorf("Valid(%q) = false, want true", b)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	nasty := "agent-quorum: \"auth.go\" is held by <claude:9f2c> & others.\nDo not edit it;\r\n\tuse 'other' work \\   é </script>"

	out := Render(preEdit, proto.Response{OK: true, Decision: proto.Deny, Reason: nasty})
	assertOneLine(t, out.Stdout)
	var got rendered
	if err := sonic.ConfigStd.Unmarshal(out.Stdout, &got); err != nil {
		t.Fatal(err)
	}
	h := got.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "deny" || h.PermissionDecisionReason != nasty || h.AdditionalContext != "" {
		t.Errorf("deny round trip = %+v, want PreToolUse deny with the original reason and no context", h)
	}
	for _, raw := range []string{"<claude:9f2c>", "&", "</script>"} {
		if !bytes.Contains(out.Stdout, []byte(raw)) {
			t.Errorf("Render(deny) = %q, want unescaped %q", out.Stdout, raw)
		}
	}

	out = Render(start, proto.Response{OK: true, Context: nasty})
	assertOneLine(t, out.Stdout)
	got = rendered{}
	if err := sonic.ConfigStd.Unmarshal(out.Stdout, &got); err != nil {
		t.Fatal(err)
	}
	h = got.HookSpecificOutput
	if h.HookEventName != "SessionStart" || h.AdditionalContext != nasty || h.PermissionDecision != "" || h.PermissionDecisionReason != "" {
		t.Errorf("context round trip = %+v, want SessionStart with the original context and no decision", h)
	}
	if bytes.Contains(out.Stdout, []byte("permissionDecision")) {
		t.Errorf("Render(SessionStart) = %q, want no permissionDecision", out.Stdout)
	}
}

// Printing "allow" would skip the user's permission prompt, and "ask" would
// force one. Neither may ever be emitted, whatever the daemon says.
func TestRenderNeverAllows(t *testing.T) {
	events := []Event{{}, preEdit, preBash, start, end, prompt,
		{HookEvent: "PreToolUse", Tool: "Write"}, {HookEvent: "PreToolUse", Tool: "Read"},
		{HookEvent: "PostToolUse"}, {HookEvent: "PermissionRequest"}, {HookEvent: "Stop"}}
	decisions := []string{"", proto.Grant, proto.Deny, "allow", "ask", "defer", "ALLOW", "bogus"}
	texts := []string{"", "held by claude:1a7e"}
	for _, ev := range events {
		for _, ok := range []bool{true, false} {
			for _, d := range decisions {
				for _, reason := range texts {
					for _, ctx := range texts {
						r := proto.Response{OK: ok, Error: "e", Decision: d, Reason: reason, Context: ctx}
						out := Render(ev, r)
						if out.Exit != 0 {
							t.Errorf("Render(%+v, %+v).Exit = %d, want 0", ev, r, out.Exit)
						}
						s := string(out.Stdout)
						if strings.Contains(strings.ToLower(s), "allow") || strings.Contains(s, `"ask"`) {
							t.Errorf("Render(%+v, %+v) = %q, want no allow or ask", ev, r, s)
						}
						if len(out.Stdout) == 0 {
							continue
						}
						var got rendered
						if err := sonic.ConfigStd.Unmarshal(out.Stdout, &got); err != nil {
							t.Errorf("Unmarshal(%q) err = %v, want nil", s, err)
						}
						if pd := got.HookSpecificOutput.PermissionDecision; pd != "" && pd != "deny" {
							t.Errorf("Render(%+v, %+v) permissionDecision = %q, want empty or deny", ev, r, pd)
						}
						if d != proto.Deny && strings.Contains(s, "permissionDecision") {
							t.Errorf("Render(decision %q) = %q, want no permissionDecision", d, s)
						}
					}
				}
			}
		}
	}
}
