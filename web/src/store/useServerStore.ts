import { create } from 'zustand'

import { fetchServer, type ServerResources } from '../api/server'

interface ServerState {
  resources: ServerResources | null
  load: (history?: boolean) => Promise<void>
}

export const useServerStore = create<ServerState>((set, get) => ({
  resources: null,
  load: async (history = false) => {
    try {
      const next = await fetchServer(history)
      // A plain poll keeps the hour already fetched for the HM errors tab.
      set({ resources: history ? next : { ...next, history: get().resources?.history } })
    } catch {
      set({ resources: null })
    }
  },
}))
