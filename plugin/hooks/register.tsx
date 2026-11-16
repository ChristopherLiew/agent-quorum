import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Holder, Snap } from '../types'

const PANE = 'quorum'
const ROW = 'row:' // key prefix of a session row's Button
const COMMAND = 'quorum-dashboard'
const POLL_MS = 2000

// Change the shape tag when Snap's fields change: a reload keeps $.state.
const snap = atom(
  { plugin: 'agent-quorum', key: 'snap' } as const,
  { daemon: false, sessions: [], error: '' } as Snap,
  { shape: 'snap-1' },
)
const selected = atom({ plugin: 'agent-quorum', key: 'selected' } as const, '')
const expanded = atom({ plugin: 'agent-quorum', key: 'expanded' } as const, [] as string[])

const agentColor: Record<string, string> = {
  claude: 'text-accent',
  codex: 'text-pro',
  opencode: 'text-warning',
  antigravity: 'text-success',
}

// Column widths; LAST PROMPT takes the rest.
const W = { gutter: 3, pid: 7, agent: 12, idle: 6, files: 6, repo: 16 }

// Module state: a reload resets it, and session.start sets it again.
let bin = 'agent-quorum'
let home = ''
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
    await $.command.register({ name: COMMAND, description: 'Show live agent-quorum sessions in a pane' })
    self = `claude:${await $.session.id()}`
    home = (await $.env.get('HOME')) ?? ''
    bin = await findBin($)
    // ponytail: polls only while the pane is shown; no history is kept.
    $.clock.every(POLL_MS, async () => {
      const panes = await $.ui.panes()
      if (panes.some(pane => pane.id === PANE && pane.isShown)) await refresh($)
    })
    return next(e)
  })

  on('command.run', { command: COMMAND }, async $ => {
    await $.ui.open({ id: PANE, title: 'agent-quorum', focus: true, closeOnEscape: true, rows: 30, columns: 200 })
    await refresh($)
    return { text: 'agent-quorum dashboard opened.' }
  })

  // The focus ring is the selection: ↑/↓ move it from row to row.
  on('ui.focus', { requestId: PANE }, async ($, e, next) => {
    const id = e.element?.startsWith(ROW) ? e.element.slice(ROW.length) : ''
    if (id) await update($, selected, () => id)
    return next(e)
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Text, Button } = $.ui.resolve(e)
    const s = await read($, snap)
    const rows = sortRows(s.sessions)
    const sel = pick(rows, await read($, selected), self)
    const open = new Set(await read($, expanded))
    const isWide = e.props.bodyColumns >= 80
    const files = rows.reduce((n, r) => n + (r.paths?.length ?? 0), 0)
    // What LAST PROMPT has left after the fixed columns, the border and padding.
    const fixed = W.gutter + W.agent + W.idle + W.files + (isWide ? W.pid + W.repo : 0)
    const promptWidth = Math.max(10, e.props.bodyColumns - fixed - 4)

    const toggle = async (id: string) => {
      await update($, selected, () => id)
      await update($, expanded, ids => (ids.includes(id) ? ids.filter(x => x !== id) : [...ids, id]))
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
      try {
        const run = await $.process.run([bin, 'release', '--session', sel.session, '--all'])
        $.ui.toast(run.exitCode === 0 ? `Released ${sel.session}.` : `Release failed: ${run.stderr.trim()}`)
      } catch (err) {
        $.ui.toast(`Release failed: ${err instanceof Error ? err.message : String(err)}`)
      }
      await refresh($)
    }

    const sessionRow = (r: Holder) => {
      const isSel = r.session === sel?.session
      const fg = isSel ? 'on-accent' : undefined
      const agent = agentOf(r)
      const n = r.paths?.length ?? 0
      // Each row is a Button so ↑/↓ walk the rows and Enter toggles its files.
      // A Button holds Text only, so cells are fixed-width padded runs.
      return (
        <Box key={`r-${r.session}`} backgroundColor={isSel ? 'fill-accent' : undefined}>
          <Button key={ROW + r.session} plain onPress={() => toggle(r.session)}>
            <Text color={fg ?? 'text-accent'} bold>
              {fit(`${isSel ? '▶' : ' '}${open.has(r.session) ? '▾' : '▸'}`, W.gutter)}
            </Text>
            {isWide && <Text color={fg ?? 'text-muted'}>{fit(r.pid ? String(r.pid) : '-', W.pid, true)}</Text>}
            <Text color={fg ?? agentColor[agent]} bold>
              {fit(agent + (r.session === self ? '*' : ''), W.agent)}
            </Text>
            <Text color={fg ?? idleColor(r.idle_s)}>{fit(idle(r.idle_s), W.idle, true)}</Text>
            <Text color={fg ?? (n ? 'text-primary' : 'text-muted')} bold={n > 0}>
              {fit(String(n), W.files, true)}
            </Text>
            {isWide && <Text color={fg ?? 'text-pro'}>{fit(base(r.repo || r.cwd || ''), W.repo)}</Text>}
            <Text color={fg ?? 'text-secondary'}>{fit(r.note ?? '', promptWidth)}</Text>
          </Button>
        </Box>
      )
    }

    const tree = (r: Holder) => {
      const lines = fileTree(r.paths ?? [], r.repo || r.cwd)
      const indent = ' '.repeat(W.gutter + (isWide ? W.pid : 0))
      if (lines.length === 0) {
        return (
          <Text color="text-muted">
            {indent}└─ holds no files
          </Text>
        )
      }
      return lines.map(l => (
        <Text wrap="truncate-end">
          <Text color="text-muted">
            {indent}
            {l.prefix}
          </Text>
          <Text color={l.isDir ? 'text-accent' : 'text-primary'} bold={l.isDir}>
            {l.name}
          </Text>
        </Text>
      ))
    }

    return (
      <Box flexDirection="column" borderStyle="round" borderColor="border-accent" paddingX={1}>
        <Box flexDirection="row" justifyContent="space-between" backgroundColor="bg-accent" paddingX={1}>
          <Text>
            <Text color="text-accent" bold>
              ◆ agent-quorum
            </Text>
            <Text color="text-secondary">{'   '}</Text>
            <Text color="text-accent" bold>
              {rows.length}
            </Text>
            <Text color="text-secondary"> sessions · </Text>
            <Text color="text-accent" bold>
              {files}
            </Text>
            <Text color="text-secondary"> files held</Text>
          </Text>
          <Text color={s.daemon ? 'text-success' : 'text-danger'} bold>
            {s.daemon ? '● daemon up' : '○ daemon down'}
          </Text>
        </Box>
        {s.error !== '' && <Text color="text-danger">! {s.error}</Text>}
        <Text> </Text>
        {rows.length === 0 ? (
          <Text color="text-muted">{s.daemon ? 'No live sessions.' : 'The daemon starts with the first agent session.'}</Text>
        ) : (
          <Box flexDirection="column">
            <Box backgroundColor="surface-2">
              <Text color="text-accent" bold>
                {fit('', W.gutter)}
                {isWide && fit('PID', W.pid, true)}
                {fit('AGENT', W.agent)}
                {fit('IDLE', W.idle, true)}
                {fit('FILES', W.files, true)}
                {isWide && fit('REPO', W.repo)}
                LAST PROMPT
              </Text>
            </Box>
            {rows.map(r => (
              <Box key={`g-${r.session}`} flexDirection="column">
                {sessionRow(r)}
                {open.has(r.session) && tree(r)}
              </Box>
            ))}
          </Box>
        )}
        {sel && (
          <Box flexDirection="row" marginTop={1}>
            <Text color="text-accent" bold>
              ▶{' '}
            </Text>
            <Text color={agentColor[agentOf(sel)]} bold>
              {sel.session}
            </Text>
            <Text color="text-muted" wrap="truncate-end">
              {sel.pid ? `  pid ${sel.pid}` : ''}
              {'  '}
              {tilde(sel.cwd ?? '')}
            </Text>
          </Box>
        )}
        <Box flexDirection="row" gap={2} marginTop={1}>
          <Text color="text-muted">↑↓ select  ⏎ files</Text>
          <Button key="release" hotkey="r" plain label="release" dimColor={!sel || sel.session === self} onPress={release} />
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

// findBin resolves the CLI much as the hook launcher does: $AGENT_QUORUM_BIN,
// then the newest installed agent-quorum-<version> (session-start.sh installs
// this plugin's), then agent-quorum on PATH. The mod cannot read plugin.json.
async function findBin($: EngineInterface): Promise<string> {
  const env = await $.env.get('AGENT_QUORUM_BIN')
  if (env) return env
  const dir = (await $.env.get('AGENT_QUORUM_HOME')) ?? `${await $.env.get('HOME')}/.agent-quorum`
  try {
    const names = (await $.fs.list(`${dir}/bin`)).map(f => f.name).filter(n => version(n))
    names.sort((a, b) => compareVersions(version(b)!, version(a)!))
    if (names[0]) return `${dir}/bin/${names[0]}`
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

// TreeLine is one line of fileTree: the branch drawing, then the name.
export type TreeLine = { prefix: string; name: string; isDir: boolean }

type Dir = Map<string, Dir>

// fileTree draws paths as a directory tree relative to root, directories
// first, a chain of single-directory folders folded into one line (a/b/c/).
export function fileTree(paths: readonly string[], root?: string): TreeLine[] {
  const top: Dir = new Map()
  for (const p of paths) {
    let dir = top
    for (const part of rel(p, root).split('/').filter(Boolean)) {
      const next = dir.get(part) ?? new Map()
      dir.set(part, next)
      dir = next
    }
  }
  const out: TreeLine[] = []
  const walk = (dir: Dir, prefix: string) => {
    const kids = [...dir].sort(([a, x], [b, y]) => Number(y.size > 0) - Number(x.size > 0) || a.localeCompare(b))
    kids.forEach(([name, child], i) => {
      let label = name
      for (let only = [...child][0]; child.size === 1 && only && only[1].size > 0; only = [...child][0]) {
        label += `/${only[0]}`
        child = only[1]
      }
      const isLast = i === kids.length - 1
      const isDir = child.size > 0
      out.push({ prefix: prefix + (isLast ? '└─ ' : '├─ '), name: isDir ? `${label}/` : label, isDir })
      walk(child, prefix + (isLast ? '   ' : '│  '))
    })
  }
  walk(top, '')
  return out
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

// fit pads or cuts text to exactly width cells (an ellipsis marks a cut),
// leaving one cell of gap; isRight right-aligns it.
export function fit(text: string, width: number, isRight = false): string {
  const room = width - 1
  const t = text.length > room ? `${text.slice(0, room - 1)}…` : text
  return (isRight ? t.padStart(room) : t.padEnd(room)) + ' '
}

// agentOf is the session's agent, read from its id when the field is absent.
function agentOf(r: Holder): string {
  return r.agent ?? r.session.split(':')[0] ?? ''
}

// base is the last element of a path.
function base(p: string): string {
  return p.slice(p.lastIndexOf('/') + 1)
}

// rel shows p relative to root when it is inside it.
function rel(p: string, root?: string): string {
  return root && p.startsWith(`${root}/`) ? p.slice(root.length + 1) : p
}

// tilde shortens a path under $HOME to ~/...
function tilde(p: string): string {
  return home && p.startsWith(`${home}/`) ? `~${p.slice(home.length)}` : p
}
