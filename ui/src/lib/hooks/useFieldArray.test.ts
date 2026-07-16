// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useFieldArray } from './useFieldArray'

interface Row {
  name: string
  count: number
}

const seed: Row[] = [
  { name: 'a', count: 1 },
  { name: 'b', count: 2 },
  { name: 'c', count: 3 },
]

describe('useFieldArray', () => {
  it('set updates one row in place (immutable)', () => {
    const onChange = vi.fn()
    const { result } = renderHook(() => useFieldArray<Row>(seed, onChange))
    act(() => {
      result.current.set(1, { count: 99 })
    })
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).toEqual([
      { name: 'a', count: 1 },
      { name: 'b', count: 99 },
      { name: 'c', count: 3 },
    ])
  })

  it('add appends a new row at the end', () => {
    const onChange = vi.fn()
    const { result } = renderHook(() => useFieldArray<Row>(seed, onChange))
    act(() => {
      result.current.add({ name: 'd', count: 4 })
    })
    expect(onChange).toHaveBeenCalledWith([
      ...seed,
      { name: 'd', count: 4 },
    ])
  })

  it('remove drops a row by index', () => {
    const onChange = vi.fn()
    const { result } = renderHook(() => useFieldArray<Row>(seed, onChange))
    act(() => {
      result.current.remove(1)
    })
    expect(onChange).toHaveBeenCalledWith([
      { name: 'a', count: 1 },
      { name: 'c', count: 3 },
    ])
  })

  it('handles empty array (no rows to set/remove)', () => {
    const onChange = vi.fn()
    const { result } = renderHook(() => useFieldArray<Row>([], onChange))
    act(() => {
      result.current.add({ name: 'first', count: 0 })
    })
    expect(onChange).toHaveBeenCalledWith([{ name: 'first', count: 0 }])
  })

  it('uses the latest rows array on each call (no stale closure)', () => {
    const onChange = vi.fn()
    const { result, rerender } = renderHook(
      ({ rows }: { rows: Row[] }) => useFieldArray<Row>(rows, onChange),
      { initialProps: { rows: seed } },
    )
    rerender({ rows: [{ name: 'x', count: 0 }] })
    act(() => {
      result.current.set(0, { count: 7 })
    })
    expect(onChange).toHaveBeenCalledWith([{ name: 'x', count: 7 }])
  })
})
