// Remembers which projects this viewer opens most, for the sidebar. Kept in
// localStorage: it is a per-viewer convenience, nothing depends on it.
interface Usage { count: number, last: number }
const KEY = 'office:project-usage'

function read(): Record<string, Usage> {
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? '{}')
  } catch {
    return {}
  }
}

export function useProjectUsage() {
  const usage = useState<Record<string, Usage>>('project-usage', () => (import.meta.client ? read() : {}))
  function touch(id: string) {
    const u = { ...usage.value }
    u[id] = { count: (u[id]?.count ?? 0) + 1, last: Date.now() }
    usage.value = u
    try {
      localStorage.setItem(KEY, JSON.stringify(u))
    } catch { /* private mode: keep in memory */ }
  }
  // most opened first, then most recent; unknown projects keep API order
  function top<T extends { id: string }>(list: T[], n: number, include?: string): T[] {
    const score = (p: T) => usage.value[p.id]
    const sorted = [...list].sort((a, b) => (score(b)?.count ?? 0) - (score(a)?.count ?? 0) || (score(b)?.last ?? 0) - (score(a)?.last ?? 0))
    const out = sorted.slice(0, n)
    const cur = include ? list.find(p => p.id === include) : undefined
    if (cur && !out.includes(cur)) {
      if (out.length < n) out.push(cur)
      else out[n - 1] = cur
    }
    return out
  }
  return { touch, top }
}
