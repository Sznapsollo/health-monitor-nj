import { beforeEach, describe, expect, it } from 'vitest'

import { applyOrder, MAX_TILE_HEIGHT, MIN_TILE_HEIGHT, moveWithin, useUiStore } from './useUiStore'

describe('moveWithin', () => {
  it('swaps with the neighbour', () => {
    expect(moveWithin(['a', 'b', 'c'], 'b', -1)).toEqual(['b', 'a', 'c'])
    expect(moveWithin(['a', 'b', 'c'], 'b', 1)).toEqual(['a', 'c', 'b'])
  })

  it('does nothing at the ends or for an unknown value', () => {
    expect(moveWithin(['a', 'b'], 'a', -1)).toEqual(['a', 'b'])
    expect(moveWithin(['a', 'b'], 'b', 1)).toEqual(['a', 'b'])
    expect(moveWithin(['a', 'b'], 'zz', 1)).toEqual(['a', 'b'])
  })
})

describe('applyOrder', () => {
  const items = [{ value: 'a' }, { value: 'b' }, { value: 'c' }]

  it('follows the remembered order', () => {
    expect(applyOrder(items, ['c', 'a', 'b']).map((i) => i.value)).toEqual(['c', 'a', 'b'])
  })

  it('keeps the server order when nothing was arranged', () => {
    expect(applyOrder(items, []).map((i) => i.value)).toEqual(['a', 'b', 'c'])
  })

  it('puts values the viewer never arranged after the ones they did', () => {
    expect(applyOrder(items, ['c']).map((i) => i.value)).toEqual(['c', 'a', 'b'])
  })
})

describe('ui preferences', () => {
  beforeEach(() => {
    globalThis.localStorage?.clear()
    useUiStore.getState().reset()
  })

  it('zooms within bounds', () => {
    const { zoom, setTileHeight } = useUiStore.getState()
    setTileHeight(MIN_TILE_HEIGHT)
    zoom(-1)
    expect(useUiStore.getState().tileHeight).toBe(MIN_TILE_HEIGHT)

    setTileHeight(MAX_TILE_HEIGHT)
    zoom(1)
    expect(useUiStore.getState().tileHeight).toBe(MAX_TILE_HEIGHT)
  })

  it('remembers preferences across a reload', () => {
    useUiStore.getState().toggleLegend()
    useUiStore.getState().setColumns(3)
    const stored = JSON.parse(globalThis.localStorage.getItem('hm.ui') ?? '{}')
    expect(stored.showLegend).toBe(true)
    expect(stored.columns).toBe(3)
  })

  it('toggles a tile closed and open again', () => {
    const { toggleMinimised } = useUiStore.getState()
    toggleMinimised('requests//a')
    expect(useUiStore.getState().minimised['requests//a']).toBe(true)
    toggleMinimised('requests//a')
    expect(useUiStore.getState().minimised['requests//a']).toBeUndefined()
  })

  it('clamps the column count to something drawable', () => {
    useUiStore.getState().setColumns(99)
    expect(useUiStore.getState().columns).toBe(4)
    useUiStore.getState().setColumns(0)
    expect(useUiStore.getState().columns).toBe(1)
  })
})
