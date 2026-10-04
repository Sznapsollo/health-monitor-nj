/**
 * The CSS grid columns a dashboard asks for. Widths are relative and a missing
 * one counts as 1, so `[2, 1]` is two thirds and one third. A column never
 * grows to fit wide content: a plain `fr` would, and push the page sideways.
 */
export function columnTemplate(columns: { width?: number }[]): string {
  return columns.map((c) => `minmax(0, ${Math.max(1, c.width ?? 1)}fr)`).join(' ')
}
