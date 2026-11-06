import type { Activity, Ledger, Line, Seg } from '../types'

export const TICK_MS = 5000
export const RESTLESS_MS = 2 * 60_000
export const DOZING_MS = 5 * 60_000
export const STUFFED_PCT = 70
export const MIN_TTL_MS = 5 * 60_000
export const HOUR_TTL_MS = 60 * 60_000
// Matches the engine's chilly window (chilly_frac 0.2).
export const CHILLY_FRAC = 0.2
// A request that writes at least this much and reads less than it wrote re-cached the prefix.
export const COLD_MIN_TOKENS = 10_000
export const FLASH_MS = 8000
export const MILESTONE_KINDS = new Set(['evolve', 'level', 'devolve'])

export const EMPTY_LEDGER: Ledger = {
  added: 0,
  removed: 0,
  coldMisses: 0,
  recacheTokens: 0,
  lastResponseAt: null,
  // The mod API doesn't expose the cache TTL; 1h is the subscription default, corrected upward only.
  ttlMs: HOUR_TTL_MS,
  compactedSinceResponse: false,
  lastModel: '',
  lastEffort: '',
  milestonesSince: null,
}

// Theme keys follow the person's theme; shiny keeps the binary's gold (xterm 220).
export function toneColor(tone: string): string | undefined {
  if (tone === 'good') return 'success'
  if (tone === 'meh') return 'warning'
  if (tone === 'bad') return 'error'
  if (tone === 'shiny') return '#ffd700'
  return undefined
}

const FLASH_STYLE: Record<string, { glyph: string; color: string }> = {
  evolve: { glyph: '✦', color: '#ffd75f' },
  level: { glyph: '▲', color: '#5fd787' },
  devolve: { glyph: '▽', color: '#a8a8a8' },
}

export function flashLine(kind: string, msg: string, trait?: string): Line {
  const style = FLASH_STYLE[kind] ?? { glyph: '▲', color: '#5fd787' }
  return [
    { text: `${style.glyph} `, color: style.color, bold: true },
    { text: trait ? `${msg} · ${trait}` : msg, color: style.color },
  ]
}

export function reaction(
  name: string,
  mode: string,
  act: Activity | null,
  turnStage: number,
  ctxPct: number | undefined,
): string {
  if (turnStage >= 2) return `${name} dozed off waiting`
  if (turnStage === 1) return `${name} is getting restless`
  if ((ctxPct ?? 0) >= STUFFED_PCT) return `${name} is feeling stuffed`
  if (mode === 'thinking') return `${name} is pondering`
  if (mode === 'requesting') return `${name} is waiting for a treat`
  if (mode === 'responding') return `${name} is chattering`
  if (act === null) return `${name} is busy`
  if (act.isCommit) return `${name} smells a commit!`
  if (act.tool === 'Bash') return `${name} is poking the shell`
  if (['Edit', 'Write', 'NotebookEdit'].includes(act.tool)) return `${name} is scribbling`
  if (['Read', 'Grep', 'Glob'].includes(act.tool)) return `${name} is sniffing around`
  if (act.tool === 'Agent') return `${name} is herding helpers`
  if (['WebFetch', 'WebSearch'].includes(act.tool)) return `${name} is out foraging`
  return `${name} is fetching ${act.tool}`
}

export function stageFor(elapsedMs: number): number {
  if (elapsedMs >= DOZING_MS) return 2
  if (elapsedMs >= RESTLESS_MS) return 1
  return 0
}

export function patchLines(patch: readonly { lines: readonly string[] }[]): { added: number; removed: number } {
  let added = 0
  let removed = 0
  for (const hunk of patch) {
    for (const line of hunk.lines) {
      if (line.startsWith('+')) added++
      else if (line.startsWith('-')) removed++
    }
  }
  return { added, removed }
}

export type Step = { startedAt: number; model: string; effort: string; cacheRead: number; cacheWrite: number }

