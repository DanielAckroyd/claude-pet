import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Activity, Face } from '../types'
import {
  EMPTY_LEDGER,
  FLASH_MS,
  MILESTONE_KINDS,
  TICK_MS,
  buildPayload,
  chillText,
  flashLine,
  observeStep,
  parseAnsi,
  patchLines,
  reaction,
  stageFor,
  toneColor,
  updateHint,
} from './logic'
import type { Step } from './logic'

const PANE = 'claudepet-sheet'

const face = atom({ plugin: 'claudepet', key: 'face' } as const, null as Face | null, { shape: 'face-1' })
const sheet = atom({ plugin: 'claudepet', key: 'sheet' } as const, [], { shape: 'lines-1' })
const activity = atom({ plugin: 'claudepet', key: 'activity' } as const, null as Activity | null, {
  shape: 'activity-1',
})
// Bumped by the tick so the spinner redraws as a turn drags on.
const stage = atom({ plugin: 'claudepet', key: 'stage' } as const, 0)
// $.state outlives a reload, so the totals the binary diffs against never go backwards mid-session.
const ledger = atom({ plugin: 'claudepet', key: 'ledger' } as const, EMPTY_LEDGER, { shape: 'ledger-1' })
const chill = atom({ plugin: 'claudepet', key: 'chill' } as const, '')
const flash = atom({ plugin: 'claudepet', key: 'flash' } as const, [], { shape: 'flash-1' })
const hint = atom({ plugin: 'claudepet', key: 'hint' } as const, '')

type Usage = { percent?: number; window?: number; five?: number }
type Milestone = { ts: number; kind: string; msg: string; trait?: string }
type JsonOut = Face & { text: string; mood: string; version: string; milestones: Milestone[] }

let bin = 'claude-pet'
let modVersion = ''
let cwd = ''
let usage: Usage = {}
let isTicking = false
let hasLoggedFailure = false
let turnStartedAt: number | undefined
// A flash clears itself only while it's still the one showing, so back-to-back milestones don't cut each other short.
let flashSeq = 0

// Best effort: without our own version there's just no update hint.
async function readModVersion($: EngineInterface): Promise<string> {
  try {
    const manifest = JSON.parse(await $.fs.read(`${$.plugin.root}/.claude-plugin/plugin.json`)) as { version?: unknown }
    return typeof manifest.version === 'string' ? manifest.version : ''
  } catch {
    return ''
  }
}

async function setHint($: EngineInterface, text: string) {
  if ((await read($, hint)) !== text) await update($, hint, () => text)
}

function logOnce($: EngineInterface, what: string, err: unknown) {
  if (hasLoggedFailure) return
  hasLoggedFailure = true
  $.ui.log(`claudepet: ${what} failed (${String(err)}); the pet stays hidden`, { to: 'debug' })
}

async function showFlash($: EngineInterface, m: Milestone) {
  const seq = ++flashSeq
  await update($, flash, () => flashLine(m.kind, m.msg, m.trait))
  $.clock.after(FLASH_MS, () => {
    if (seq === flashSeq) void update($, flash, () => [])
  })
}

