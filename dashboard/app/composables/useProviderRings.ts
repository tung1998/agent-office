// The AI connections' marks (header, watch box): the vendor's logo (else its
// initial) in two rings: the outer fills to the 5-hour window, the inner to
// the week. Red when the connection is in error, a plain green ring when it
// reports no window.
import type { MessageKey } from '~/locales/vi'

// the vendor's logo where there is one (simple-icons); written in full so the
// icon bundle picks them up
const kindLogo: Record<string, string> = {
  claude_cli: 'i-simple-icons-claude', anthropic: 'i-simple-icons-claude',
  codex_cli: 'i-simple-icons-openai', openai: 'i-simple-icons-openai',
  gemini_cli: 'i-simple-icons-googlegemini', antigravity_cli: 'i-simple-icons-google', dsh: 'i-simple-icons-deepseek'
}
const presetLogo: Record<string, string> = {
  openrouter: 'i-simple-icons-openrouter', gemini: 'i-simple-icons-googlegemini', deepseek: 'i-simple-icons-deepseek',
  xai: 'i-simple-icons-x', mistral: 'i-simple-icons-mistralai', perplexity: 'i-simple-icons-perplexity',
  nvidia: 'i-simple-icons-nvidia', qwen: 'i-simple-icons-qwen', minimax: 'i-simple-icons-minimax',
  ollama: 'i-simple-icons-ollama', lmstudio: 'i-simple-icons-lmstudio'
}
// brand colours of the logos that have one; the rest take the text colour
const logoColor: Record<string, string> = {
  'i-simple-icons-claude': 'text-[#D97757]', 'i-simple-icons-googlegemini': 'text-[#8E75B2]', 'i-simple-icons-google': 'text-[#4285F4]',
  'i-simple-icons-deepseek': 'text-[#4D6BFE]', 'i-simple-icons-mistralai': 'text-[#FA520F]', 'i-simple-icons-nvidia': 'text-[#76B900]',
  'i-simple-icons-perplexity': 'text-[#1FB8CD]', 'i-simple-icons-qwen': 'text-[#615CED]'
}
const palette = ['bg-violet-500', 'bg-sky-500', 'bg-emerald-500', 'bg-amber-500', 'bg-rose-500', 'bg-indigo-500', 'bg-teal-500', 'bg-orange-500']
const colorOf = (key: string) => palette[[...key].reduce((a, c) => a + c.charCodeAt(0), 0) % palette.length]
const R = 11 // outer ring: 5 hours
const RI = 8.5 // inner ring: the week
const tone = (u: number) => u >= 0.9 ? 'stroke-(--ui-error)' : u >= 0.7 ? 'stroke-(--ui-warning)' : 'stroke-(--ui-primary)'

export interface ProviderRingArc { r: number, stroke: string, share: number, c: number }
export interface ProviderRingData {
  id: string
  name: string
  isDefault: boolean
  error: boolean
  logo: string
  logoColor: string
  letter: string
  bg: string
  arcs: ProviderRingArc[]
  // 5 hours and the week in %, when reported
  windows: { key: 'five_hour' | 'seven_day', pct: number, resetsAt: string }[]
  title: string
}

export function useProviderRings() {
  const { t } = useLang()
  const { data } = useLiveFetch<{ providers: Provider[] }>('/api/providers', { lazy: true })
  const { limits, refresh } = useLimits()
  const rings = computed<ProviderRingData[]>(() => (data.value?.providers ?? []).filter(p => p.enabled).map((p) => {
    const ws = Object.entries(limits.value.find(l => l.provider_id === p.id)?.windows ?? {})
      .filter(([k]) => k === 'five_hour' || k === 'seven_day')
    const error = p.status === 'error'
    const win = Object.fromEntries(ws)
    // error or no window: one full ring; else a ring per window, filled to its share
    const arcs = error || !ws.length
      ? [{ r: R, stroke: error ? 'stroke-(--ui-error)' : 'stroke-(--ui-success)', share: 1 }]
      : ([['five_hour', R], ['seven_day', RI]] as const).filter(([k]) => win[k]).map(([k, r]) => {
          const u = win[k]!.utilization
          return { r, stroke: tone(u), share: Math.min(1, u) }
        })
    const parts = [p.name]
    if (error) parts.push(p.status_detail || t('prov.statusError'))
    for (const [k, w] of ws) parts.push(`${t(`limits.window.${k}` as MessageKey)} ${Math.round(w.utilization * 100)}%`)
    const logo = presetLogo[p.preset] ?? kindLogo[p.kind] ?? ''
    return {
      id: p.id,
      name: p.name,
      isDefault: p.is_default,
      error,
      logo,
      logoColor: logoColor[logo] ?? 'text-(--ui-text)',
      letter: p.name.slice(0, 1).toUpperCase(),
      bg: colorOf(p.kind) ?? palette[0]!,
      arcs: arcs.map(a => ({ ...a, c: 2 * Math.PI * a.r })),
      windows: (['five_hour', 'seven_day'] as const).filter(k => win[k]).map(k => ({ key: k, pct: Math.round(win[k]!.utilization * 100), resetsAt: win[k]!.resets_at })),
      title: parts.join(' · ')
    }
  }))
  return { rings, providers: computed(() => data.value?.providers ?? []), loaded: computed(() => !!data.value), refresh }
}
