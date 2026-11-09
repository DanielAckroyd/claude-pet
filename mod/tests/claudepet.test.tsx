import { expect, test } from 'claude-code/testing'
import type { TestBody } from 'claude-code/testing'

import {
  EMPTY_LEDGER,
  HOUR_TTL_MS,
  MIN_TTL_MS,
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
} from '../hooks/logic'
import type { Face } from '../types'

const T0 = 1_800_000_000_000
const step = (over: Partial<Parameters<typeof observeStep>[1]> = {}) => ({
  startedAt: T0,
  model: 'opus',
  effort: 'high',
  cacheRead: 0,
  cacheWrite: 0,
  ...over,
})

test('the update hint shows only when two releases differ, or the binary predates versions', async () => {
  expect(updateHint('0.6.0', '0.6.0')).toBe('')
  expect(updateHint('0.6.0', '0.5.0')).toBe('run claude-pet update')
  expect(updateHint('0.5.0', '0.6.0')).toBe('run claude-pet update')
  expect(updateHint('0.6.0', undefined)).toBe('run claude-pet update')
  expect(updateHint('0.6.0', '0.3.1-0.20261105061244-285445c0f0df+dirty')).toBe('')
  expect(updateHint('0.6.0', 'dev')).toBe('')
  expect(updateHint('', '0.5.0')).toBe('')
})

test('patch lines count + and - across hunks', async () => {
  expect(patchLines([{ lines: [' a', '-b', '+c', '+d'] }, { lines: ['-e'] }])).toEqual({ added: 2, removed: 2 })
  expect(patchLines([])).toEqual({ added: 0, removed: 0 })
})

test('reactions follow priority: turn length, then context, then what Claude is doing', async () => {
  const bash = { tool: 'Bash', isCommit: false }
  expect(reaction('Fizz', 'tool-use', bash, 2, 90)).toBe('Fizz dozed off waiting')
  expect(reaction('Fizz', 'tool-use', bash, 1, 90)).toBe('Fizz is getting restless')
  expect(reaction('Fizz', 'tool-use', bash, 0, 75)).toBe('Fizz is feeling stuffed')
  expect(reaction('Fizz', 'thinking', bash, 0, 10)).toBe('Fizz is pondering')
  expect(reaction('Fizz', 'tool-use', { tool: 'Bash', isCommit: true }, 0, 10)).toBe('Fizz smells a commit!')
  expect(reaction('Fizz', 'tool-use', { tool: 'Grep', isCommit: false }, 0, undefined)).toBe('Fizz is sniffing around')
  expect(stageFor(0)).toBe(0)
  expect(stageFor(3 * 60_000)).toBe(1)
  expect(stageFor(6 * 60_000)).toBe(2)
})

test('a miss after an idle gap past the TTL is a cold cache', async () => {
  const warm = { ...EMPTY_LEDGER, lastResponseAt: T0, lastModel: 'opus', lastEffort: 'high' }
  const later = T0 + HOUR_TTL_MS + 1
  const cold = observeStep(warm, step({ startedAt: later, cacheWrite: 200_000, cacheRead: 5_000 }), later + 1)
  expect(cold.coldMisses).toBe(1)
  expect(cold.recacheTokens).toBe(200_000)
  expect(cold.lastResponseAt).toBe(later + 1)

  const quick = observeStep(warm, step({ startedAt: T0 + 60_000, cacheWrite: 200_000 }), T0 + 61_000)
  expect(quick.coldMisses).toBe(0)
})

test('misses after a compaction or a model or effort switch are not idling', async () => {
  const warm = { ...EMPTY_LEDGER, lastResponseAt: T0, lastModel: 'opus', lastEffort: 'high' }
  const later = T0 + HOUR_TTL_MS + 1
  const miss = { startedAt: later, cacheWrite: 200_000 }
  expect(observeStep({ ...warm, compactedSinceResponse: true }, step(miss), later).coldMisses).toBe(0)
  expect(observeStep(warm, step({ ...miss, model: 'sonnet' }), later).coldMisses).toBe(0)
  expect(observeStep(warm, step({ ...miss, effort: 'low' }), later).coldMisses).toBe(0)
})

test('the TTL is only learned upward, from a hit after a long gap', async () => {
  const short = { ...EMPTY_LEDGER, ttlMs: MIN_TTL_MS, lastResponseAt: T0, lastModel: 'opus', lastEffort: 'high' }
  const hit = observeStep(short, step({ startedAt: T0 + 20 * 60_000, cacheRead: 150_000 }), T0 + 20 * 60_000)
  expect(hit.ttlMs).toBe(HOUR_TTL_MS)

  const long = { ...short, ttlMs: HOUR_TTL_MS }
  const miss = observeStep(long, step({ startedAt: T0 + 20 * 60_000, cacheWrite: 150_000 }), T0 + 20 * 60_000)
  expect(miss.ttlMs).toBe(HOUR_TTL_MS)
  expect(miss.coldMisses).toBe(0)
})

test('the countdown shows only in the last fifth of the TTL', async () => {
  const l = { ...EMPTY_LEDGER, lastResponseAt: T0 }
  expect(chillText(l, T0 + 30 * 60_000)).toBe('')
  expect(chillText(l, T0 + 50 * 60_000)).toBe('❄ 10m')
  expect(chillText(l, T0 + HOUR_TTL_MS + 1)).toBe('')
  expect(chillText(EMPTY_LEDGER, T0)).toBe('')
})

