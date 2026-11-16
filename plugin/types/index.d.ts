// Holder mirrors proto.Holder (internal/proto/proto.go): one live session as
// `agent-quorum status --json` prints it. Keep the two in step.
export type Holder = {
  session: string
  agent?: string
  pid?: number
  cwd?: string
  repo?: string
  note?: string
  idle_s: number
  paths?: string[]
}

// Snap is the last status poll: daemon up or not, its sessions, and the
// error of the last failed poll ('' when it succeeded).
export type Snap = { daemon: boolean; sessions: Holder[]; error: string }

declare module 'claude-code' {
  interface PluginState {
    // expanded: the sessions whose file tree is open.
    'agent-quorum': { snap: Shaped<Snap>; selected: string; expanded: string[] }
  }
}
