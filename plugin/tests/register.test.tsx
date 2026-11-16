import { expect, test } from 'claude-code/testing'

import { compareVersions, fileTree, idle, version } from '../hooks/register'

const PANE = {
  plugin: 'agent-quorum',
  component: 'Pane',
  requestId: 'quorum',
  props: {
    title: 'agent-quorum',
    isFocused: true,
    bodyColumns: 100,
    placement: 'dock',
    scroll: { offset: 0, bodyRows: 30 },
    view: {},
  },
} as const

const SESSIONS = {
  daemon: true,
  sessions: [
    { session: 'claude:me', agent: 'claude', pid: 1, repo: '/r', note: 'my prompt', idle_s: 0, paths: ['/r/a.go'] },
    { session: 'codex:peer', agent: 'codex', pid: 2, repo: '/r', note: 'fix flaky test', idle_s: 700, paths: ['/r/internal/daemon/b.go', '/r/internal/daemon/c.go', '/r/x.go'] },
  ],
}

type Status = { exitCode: number; stdout: string; stderr: string }

// start brings the mod up in a session whose `status --json` answers `status`,
// opens the pane with /quorum-dashboard and returns every argv the mod ran.
async function start($: any, on: any, status: Status): Promise<string[][]> {
  const runs: string[][] = []
  on('env.get', (_: unknown, e: { name: string }) => ({ value: e.name === 'AGENT_QUORUM_BIN' ? '/bin/aq' : undefined }))
  on('session.id', () => ({ value: 'me' }))
  on('command.register', () => ({ value: { command: 'quorum-dashboard' } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.panes', () => ({ value: [] }))
  on('clock.every', () => ({ value: undefined }))
  on('session.start', (_: unknown, e: { cwd: string }) => ({ cwd: e.cwd }))
  on('process.run', (_: unknown, e: { argv: string[] }) => {
    runs.push([...e.argv])
    return { value: { ...status, isStdoutTruncated: false, isStderrTruncated: false } }
  })
  await $.session.start({ cwd: '/r', surface: 'terminal', isInteractive: true })
  await $.command.run({ command: 'quorum-dashboard' })
  return runs
}

const ok = (body: unknown): Status => ({ exitCode: 0, stdout: JSON.stringify(body), stderr: '' })

test('the pane lists every session, on every surface', async ($, on) => {
  const runs = await start($, on, ok(SESSIONS))
  expect(runs[0]).toEqual(['/bin/aq', 'status', '--json'])

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...PANE, surface })
    expect(await ui.find({ type: 'Text', text: /daemon up/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /^4$/ })).toBeDefined() // files held
    expect(await ui.find({ type: 'Text', text: /^claude\* +$/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /fix flaky test/ })).toBeDefined()
    // The peer, not this session, is selected by default; trees start closed.
    expect(await ui.find({ type: 'Text', text: /^codex:peer$/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /c\.go/ })).toBeUndefined()
    await ui.unmount()
  }
})

test('Enter on a row opens and closes its file tree', async ($, on) => {
  await start($, on, ok(SESSIONS))
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...PANE, surface })
    await ui.press({ key: 'row:codex:peer' })
    expect(await ui.find({ type: 'Text', text: /^internal\/daemon\/$/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /^c\.go$/ })).toBeDefined()
    await ui.press({ key: 'row:codex:peer' })
    expect(await ui.find({ type: 'Text', text: /c\.go/ })).toBeUndefined()
    await ui.unmount()
  }
})

test('a stopped daemon is said, not an error', async ($, on) => {
  await start($, on, ok({ daemon: false, sessions: [] }))
  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /daemon down/ })).toBeDefined()
  expect(await ui.find({ type: 'Text', text: /starts with the first agent session/ })).toBeDefined()
  await ui.unmount()
})

test('a failing CLI is drawn in the pane', async ($, on) => {
  await start($, on, { exitCode: 1, stdout: '', stderr: 'boom' })
  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /! boom/ })).toBeDefined()
  await ui.unmount()
})

test('installed binaries are ordered as versions, not strings', async () => {
  const names = ['agent-quorum-0.9.0', 'agent-quorum-0.10.0-rc.1', 'agent-quorum-0.10.0', 'other']
  const got = names
    .filter(n => version(n))
    .sort((a, b) => compareVersions(version(b)!, version(a)!))
  expect(got).toEqual(['agent-quorum-0.10.0', 'agent-quorum-0.10.0-rc.1', 'agent-quorum-0.9.0'])
  expect(fileTree(['/r/x.go', '/r/internal/daemon/b.go', '/r/internal/daemon/c.go', '/r/cmd/main.go'], '/r')).toEqual([
    { prefix: '├─ ', name: 'cmd/', isDir: true },
    { prefix: '│  └─ ', name: 'main.go', isDir: false },
    { prefix: '├─ ', name: 'internal/daemon/', isDir: true },
    { prefix: '│  ├─ ', name: 'b.go', isDir: false },
    { prefix: '│  └─ ', name: 'c.go', isDir: false },
    { prefix: '└─ ', name: 'x.go', isDir: false },
  ])
  expect(idle(59)).toBe('59s')
  expect(idle(600)).toBe('10m')
  expect(idle(7200)).toBe('2h')
})
