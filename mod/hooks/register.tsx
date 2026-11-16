import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Holder, Snap } from '../types'

const PANE = 'quorum'
const POLL_MS = 2000

// Change the shape tag when Snap's fields change: a reload keeps $.state.
const snap = atom(
  { plugin: 'agent-quorum-dashboard', key: 'snap' } as const,
  { daemon: false, sessions: [], error: '' } as Snap,
  { shape: 'snap-1' },
)
const selected = atom({ plugin: 'agent-quorum-dashboard', key: 'selected' } as const, '')

const agentColor: Record<string, string> = {
  claude: 'text-accent',
  codex: 'text-pro',
  opencode: 'text-warning',
  antigravity: 'text-success',
}

// Module state: a reload resets it, and session.start sets it again.
let bin = 'agent-quorum'
let self = ''
let isPolling = false

// refresh runs `status --json` once and stores the result. It never throws:
// a failure is kept in snap.error and drawn in the pane.
async function refresh($: EngineInterface): Promise<void> {
  if (isPolling) return
  isPolling = true
  try {
    const run = await $.process.run([bin, 'status', '--json'], { timeoutMs: POLL_MS })
    if (run.exitCode !== 0) throw new Error(run.stderr.trim() || `exit ${run.exitCode}`)
    const out = JSON.parse(run.stdout) as { daemon: boolean; sessions?: Holder[] }
    await update($, snap, () => ({ daemon: out.daemon, sessions: out.sessions ?? [], error: '' }))
  } catch (err) {
    const error = err instanceof Error ? err.message : String(err)
    await update($, snap, last => ({ ...last, error }))
  } finally {
    isPolling = false
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.command.register({ name: 'quorum', description: 'Show live agent-quorum sessions in a pane' })
    self = `claude:${await $.session.id()}`
    bin = await findBin($)
    // ponytail: polls only while the pane is shown; no history is kept.
    $.clock.every(POLL_MS, async () => {
      const panes = await $.ui.panes()
      if (panes.some(pane => pane.id === PANE && pane.isShown)) await refresh($)
    })
    return next(e)
  })

  on('command.run', { command: 'quorum' }, async $ => {
    await $.ui.open({ id: PANE, title: 'agent-quorum', focus: true, closeOnEscape: true })
    await refresh($)
    return { text: 'agent-quorum pane opened.' }
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Text, Button } = $.ui.resolve(e)
    const s = await read($, snap)
    const rows = sortRows(s.sessions)
    const sel = pick(rows, await read($, selected), self)
    const isWide = e.props.bodyColumns >= 80
    const files = rows.reduce((n, r) => n + (r.paths?.length ?? 0), 0)

    const move = async (step: number) => {
      const now = sortRows((await read($, snap)).sessions)
      const cur = pick(now, await read($, selected), self)
      const at = now.findIndex(r => r.session === cur?.session)
      const to = now[Math.min(Math.max(at + step, 0), now.length - 1)]
      if (to) await update($, selected, () => to.session)
    }

    const release = async () => {
      if (!sel) return
      if (sel.session === self) {
        $.ui.toast('That is this session.')
        return
      }
      let answer = ''
      try {
        answer = await $.ui.ask(`Release every file ${sel.session} holds?`, ['Release', 'Cancel'])
      } catch {
        return // dismissed
      }
      if (answer !== 'Release') return
      const run = await $.process.run([bin, 'release', '--session', sel.session, '--all'])
      $.ui.toast(run.exitCode === 0 ? `Released ${sel.session}.` : `Release failed: ${run.stderr.trim()}`)
      await refresh($)
    }

    const cell = (text: string, width: number, color?: string, isRight = false) => (
      <Box width={width} flexShrink={0}>
        <Text color={color} wrap="truncate-end">
          {isRight ? text.padStart(width - 1) : text}
        </Text>
      </Box>
    )

    const row = (r: Holder) => {
      const isSel = r.session === sel?.session
      const fg = isSel ? 'on-accent' : undefined
      const agent = r.agent ?? r.session.split(':')[0] ?? ''
      return (
        <Box key={r.session} flexDirection="row" backgroundColor={isSel ? 'fill-accent' : undefined}>
          {isWide && cell(r.pid ? String(r.pid) : '-', 7, fg ?? 'text-muted', true)}
          {cell(agent + (r.session === self ? '*' : ''), 12, fg ?? agentColor[agent])}
          {cell(idle(r.idle_s), 6, fg ?? idleColor(r.idle_s), true)}
          {cell(String(r.paths?.length ?? 0), 7, fg ?? (r.paths?.length ? undefined : 'text-muted'), true)}
          {isWide && cell(base(r.repo || r.cwd || ''), 16, fg ?? 'text-muted')}
          <Box flexGrow={1}>
            <Text color={fg ?? 'text-secondary'} wrap="truncate-end">
              {r.note ?? ''}
            </Text>
          </Box>
        </Box>
      )
    }

    return (
      <Box flexDirection="column" borderStyle="round" borderColor="border" paddingX={1}>
        <Box flexDirection="row" justifyContent="space-between">
          <Text>
            <Text bold>agent-quorum</Text>
            <Text color="text-muted">
              {'   '}
              {rows.length} sessions · {files} files
            </Text>
          </Text>
          <Text color={s.daemon ? 'text-success' : 'text-danger'}>{s.daemon ? '● daemon up' : '○ daemon down'}</Text>
        </Box>
        {s.error !== '' && <Text color="text-danger">! {s.error}</Text>}
        <Text> </Text>
        {rows.length === 0 ? (
          <Text color="text-muted">{s.daemon ? 'No live sessions.' : 'The daemon starts with the first agent session.'}</Text>
        ) : (
          <Box flexDirection="column">
            <Box flexDirection="row">
              {isWide && cell('PID', 7, 'text-muted', true)}
              {cell('AGENT', 12, 'text-muted')}
              {cell('IDLE', 6, 'text-muted', true)}
              {cell('FILES', 7, 'text-muted', true)}
              {isWide && cell('REPO', 16, 'text-muted')}
              <Text color="text-muted">LAST PROMPT</Text>
            </Box>
            {rows.map(row)}
          </Box>
        )}
        {sel && (
          <Box flexDirection="column" marginTop={1}>
            <Text>
              <Text color={agentColor[sel.agent ?? '']} bold>
                {sel.session}
              </Text>
              <Text color="text-muted">
                {'  '}
                {sel.cwd ?? ''}
              </Text>
            </Text>
            {(sel.paths ?? []).length === 0 && <Text color="text-muted">  holds no files</Text>}
            {(sel.paths ?? []).map(p => (
              <Text>  {rel(p, sel.repo)}</Text>
            ))}
          </Box>
        )}
        <Box flexDirection="row" gap={2} marginTop={1}>
          <Button key="down" hotkey="j" plain label="down" onPress={() => move(1)} />
          <Button key="up" hotkey="k" plain label="up" onPress={() => move(-1)} />
          <Button key="release" hotkey="r" plain label="release files" dimColor={!sel || sel.session === self} onPress={release} />
          <Button
            key="copy"
            hotkey="c"
            plain
            label="copy id"
            dimColor={!sel}
            onPress={press => {
              if (sel) void $.ui.copy({ text: sel.session, surface: press.surface })
            }}
          />
          <Text color="text-muted">esc close</Text>
        </Box>
      </Box>
    )
  })
}

