import { useCallback } from 'react'

/**
 * useFieldArray returns the three row-mutation helpers every
 * "list of typed objects" editor needs:
 *
 *   - set(i, patch)   — immutable update of one row
 *   - add(defaultRow) — append a new row
 *   - remove(i)       — drop a row by index
 *
 * It does NOT own the row state — the editor still receives
 * `rows` + `onChange` and stays a controlled component. This
 * keeps the parent's submit/reset flow identical (it already
 * holds the array, e.g. via Form.useWatch + useState) and lets
 * a single `onChange` instance drive both this hook and any
 * sibling rendering that also reads `rows`.
 *
 * `add` is parameterised (not bound to a single default row)
 * because each editor in this app seeds new rows differently:
 * env vars start `{ key: '', value: '' }`, volumes start
 * `{ type: 'volume', target: '' }`, exposed ports start
 * `{ name: '', port: 80 }`.
 */
export function useFieldArray<T>(
  rows: readonly T[],
  onChange: (next: T[]) => void,
) {
  const set = useCallback(
    (i: number, patch: Partial<T>) => {
      onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
    },
    [rows, onChange],
  )
  const add = useCallback(
    (defaultRow: T) => {
      onChange([...rows, defaultRow])
    },
    [rows, onChange],
  )
  const remove = useCallback(
    (i: number) => onChange(rows.filter((_, idx) => idx !== i)),
    [rows, onChange],
  )
  return { set, add, remove }
}
