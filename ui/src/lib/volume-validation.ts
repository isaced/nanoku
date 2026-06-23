import type { VolumeInput } from './types'

export function volumeRowInvalid(row: VolumeInput): boolean {
  const target = (row.target ?? '').trim()
  if (target === '') return true
  if (row.type === 'bind' && (row.source ?? '').trim() === '') return true
  return false
}