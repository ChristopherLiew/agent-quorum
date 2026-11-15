// Package proto defines the agent-neutral wire protocol between the hook/CLI
// client and the daemon: one JSON object per line each way, one request per
// connection. See docs/DESIGN.md section 8.
package proto

// Version is the wire protocol version. Bump only for incompatible changes.
const Version = 1

// The Op values name the operations a Request can ask for.
const (
	OpHello    = "hello"    // SessionStart: register session, learn about peers
	OpClaim    = "claim"    // before an edit: take or refresh claims on Paths
	OpGuard    = "guard"    // before a shell command: check Command for risky git verbs
	OpNote     = "note"     // record what the session is working on
	OpBye      = "bye"      // SessionEnd: drop the session and its claims
	OpRelease  = "release"  // CLI: drop claims of Session (Paths, or All)
	OpCheck    = "check"    // pre-commit: which of Paths are held by a session other than Session
	OpStatus   = "status"   // all live sessions and their valid claims
	OpShutdown = "shutdown" // handled by the daemon itself, never reaches core
)

// Grant and Deny are the values of Response.Decision.
const (
	Grant = "grant"
	Deny  = "deny"
)

// Request is sent by the client. Session is the agent's raw session id; the
// core derives the owner id "<Agent>:<Session>". When Agent is empty, Session
// is taken to be a full owner id already (used by the CLI).
type Request struct {
	V       int      `json:"v"`  // protocol version, see Version
	Op      string   `json:"op"` // one of the Op constants
	Agent   string   `json:"agent,omitempty"`
	Session string   `json:"session,omitempty"`
	PID     int      `json:"pid,omitempty"` // pid of the agent process, 0 if unknown
	CWD     string   `json:"cwd,omitempty"`
	Paths   []string `json:"paths,omitempty"` // absolute, already symlink-resolved by the client
	All     bool     `json:"all,omitempty"`
	Command string   `json:"command,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// Holder describes a session and (some of) the paths it holds.
type Holder struct {
	Session string   `json:"session"` // full owner id, "<agent>:<id>"
	Agent   string   `json:"agent,omitempty"`
	PID     int      `json:"pid,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
	Repo    string   `json:"repo,omitempty"`
	Note    string   `json:"note,omitempty"`
	IdleS   int      `json:"idle_s"` // seconds since the session was last seen
	Paths   []string `json:"paths,omitempty"`
}

// Response is the daemon's answer.
type Response struct {
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`    // set when OK is false
	Decision string   `json:"decision,omitempty"` // Grant or Deny, for claim/guard/check
	Reason   string   `json:"reason,omitempty"`   // on Deny: instruction text written for the model
	Context  string   `json:"context,omitempty"`  // on hello with peers: text to inject into the new session
	Holders  []Holder `json:"holders,omitempty"`  // on Deny: who holds what
	Peers    []Holder `json:"peers,omitempty"`    // on hello: other live sessions in the same repo
	Sessions []Holder `json:"sessions,omitempty"` // on status: every live session
}