// findBin resolves the CLI the way the hook launcher does, minus the plugin
// version (this plugin's can differ): $AGENT_QUORUM_BIN, then the newest
// installed agent-quorum-<version>, then agent-quorum on PATH.
async function findBin($: EngineInterface): Promise<string> {
  const env = await $.env.get('AGENT_QUORUM_BIN')
  if (env) return env
  const home = (await $.env.get('AGENT_QUORUM_HOME')) ?? `${await $.env.get('HOME')}/.agent-quorum`
  try {
    const names = (await $.fs.list(`${home}/bin`)).map(f => f.name).filter(n => version(n))
    names.sort((a, b) => compareVersions(version(b)!, version(a)!))
    if (names[0]) return `${home}/bin/${names[0]}`
  } catch {
    // no install dir: fall through to PATH
  }
  return 'agent-quorum'
}

// version parses "agent-quorum-1.2.3[-pre]" into [1, 2, 3, isRelease].
export function version(name: string): number[] | undefined {
  const m = /^agent-quorum-(\d+)\.(\d+)\.(\d+)(-.+)?$/.exec(name)
  return m ? [Number(m[1]), Number(m[2]), Number(m[3]), m[4] ? 0 : 1] : undefined
}

// compareVersions orders version() tuples numerically, so 0.10.0 > 0.9.0 and
// a release > its pre-releases.
export function compareVersions(a: number[], b: number[]): number {
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return (a[i] ?? 0) - (b[i] ?? 0)
  return 0
}

// sortRows groups sessions by repo (cwd when not in one), most recent first.
export function sortRows(rows: readonly Holder[]): Holder[] {
  return [...rows].sort(
    (a, b) => (a.repo || a.cwd || '').localeCompare(b.repo || b.cwd || '') || a.idle_s - b.idle_s,
  )
}

// pick is the selected session if still listed, else the first that isn't us.
export function pick(rows: readonly Holder[], id: string, self: string): Holder | undefined {
  return rows.find(r => r.session === id) ?? rows.find(r => r.session !== self) ?? rows[0]
}

// idle formats idle seconds the way htop formats times: 59s, 10m, 2h.
export function idle(s: number): string {
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  return `${Math.floor(s / 3600)}h`
}

// idleColor heats IDLE: green while active, amber past 10m, red past 30m.
function idleColor(s: number): string | undefined {
  if (s < 60) return 'text-success'
  if (s < 600) return undefined
  return s < 1800 ? 'text-warning' : 'text-danger'
}

// base is the last element of a path.
function base(p: string): string {
  return p.slice(p.lastIndexOf('/') + 1)
}

// rel shows p relative to repo when it is inside it.
function rel(p: string, repo?: string): string {
  return repo && p.startsWith(`${repo}/`) ? p.slice(repo.length + 1) : p
}
