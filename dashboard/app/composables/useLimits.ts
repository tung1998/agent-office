// Subscription usage windows each connection reported (Claude Code: 5 hours,
// the week, per model), shared by the sidebar, the chat and the providers page.
export interface LimitWindow { utilization: number, resets_at: string }
export interface ProviderLimits { provider_id: string, provider_name: string, is_default: boolean, status: string, windows: Record<string, LimitWindow>, updated_at: string }

export function useLimits() {
  const limits = useState<ProviderLimits[]>('provider-limits', () => [])
  const loadedAt = useState('provider-limits-at', () => 0)
  async function refresh(force = false) {
    if (!force && Date.now() - loadedAt.value < 30000) return
    loadedAt.value = Date.now()
    try {
      limits.value = (await $fetch<{ limits: ProviderLimits[] }>('/api/providers/limits')).limits
    } catch { /* not signed in yet, or offline: keep what we had */ }
  }
  return { limits, refresh }
}

// windows in a fixed order: 5 hours, the week, then per model
export function orderedWindows(w: Record<string, LimitWindow>) {
  const rank = (k: string) => k === 'five_hour' ? 0 : k === 'seven_day' ? 1 : 2
  return Object.entries(w).sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
}
