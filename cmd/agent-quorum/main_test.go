package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("got %q, want %q", out.String(), version)
	}
}

// The hook path must never block an agent while unimplemented or broken.
func TestHookFailsOpen(t *testing.T) {
	var out, errb bytes.Buffer
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Edit"}`)
	if code := run([]string{"hook", "--agent", "claude"}, in, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty when there is no decision, got %q", out.String())
	}
}
