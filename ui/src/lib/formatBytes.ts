/**
 * formatBytes renders a byte count as a short, human-readable
 * string (e.g. 1.4 GB). Used by every page that shows memory,
 * network, or disk counters — duplicated four times before this
 * landed in lib/. The 1024-base is correct for Docker stats
 * (the engine reports bytes, not powers-of-10).
 */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  )
  return `${(bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}