test('the payload is the statusline shape, marked as the mod', async () => {
  const l = { ...EMPTY_LEDGER, added: 7, removed: 2, coldMisses: 1, recacheTokens: 90_000, lastResponseAt: T0 }
  const p = buildPayload('sid', '/repo', { percent: 40, window: 1_000_000, five: 12 }, l, T0 + 1000)
  expect(p.source).toBe('mod')
  expect(p.cost).toEqual({ total_lines_added: 7, total_lines_removed: 2 })
  expect(p.rate_limits).toEqual({ five_hour: { used_percentage: 12 } })
  expect(p.prompt_cache).toEqual({
    miss_causes: { ttl_expired_mod: 1 },
    miss_recache_tokens: 90_000,
    warm: true,
    ttl: '1h',
    expires_at: (T0 + HOUR_TTL_MS) / 1000,
  })
})

test('tones map to theme keys and milestones to coloured runs', async () => {
  expect(toneColor('good')).toBe('success')
  expect(toneColor('bad')).toBe('error')
  expect(toneColor('mystery')).toBeUndefined()
  expect(flashLine('evolve', 'evolved into an owl', 'steady').map(s => s.text).join('')).toBe(
    '✦ evolved into an owl · steady',
  )
})

test('the stat sheet ANSI parses into styled runs', async () => {
  expect(parseAnsi('\x1b[1mFizz\x1b[0m the \x1b[38;5;42mowl\x1b[0m')).toEqual([
    { text: 'Fizz', bold: true },
    { text: ' the ' },
    { text: 'owl', color: '#00d787' },
  ])
})

const FOOTER = { plugin: 'claudepet', component: 'SessionMode', props: { modes: [] } } as const
const FACE: Face = { body: '(•ᴗ•)♣', word: 'content', tone: 'good', name: 'Fizz', owner: 'mod' }

for (const surface of ['terminal', 'desktop'] as const) {
  test(`the footer shows the face on ${surface}`, async ($, on) => {
    on('state.get', (_, e) => ({
      value: { value: e.key === 'face' ? { shape: 'face-1', value: FACE } : undefined, version: 1 },
    }))

    const ui = await $.ui.mount({ ...FOOTER, surface })
    expect((await ui.find({ type: 'Text', text: /content/ }))?.text).toBe('(•ᴗ•)♣ content')
    await ui.unmount()
  })

  test(`the footer leaves the row to the engine without a face on ${surface}`, async ($, on) => {
    on('ui.render', ($, e) => {
      const { Text } = $.ui.resolve(e)
      return <Text>engine</Text>
    })

    const ui = await $.ui.mount({ ...FOOTER, surface })
    expect((await ui.find({ type: 'Text', text: /engine/ }))?.text).toBe('engine')
    await ui.unmount()
  })

  test(`the footer shows the update hint without a face on ${surface}`, async ($, on) => {
    on('state.get', (_, e) => ({
      value: { value: e.key === 'hint' ? 'run claude-pet update' : undefined, version: 1 },
    }))

    const ui = await $.ui.mount({ ...FOOTER, surface })
    expect((await ui.find({ type: 'Text', text: /claude-pet update/ }))?.text).toBe(' run claude-pet update')
    await ui.unmount()
  })

  test(`the footer hides the face where the statusline draws it, on ${surface}`, async ($, on) => {
    on('state.get', (_, e) => ({
      value: {
        value: e.key === 'face' ? { shape: 'face-1', value: { ...FACE, owner: 'statusline' } } : undefined,
        version: 1,
      },
    }))
    on('ui.render', ($, e) => {
      const { Text } = $.ui.resolve(e)
      return <Text>engine</Text>
    })

    const ui = await $.ui.mount({ ...FOOTER, surface })
    expect(await ui.find({ type: 'Text', text: /content/ })).toBeUndefined()
    await ui.unmount()
  })
}

const SPINNER = { plugin: 'claudepet', component: 'Spinner', surface: 'terminal' } as const
const SPINNER_PROPS = { word: 'Sauteing', message: null, suffix: '…', mode: 'thinking' } as const

// The engine's Spinner draws whatever word the chain hands it, so the test stands in for it.
function spinnerWithFace(on: Parameters<TestBody>[1]) {
  on('state.get', (_, e) => ({
    value: { value: e.key === 'face' ? { shape: 'face-1', value: FACE } : undefined, version: 1 },
  }))
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>{(e.props as { word: string }).word}</Text>
  })
}

test('the thinking line shows the pet by default', async ($, on) => {
  spinnerWithFace(on)
  const ui = await $.ui.mount({ ...SPINNER, props: SPINNER_PROPS })
  expect((await ui.find({ type: 'Text', text: /Fizz/ }))?.text).toBe('(•ᴗ•)♣ Fizz is pondering')
  await ui.unmount()
})

test('spinner off leaves the thinking line to the engine', { options: { spinner: false } }, async ($, on) => {
  spinnerWithFace(on)
  const ui = await $.ui.mount({ ...SPINNER, props: SPINNER_PROPS })
  expect((await ui.find({ type: 'Text', text: /Sauteing|Fizz/ }))?.text).toBe('Sauteing')
  await ui.unmount()
})