// A miss after an idle gap past the TTL is a cold cache. The TTL is only learned upward (a hit past 5m
// means 1h): a miss after a short gap has too many other causes to prove a 5m TTL.
export function observeStep(l: Ledger, step: Step, now: number): Ledger {
  const gap = l.lastResponseAt === null ? 0 : step.startedAt - l.lastResponseAt
  const isSwitch = l.lastModel !== '' && (step.model !== l.lastModel || step.effort !== l.lastEffort)
  const isIdle = gap >= MIN_TTL_MS && !l.compactedSinceResponse && !isSwitch
  const isMiss = step.cacheWrite >= COLD_MIN_TOKENS && step.cacheRead < step.cacheWrite

  const ttlMs = isIdle && !isMiss && step.cacheRead >= COLD_MIN_TOKENS ? Math.max(l.ttlMs, HOUR_TTL_MS) : l.ttlMs
  const isCold = isIdle && isMiss && gap >= ttlMs

  return {
    ...l,
    ttlMs,
    coldMisses: l.coldMisses + (isCold ? 1 : 0),
    recacheTokens: l.recacheTokens + (isCold ? step.cacheWrite : 0),
    lastResponseAt: now,
    compactedSinceResponse: false,
    lastModel: step.model,
    lastEffort: step.effort,
  }
}

// The countdown shows only in the engine's chilly window, as a passive glyph.
export function chillText(l: Ledger, now: number): string {
  if (l.lastResponseAt === null) return ''
  const left = l.lastResponseAt + l.ttlMs - now
  return left > 0 && left < l.ttlMs * CHILLY_FRAC ? `❄ ${Math.ceil(left / 60_000)}m` : ''
}

// Mod and binary ship from the same release; a dev build on either side can't be compared, so it gets no hint.
export function updateHint(modVersion: string, binVersion: unknown): string {
  const release = /^\d+\.\d+\.\d+$/
  if (!release.test(modVersion)) return ''
  if (typeof binVersion === 'string' && (binVersion === modVersion || !release.test(binVersion))) return ''
  return 'run claude-pet update'
}

// The statusline payload shape the binary already scores, with the mod's own totals.
export function buildPayload(
  sessionId: string,
  cwd: string,
  usage: { percent?: number; window?: number; five?: number },
  l: Ledger,
  now: number,
): Record<string, unknown> {
  const data: Record<string, unknown> = {
    source: 'mod',
    session_id: sessionId,
    workspace: { current_dir: cwd },
    cost: { total_lines_added: l.added, total_lines_removed: l.removed },
  }
  if (usage.percent !== undefined) {
    data.context_window = { used_percentage: usage.percent, context_window_size: usage.window }
  }
  if (usage.five !== undefined) {
    data.rate_limits = { five_hour: { used_percentage: usage.five } }
  }

  const cache: Record<string, unknown> = {
    miss_causes: { ttl_expired_mod: l.coldMisses },
    miss_recache_tokens: l.recacheTokens,
  }
  if (l.lastResponseAt !== null) {
    const expiresAt = l.lastResponseAt + l.ttlMs
    cache.warm = now < expiresAt
    cache.ttl = l.ttlMs >= HOUR_TTL_MS ? '1h' : '5m'
    cache.expires_at = expiresAt / 1000
  }
  data.prompt_cache = cache
  return data
}

// xterm 256-colour index to hex: 16-231 is a 6x6x6 cube, 232-255 a grey ramp.
function xterm(n: number): string | undefined {
  const hex = (v: number) => v.toString(16).padStart(2, '0')
  if (n >= 16 && n <= 231) {
    const level = (c: number) => (c === 0 ? 0 : 55 + c * 40)
    const i = n - 16
    return `#${hex(level(Math.floor(i / 36)))}${hex(level(Math.floor(i / 6) % 6))}${hex(level(i % 6))}`
  }
  if (n >= 232 && n <= 255) {
    const g = 8 + (n - 232) * 10
    return `#${hex(g)}${hex(g)}${hex(g)}`
  }
  return undefined
}

const SGR = /\x1b\[([0-9;]*)m/g

// The stat sheet is still ANSI; only the codes claude-pet emits (0, 1, 2, 38;5;N) are understood.
export function parseAnsi(text: string): Line {
  const segs: Line = []
  let style: Omit<Seg, 'text'> = {}
  let at = 0
  const push = (chunk: string) => chunk && segs.push({ text: chunk, ...style })

  for (const m of text.matchAll(SGR)) {
    push(text.slice(at, m.index))
    at = m.index + m[0].length
    const codes = (m[1] ?? '').split(';').map(Number)
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i]
      if (c === 0) style = {}
      else if (c === 1) style = { ...style, bold: true }
      else if (c === 2) style = { ...style, dim: true }
      else if (c === 38 && codes[i + 1] === 5) {
        style = { ...style, color: xterm(codes[i + 2] ?? -1) }
        i += 2
      }
    }
  }
  push(text.slice(at))
  return segs
}
