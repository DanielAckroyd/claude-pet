export type Seg = { text: string; color?: string; bold?: boolean; dim?: boolean }
export type Line = Seg[]
export type Activity = { tool: string; isCommit: boolean }

// What `claude-pet statusline --json` prints, minus the milestones the tick consumes.
export type Face = { body: string; word: string; tone: string; name: string; owner: string }

// Running per-session totals, sent to the binary the way the statusline payload carried them.
export type Ledger = {
  added: number
  removed: number
  coldMisses: number
  recacheTokens: number
  lastResponseAt: number | null
  ttlMs: number
  compactedSinceResponse: boolean
  lastModel: string
  lastEffort: string
  milestonesSince: number | null
}

declare module 'claude-code' {
  interface PluginState {
    claudepet: {
      face: Shaped<Face | null>
      sheet: Shaped<Line[]>
      activity: Shaped<Activity | null>
      stage: number
      ledger: Shaped<Ledger>
      chill: string
      flash: Shaped<Line>
      hint: string
    }
  }
}
