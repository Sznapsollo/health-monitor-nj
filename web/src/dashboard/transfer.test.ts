import { describe, expect, it } from 'vitest'

import type { Dashboard } from '../api/dashboards'
import { exportAllFileName, exportFileName, exportText, fileSafe, parseImport } from './transfer'

const ops: Dashboard = {
  id: 'ops',
  platform: 'example',
  name: 'Ops',
  default: true,
  createdBy: 'anna',
  updatedBy: 'bob',
  rows: [
    {
      columns: [
        { width: 2, panels: [{ type: 'chart', signal: 'requests', group: 'port' }] },
        { width: 1, panels: [{ type: 'gauge', signal: 'jobQueuesLoad', sort: 'valueDesc' }] },
      ],
    },
  ],
}

describe('dashboard export and import', () => {
  it('round-trips the arrangement and leaves out who made it and where', () => {
    const text = exportText([ops, { ...ops, id: 'second', name: 'Second', default: false }])
    expect(text).not.toMatch(/anna|bob|"platform"|"id"/)
    const back = parseImport(text)
    expect(back).toEqual([
      { name: 'Ops', default: true, rows: ops.rows },
      { name: 'Second', rows: ops.rows },
    ])
  })

  it('reads a single dashboard as the API returns it, including the older columns form', () => {
    expect(parseImport(JSON.stringify(ops))).toEqual([
      { name: 'Ops', default: true, rows: ops.rows },
    ])
    const older = { name: 'Old', columns: ops.rows[0]!.columns }
    expect(parseImport(JSON.stringify(older))[0]!.rows).toEqual(ops.rows)
  })

  it('refuses what is not a dashboard, saying why', () => {
    expect(() => parseImport('not json')).toThrow('not a JSON file')
    expect(() => parseImport('{"format":"hm-dashboards","dashboards":[]}')).toThrow('no dashboards')
    expect(() => parseImport('{"name":"x","rows":[{"columns":[{"panels":[{}]}]}]}')).toThrow(
      'no valid rows',
    )
    expect(() => parseImport('[1]')).toThrow('not an object')
  })
})

describe('export file names', () => {
  it('turn any dashboard name into a safe one, with the dashboard prefix', () => {
    expect(exportFileName({ ...ops, name: "Anna's ops (copy)" })).toBe(
      'dashboard_Anna_s_ops_copy.json',
    )
    expect(exportFileName({ ...ops, name: 'Zapełnienie kolejek / Łódź' })).toBe(
      'dashboard_Zapelnienie_kolejek_Lodz.json',
    )
    expect(exportFileName({ ...ops, name: '' })).toBe('dashboard_ops.json')
    expect(exportAllFileName('example')).toBe('dashboards_example.json')
  })

  it('never returns an empty or hidden name', () => {
    expect(fileSafe('???')).toBe('unnamed')
    expect(fileSafe('..hidden..')).toBe('hidden')
    expect(fileSafe('a  --  b')).toBe('a_--_b')
  })
})