async function tick($: EngineInterface) {
  if (isTicking) return
  isTicking = true
  try {
    const now = await $.clock.now()
    const l = await read($, ledger)
    // First load starts from now: a fresh session shouldn't replay the pet's history as flashes.
    const since = l.milestonesSince ?? now / 1000
    const data = buildPayload(await $.session.id(), cwd, usage, l, now)
    const { stdout } = await $.process.run([bin, 'statusline', '--json', '--since', String(since)], {
      stdin: JSON.stringify(data),
      timeoutMs: 2000,
    })

    let out: Partial<JsonOut>
    try {
      out = JSON.parse(stdout) as Partial<JsonOut>
    } catch (err) {
      // A binary older than --json prints its plain segment instead: hide the pet, point at the update.
      await setHint($, updateHint(modVersion, undefined))
      throw err
    }
    if (typeof out.body !== 'string') throw new Error('no --json output')
    await setHint($, updateHint(modVersion, out.version))

    await update($, face, () => ({
      body: out.body ?? '',
      word: out.word ?? '',
      tone: out.tone ?? '',
      name: out.name ?? 'pet',
      owner: out.owner ?? '',
    }))

    const milestones = (out.milestones ?? []).filter(m => MILESTONE_KINDS.has(m.kind))
    const newest = milestones.reduce((max, m) => Math.max(max, m.ts), since)
    if (l.milestonesSince !== newest) await update($, ledger, cur => ({ ...cur, milestonesSince: newest }))
    for (const m of milestones) await showFlash($, m)

    const turnStage = stageFor(turnStartedAt === undefined ? 0 : now - turnStartedAt)
    if ((await read($, stage)) !== turnStage) await update($, stage, () => turnStage)

    const countdown = chillText(await read($, ledger), now)
    if ((await read($, chill)) !== countdown) await update($, chill, () => countdown)
  } catch (err) {
    logOnce($, 'tick', err)
    await update($, face, () => null)
  } finally {
    isTicking = false
  }
}

async function runHook($: EngineInterface, data: Record<string, unknown>) {
  try {
    await $.process.run([bin, 'hook'], { stdin: JSON.stringify(data), timeoutMs: 5000 })
  } catch (err) {
    logOnce($, `${String(data.hook_event_name)} hook`, err)
  }
}

async function recordCompact($: EngineInterface, trigger: string) {
  const sessionId = await $.session.id()
  await runHook($, { hook_event_name: 'PreCompact', trigger: trigger === 'auto' ? 'auto' : 'manual', session_id: sessionId })
  // The next request re-caches a rewritten prefix: that's the compaction's doing, not idling.
  await update($, ledger, l => ({ ...l, compactedSinceResponse: true }))
}

async function observeCache($: EngineInterface, step: Step) {
  const now = await $.clock.now()
  await update($, ledger, l => observeStep(l, step, now))
}

async function addLines($: EngineInterface, delta: { added: number; removed: number }) {
  if (delta.added === 0 && delta.removed === 0) return
  await update($, ledger, l => ({ ...l, added: l.added + delta.added, removed: l.removed + delta.removed }))
}

async function openSheet($: EngineInterface) {
  const { stdout } = await $.process.run([bin], { timeoutMs: 2000 })
  await update($, sheet, () => stdout.replace(/\n+$/, '').split('\n').map(parseAnsi))
  await $.ui.open({ id: PANE, title: 'Pet' })
}

