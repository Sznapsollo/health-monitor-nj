import { create } from 'zustand'

import { fetchIssues, markKnown, type Issue } from '../api/issues'

interface HealthState {
  issues: Issue[]
  unknown: number
  error: string | null
  loaded: boolean
  load: () => Promise<void>
  mark: (keys: string[], known: boolean) => Promise<void>
}

let marks = 0

export const useHealthStore = create<HealthState>((set) => ({
  issues: [],
  unknown: 0,
  error: null,
  loaded: false,
  load: async () => {
    const at = marks
    try {
      const report = await fetchIssues()
      if (at !== marks) return
      set({ issues: report.issues, unknown: report.unknown, error: null, loaded: true })
    } catch (err) {
      if (at !== marks) return
      set({ error: err instanceof Error ? err.message : 'unknown error', loaded: true })
    }
  },
  mark: async (keys, known) => {
    const seq = ++marks
    try {
      const report = await markKnown(keys, known)
      if (seq !== marks) return
      set({ issues: report.issues, unknown: report.unknown, error: null, loaded: true })
    } catch (err) {
      if (seq !== marks) return
      set({ error: err instanceof Error ? err.message : 'unknown error', loaded: true })
    }
  },
}))
