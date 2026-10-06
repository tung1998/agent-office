export interface MCPState { name: string, target: string, status: 'connected' | 'needs_auth' | 'failed' | 'unknown', detail: string }
export interface MCPCheck { path: string, checked_at: string, running: boolean, error?: string, items: MCPState[] }

const STALE_MS = 10 * 60 * 1000

// useMcpStatus is the last `claude mcp list` of a folder ('' = machine-wide).
// A check takes ~30 s on the server; its result arrives as the mcp.status event.
export function useMcpStatus(path: MaybeRefOrGetter<string>) {
  const all = useState<Record<string, MCPCheck>>('mcp-status', () => ({}))
  const key = computed(() => toValue(path))
  const check = computed(() => all.value[key.value])
  const never = (c?: MCPCheck) => !c || c.checked_at.startsWith('0001')
  const put = (c: MCPCheck) => { all.value = { ...all.value, [c.path]: c } }

  async function recheck() {
    try {
      put(await $fetch<MCPCheck>('/api/automation/mcp/status/check', { method: 'POST', body: { path: key.value } }))
    } catch { /* the strip shows the old result */ }
  }
  async function load() {
    try {
      const c = await $fetch<MCPCheck>('/api/automation/mcp/status', { query: { path: key.value } })
      put(c)
      if (!c.running && (never(c) || Date.now() - new Date(c.checked_at).getTime() > STALE_MS)) await recheck()
    } catch { /* no checks on this office */ }
  }
  onLiveEvent<MCPCheck>('mcp.status', put)
  watch(key, load, { immediate: true })

  const byName = computed(() => new Map((check.value?.items ?? []).map(i => [i.name, i])))
  return { check, byName, checked: computed(() => !never(check.value)), recheck }
}
