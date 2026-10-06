// fmtBytes: a size as people read it (1 decimal from KB up).
export function fmtBytes(n: number) {
  if (!n || n < 1024) return `${n || 0} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}