export const register: Register = (on, options) => {
  bin = typeof options.bin === 'string' && options.bin !== '' ? options.bin : 'claude-pet'

  on('session.start', async ($, e, next) => {
    const started = await next(e)
    cwd = e.cwd
    modVersion = await readModVersion($)
    await $.command.register({ name: 'pet', description: "Open the pet's stat sheet in a pane" })
    void tick($)
    $.clock.every(TICK_MS, () => void tick($))
    return started
  })

  on('session.measure', async ($, e, next) => {
    usage = {
      percent: e.context.percent,
      window: e.context.window,
      five: e.rateLimits.find(r => r.kind === 'five_hour')?.percentUsed,
    }
    void tick($)
    return next(e)
  })

  on('session.compact', async ($, e, next) => {
    const result = await next(e)
    void recordCompact($, e.trigger)
    return result
  }).catch(($, e, next) => next(e))

  // Awaited before next(e) so the exit waits for it, as it waits for the settings hook.
  on('session.end', async ($, e, next) => {
    await runHook($, { hook_event_name: 'SessionEnd', session_id: e.sessionId, reason: e.reason })
    return next(e)
  }).catch(($, e, next) => next(e))

  // Pure pass-through: forwards the stream untouched and reads cache usage once it ends.
  on('turn.step', async function* ($, e, next) {
    const startedAt = Date.now()
    const r = yield* next(e)
    if (e.agentId === undefined && r.usage) {
      const step: Step = {
        startedAt,
        model: e.model,
        effort: String(e.effort ?? ''),
        cacheRead: r.usage.cache_read_input_tokens,
        cacheWrite: r.usage.cache_creation_input_tokens,
      }
      void observeCache($, step).catch(err => logOnce($, 'cache observe', err))
    }
    return r
  })

  on('turn.start', async ($, e, next) => {
    if (e.agentId === undefined) {
      turnStartedAt = Date.now()
      void update($, activity, () => null)
      void update($, stage, () => 0)
    }
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    if (e.agentId === undefined) turnStartedAt = undefined
    return next(e)
  })

  // Never awaits before the tool runs, and hands back the tool's result untouched.
  on('tool.call', async ($, e, next) => {
    if (e.agentId === undefined) {
      const isCommit = e.tool === 'Bash' && /\bgit\s+commit\b/.test(e.command)
      void update($, activity, () => ({ tool: e.tool, isCommit }))
    }

    if (e.tool === 'Edit') {
      const ran = await next(e)
      if (ran.deny === undefined && ran.isError !== true) void addLines($, patchLines(ran.result.structuredPatch))
      return ran
    }

    if (e.tool === 'Write') {
      const ran = await next(e)
      if (ran.deny === undefined && ran.isError !== true) {
        const { structuredPatch, type, content } = ran.result
        // A new file can come back with no hunks; every line of it is added.
        const delta =
          structuredPatch.length === 0 && type === 'create'
            ? { added: content.replace(/\n$/, '').split('\n').length, removed: 0 }
            : patchLines(structuredPatch)
        void addLines($, delta)
      }
      return ran
    }

    return next(e)
  }).catch(($, e, next) => next(e))

  // Rewrites only the word, so the engine still draws elapsed time and tokens after it.
  on('ui.render', { component: 'Spinner' }, async ($, e, next) => {
    const f = await read($, face)
    if (f === null) return next(e)
    const word = `${f.body} ${reaction(f.name, e.props.mode, await read($, activity), await read($, stage), usage.percent)}`
    return next({ ...e, props: { ...e.props, word } })
  })

  on('command.run', { command: 'pet' }, async $ => {
    try {
      await openSheet($)
      return { text: 'Pet sheet opened.' }
    } catch (err) {
      return { text: `claudepet: couldn't run ${bin} (${String(err)})` }
    }
  })

  // Footer mode labels: our own tree so the face keeps its colour; the engine's labels stay first.
  on('ui.render', { component: 'SessionMode' }, async ($, e, next) => {
    const f = await read($, face)
    const countdown = await read($, chill)
    const milestone = await read($, flash)
    const updateText = await read($, hint)
    // Where the statusline already draws the pet, the footer would show a second one.
    const showFace = f !== null && f.owner !== 'statusline'
    if (!showFace && countdown === '' && milestone.length === 0 && updateText === '') return next(e)

    const { Box, Text } = $.ui.resolve(e)
    return (
      <Box flexDirection="row">
        {e.props.modes.length > 0 ? <Text dimColor>{e.props.modes.join(' & ')} · </Text> : null}
        {showFace ? (
          <Text color={toneColor(f.tone)}>
            {f.body} {f.word}
          </Text>
        ) : null}
        {countdown ? <Text color="#5fafff"> {countdown}</Text> : null}
        {updateText ? <Text dimColor> {updateText}</Text> : null}
        {milestone.length > 0 ? <Text>  </Text> : null}
        {milestone.map(s => (
          <Text color={s.color} bold={s.bold}>
            {s.text}
          </Text>
        ))}
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const lines = await read($, sheet)
    const { Box, Text } = $.ui.resolve(e)
    return (
      <Box flexDirection="column">
        {lines.map(line => (
          <Box flexDirection="row">
            {line.length === 0 ? <Text> </Text> : null}
            {line.map(s => (
              <Text color={s.color} bold={s.bold} dimColor={s.dim}>
                {s.text}
              </Text>
            ))}
          </Box>
        ))}
      </Box>
    )
  })
}
