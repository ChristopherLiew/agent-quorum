package opencode

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/ChristopherLiew/agent-quorum/internal/proto"
)

const fixtureDir = "../../../testdata/hooks/opencode"

func req(op string, mod func(*proto.Request)) *proto.Request {
	r := &proto.Request{
		V:       proto.Version,
		Op:      op,
		Agent:   Agent,
		Session: "ses_root01",
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
	kind, action string
	pid          int
	want         *proto.Request
}{
	"prompt.json":                    {kind: KindPrompt, pid: 4242, want: req(proto.OpHello, func(r *proto.Request) { r.Note = "refactor token refresh in internal/auth" })},
	"prompt_empty.json":              {kind: KindPrompt, want: nil},
	"permission_edit.json":           {kind: KindPermission, action: "edit", pid: 4242, want: req(proto.OpClaim, paths("/home/user/my-project/internal/auth/token.go"))},
	"permission_edit_relative.json":  {kind: KindPermission, action: "edit", want: req(proto.OpClaim, paths("/home/user/my-project/internal/auth/token.go"))},
	"permission_patch_multi.json":    {kind: KindPermission, action: "edit", want: req(proto.OpClaim, paths("/home/user/my-project/internal/auth/token.go", "/home/user/my-project/docs/notes.md"))},
	"permission_shell_compound.json": {kind: KindPermission, action: "shell", want: req(proto.OpGuard, command("cd sub\ngit stash"))},
	"permission_shell_nongit.json":   {kind: KindPermission, action: "shell", want: req(proto.OpGuard, command("go test ./..."))},
	"permission_read.json":           {kind: KindPermission, action: "read", want: nil},
	"end.json":                       {kind: KindEnd, want: req(proto.OpBye, nil)},
	"no_session.json":                {kind: KindPermission, action: "edit", want: nil},
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
			if ev.Kind != g.kind || ev.Action != g.action {
				t.Errorf("Parse(%s) kind/action = %q/%q, want %q/%q", name, ev.Kind, ev.Action, g.kind, g.action)
			}
			if ev.PID != g.pid {
				t.Errorf("Parse(%s).PID = %d, want %d", name, ev.PID, g.pid)
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
		wantCWD     string
	}{
		{
			name:        "legacy bash action is guarded",
			input:       `{"event":"permission","session_id":"s1","directory":"/repo","action":"bash","resources":["git add -A"]}`,
			wantOp:      proto.OpGuard,
			wantCommand: "git add -A",
			wantCWD:     "/repo",
		},
		{
			name:      "write action claims",
			input:     `{"event":"permission","session_id":"s1","directory":"/repo","action":"write","resources":["new.txt"]}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/new.txt"},
			wantCWD:   "/repo",
		},
		{
			name:      "patterns among resources are skipped",
			input:     `{"event":"permission","session_id":"s1","directory":"/repo","action":"edit","resources":["src/*","a.go"]}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/a.go"},
			wantCWD:   "/repo",
		},
		{
			name:      "absolute path claimed without a directory",
			input:     `{"event":"permission","session_id":"s1","action":"edit","resources":["/repo/./a.go"]}`,
			wantOp:    proto.OpClaim,
			wantPaths: []string{"/repo/a.go"},
		},
		{
			name:  "relative path without a directory is no opinion",
			input: `{"event":"permission","session_id":"s1","directory":"repo","action":"edit","resources":["a.go"]}`,
		},
		{
			name:  "blank shell resources are no opinion",
			input: `{"event":"permission","session_id":"s1","directory":"/repo","action":"shell","resources":[" ",""]}`,
		},
		{
			name:  "unknown event is no opinion",
			input: `{"event":"idle","session_id":"s1","directory":"/repo"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("Parse() err = %v, want nil", err)
			}
			if got, want := ev.HasReq, tt.wantOp != ""; got != want {
				t.Fatalf("Parse().HasReq = %t, want %t", got, want)
			}
			if !ev.HasReq {
				return
			}
			if got := ev.Req.Op; got != tt.wantOp {
				t.Errorf("Parse().Req.Op = %q, want %q", got, tt.wantOp)
			}
			if got := ev.Req.Paths; !slices.Equal(got, tt.wantPaths) {
				t.Errorf("Parse().Req.Paths = %q, want %q", got, tt.wantPaths)
			}
			if got := ev.Req.Command; got != tt.wantCommand {
				t.Errorf("Parse().Req.Command = %q, want %q", got, tt.wantCommand)
			}
			if got := ev.Req.CWD; got != tt.wantCWD {
				t.Errorf("Parse().Req.CWD = %q, want %q", got, tt.wantCWD)
			}
		})
	}
}

func TestParseNegativePIDIsUnknown(t *testing.T) {
	ev, err := Parse([]byte(`{"event":"end","session_id":"s1","pid":-1}`))
	if err != nil {
		t.Fatalf("Parse() err = %v, want nil", err)
	}
	if ev.PID != 0 {
		t.Errorf("Parse().PID = %d, want 0", ev.PID)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte("{")); err == nil {
		t.Error("Parse(\"{\") err = nil, want non-nil")
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
			name:     "permission deny with reason",
			event:    Event{Kind: KindPermission},
			response: proto.Response{OK: true, Decision: proto.Deny, Reason: "held by <peer> & co"},
			want:     `{"deny":"held by <peer> & co"}` + "\n",
		},
		{
			name:     "permission deny without reason",
			event:    Event{Kind: KindPermission},
			response: proto.Response{OK: true, Decision: proto.Deny},
			want:     `{"deny":"` + genericDeny + `"}` + "\n",
		},
		{
			name:     "permission grant is no opinion",
			event:    Event{Kind: KindPermission},
			response: proto.Response{OK: true, Decision: proto.Grant},
		},
		{
			name:     "prompt with peer context",
			event:    Event{Kind: KindPrompt},
			response: proto.Response{OK: true, Context: "another session is active"},
			want:     `{"context":"another session is active"}` + "\n",
		},
		{
			name:     "prompt without peers is no opinion",
			event:    Event{Kind: KindPrompt},
			response: proto.Response{OK: true},
		},
		{
			name:     "deny on a prompt is ignored",
			event:    Event{Kind: KindPrompt},
			response: proto.Response{OK: true, Decision: proto.Deny, Reason: "x"},
		},
		{
			name:     "error response is no opinion",
			event:    Event{Kind: KindPermission},
			response: proto.Response{OK: false, Decision: proto.Deny},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Render(tt.event, tt.response).Stdout); got != tt.want {
				t.Errorf("Render(%+v, %+v) = %q, want %q", tt.event, tt.response, got, tt.want)
			}
		})
	}
}
