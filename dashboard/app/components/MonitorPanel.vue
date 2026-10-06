<script setup lang="ts">
// Health checks of a project, uptime-monitor style: rule-based checks (free)
// and, per monitor, an optional AI analysis when it goes down.
interface Point { at: string, ok: boolean, latency_ms: number, message: string }
interface Monitor {
  id: string
  name: string
  type: 'http' | 'tcp' | 'heartbeat' | 'process' | 'container'
  target: string
  config: { expect_status?: string, keyword?: string, timeout_ms?: number, file?: string }
  interval_s: number
  enabled: boolean
  ai_enabled: boolean
  ai_budget_usd: number
  heartbeat_path?: string
  status: 'pending' | 'up' | 'down' | 'paused'
  last_checked_at: string | null
  last_latency_ms: number
  last_message: string
  uptime_24h: number | null
  avg_latency_ms: number
  trend: Point[]
}
interface MonitorEvent { id: string, monitor_id: string, monitor_name: string, kind: 'up' | 'down', message: string, analysis: string, analysis_status: string, cost_usd: number, at: string }
interface Proc { id: string, name: string, kind: string, state: { status: string, port?: number } }
interface ComposeView { files: string[], file: string, services: { name: string, container?: { ports: { published: number }[] } }[] }

const props = defineProps<{ projectId: string }>()
const emit = defineEmits<{ askAgent: [text: string, files: never[], send?: boolean] }>()
const { t, dateLocale } = useLang()
function fixEvent(e: MonitorEvent) {
  const m = monitors.value.find(x => x.id === e.monitor_id)
  const target = m ? `${typeMeta.value[m.type].label} ${m.type === 'process' ? targetLabel(m) : m.target}` : ''
  emit('askAgent', t('monitor.askFixPrompt', { name: e.monitor_name, target, message: e.message }), [], true)
}
const toast = useToast()
const { isAdmin } = useAuth()

// lazy: the panel renders at once and fills in (no blank flash when switching)
const { data, refresh, status: monStatus } = useLiveFetch<{ monitors: Monitor[], summary: Record<string, number> }>('/api/monitors', { query: { project: props.projectId }, lazy: true })
const { data: evData, refresh: refreshEvents } = useLiveFetch<{ events: MonitorEvent[] }>('/api/monitor-events', { query: { project: props.projectId, limit: 50 }, lazy: true })
const loaded = computed(() => !!data.value || monStatus.value === 'error')
const monitors = computed(() => data.value?.monitors ?? [])
const events = computed(() => evData.value?.events ?? [])
const q = ref('')
const shown = computed(() => {
  const n = q.value.trim().toLowerCase()
  return n ? monitors.value.filter(m => `${m.name} ${m.target}`.toLowerCase().includes(n)) : monitors.value
})
const avgUptime = computed(() => {
  const list = monitors.value.filter(m => m.uptime_24h !== null)
  return list.length ? list.reduce((a, m) => a + (m.uptime_24h ?? 0), 0) / list.length : null
})

// poll only while visible (kept alive when switching sections)
let poll: ReturnType<typeof setInterval> | undefined
function startPoll() {
  clearInterval(poll)
  poll = setInterval(() => { refresh(); refreshEvents() }, 5000)
}
onMounted(startPoll)
onActivated(() => { refresh(); refreshEvents(); startPoll() })
onDeactivated(() => clearInterval(poll))
onBeforeUnmount(() => clearInterval(poll))

// ---- display ----
const statusMeta = computed(() => ({
  up: { label: t('monitor.status.up'), color: 'success' as const },
  down: { label: t('monitor.status.down'), color: 'error' as const },
  pending: { label: t('monitor.status.pending'), color: 'neutral' as const },
  paused: { label: t('monitor.status.paused'), color: 'neutral' as const }
}))
const typeMeta = computed<Record<Monitor['type'], { label: string, icon: string }>>(() => ({
  http: { label: t('monitor.type.http'), icon: 'i-lucide-globe' },
  tcp: { label: t('monitor.type.tcp'), icon: 'i-lucide-network' },
  heartbeat: { label: t('monitor.type.heartbeat'), icon: 'i-lucide-heart-pulse' },
  process: { label: t('monitor.type.process'), icon: 'i-lucide-square-terminal' },
  container: { label: t('monitor.type.container'), icon: 'i-lucide-container' }
}))
const maxLatency = computed(() => Math.max(1, ...monitors.value.map(m => m.avg_latency_ms)))
const pct = (v: number | null) => v === null ? '—' : `${v >= 99.95 ? 100 : v.toFixed(v >= 99 ? 2 : 1)}%`
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', second: '2-digit', day: '2-digit', month: '2-digit' })
const every = (s: number) => s < 60 ? t('monitor.every.seconds', { n: s }) : s < 3600 ? t('monitor.every.minutes', { n: Math.round(s / 60) }) : t('monitor.every.hours', { n: Math.round(s / 3600) })
function targetLabel(m: Monitor) {
  if (m.type === 'process') return procs.value.find(p => p.id === m.target)?.name ?? t('monitor.processDeleted')
  if (m.type === 'heartbeat') return t('monitor.everyLabel', { every: every(m.interval_s) })
  return m.target
}
const heartbeatURL = (m: Monitor) => m.heartbeat_path ? `${window.location.origin}${m.heartbeat_path}` : ''

// ---- sources for the form & suggestions ----
const { data: procData } = useLiveFetch<{ processes: Proc[] }>(() => `/api/projects/${props.projectId}/processes`, { lazy: true })
const procs = computed(() => procData.value?.processes ?? [])
const { data: composeData } = useLiveFetch<ComposeView>(() => `/api/projects/${props.projectId}/compose`, { lazy: true })

// ---- actions ----
async function patch(m: Monitor, body: Record<string, unknown>) {
  try {
    await $fetch(`/api/monitors/${m.id}`, { method: 'PATCH', body })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function checkNow(m: Monitor) {
  try {
    await $fetch(`/api/monitors/${m.id}/check`, { method: 'POST' })
    await Promise.all([refresh(), refreshEvents()])
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function remove(m: Monitor) {
  if (!confirm(t('monitor.confirmDelete', { name: m.name }))) return
  try {
    await $fetch(`/api/monitors/${m.id}`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
function rowMenu(m: Monitor) {
  return [[
    { label: t('monitor.menu.checkNow'), icon: 'i-lucide-refresh-cw', onSelect: () => checkNow(m) },
    { label: m.enabled ? t('monitor.menu.pause') : t('monitor.menu.resume'), icon: m.enabled ? 'i-lucide-pause' : 'i-lucide-play', onSelect: () => patch(m, { enabled: !m.enabled }) },
    { label: t('monitor.menu.edit'), icon: 'i-lucide-pencil', onSelect: () => openForm(m) },
    ...(m.heartbeat_path ? [{ label: t('monitor.menu.copyHeartbeat'), icon: 'i-lucide-copy', onSelect: () => copy(heartbeatURL(m)) }] : [])
  ], [{ label: t('monitor.menu.delete'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => remove(m) }]]
}
async function copy(text: string) {
  if (await copyText(text)) toast.add({ title: t('monitor.copied'), color: 'success' })
  else toast.add({ title: text, color: 'neutral', duration: 0 })
}

// ---- form ----
const formOpen = ref(false)
const editing = ref<Monitor | null>(null)
const created = ref<Monitor | null>(null)
const saving = ref(false)
const form = reactive({
  name: '', type: 'http' as Monitor['type'], target: '', interval_s: 60, expect_status: '', keyword: '', timeout_ms: 10000,
  file: '', ai_enabled: false, ai_budget_usd: 0.5
})
const intervals = computed(() => [
  { label: t('monitor.interval.30s'), value: 30 },
  { label: t('monitor.interval.1m'), value: 60 },
  { label: t('monitor.interval.5m'), value: 300 },
  { label: t('monitor.interval.15m'), value: 900 },
  { label: t('monitor.interval.1h'), value: 3600 }
])
function openForm(m?: Monitor) {
  editing.value = m ?? null
  created.value = null
  Object.assign(form, m
    ? { name: m.name, type: m.type, target: m.target, interval_s: m.interval_s, expect_status: m.config.expect_status ?? '', keyword: m.config.keyword ?? '',
        timeout_ms: m.config.timeout_ms || 10000, file: m.config.file ?? '', ai_enabled: m.ai_enabled, ai_budget_usd: m.ai_budget_usd }
    : { name: '', type: 'http', target: '', interval_s: 60, expect_status: '', keyword: '', timeout_ms: 10000, file: composeData.value?.file ?? '', ai_enabled: false, ai_budget_usd: 0.5 })
  formOpen.value = true
}
async function saveForm() {
  if (saving.value) return
  const body = {
    name: form.name, type: form.type, target: form.target, interval_s: form.interval_s, ai_enabled: form.ai_enabled, ai_budget_usd: Number(form.ai_budget_usd) || 0,
    config: { expect_status: form.expect_status || undefined, keyword: form.keyword || undefined, timeout_ms: Number(form.timeout_ms) || undefined, file: form.type === 'container' ? form.file : undefined }
  }
  saving.value = true
  try {
    const m = editing.value
      ? await $fetch<Monitor>(`/api/monitors/${editing.value.id}`, { method: 'PATCH', body })
      : await $fetch<Monitor>(`/api/projects/${props.projectId}/monitors`, { method: 'POST', body })
    await refresh()
    if (!editing.value && m.type === 'heartbeat') created.value = m // show the push URL
    else formOpen.value = false
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}

// ---- suggestions from processes and compose services ----
interface Suggest { key: string, name: string, type: Monitor['type'], target: string, file?: string, hint: string }
const suggestOpen = ref(false)
const picked = ref<Set<string>>(new Set())
const suggestions = computed<Suggest[]>(() => {
  const have = new Set(monitors.value.map(m => m.name))
  const out: Suggest[] = []
  for (const p of procs.value.filter(p => p.kind === 'service')) {
    out.push({ key: 'p' + p.id, name: t('monitor.suggest.processLabel', { name: p.name }), type: 'process', target: p.id, hint: t('monitor.suggest.processHint') })
    if (p.state.port) out.push({ key: 'h' + p.id, name: t('monitor.suggest.httpLabel', { name: p.name }), type: 'http', target: `http://localhost:${p.state.port}`, hint: `http://localhost:${p.state.port}` })
  }
  for (const s of composeData.value?.services ?? []) {
    out.push({ key: 'c' + s.name, name: t('monitor.suggest.containerLabel', { name: s.name }), type: 'container', target: s.name, file: composeData.value?.file, hint: t('monitor.suggest.containerHint') })
    for (const port of s.container?.ports ?? []) {
      out.push({ key: `t${s.name}${port.published}`, name: t('monitor.suggest.tcpLabel', { name: s.name, port: port.published }), type: 'tcp', target: `localhost:${port.published}`, hint: t('monitor.suggest.tcpHint', { port: port.published }) })
    }
  }
  return out.filter(s => !have.has(s.name))
})
function openSuggest() {
  picked.value = new Set(suggestions.value.filter(s => s.type !== 'tcp').map(s => s.key))
  suggestOpen.value = true
}
function toggle(k: string) {
  const s = new Set(picked.value)
  if (s.has(k)) s.delete(k)
  else s.add(k)
  picked.value = s
}
async function addSuggested() {
  for (const s of suggestions.value.filter(s => picked.value.has(s.key))) {
    try {
      await $fetch(`/api/projects/${props.projectId}/monitors`, {
        method: 'POST', body: { name: s.name, type: s.type, target: s.target, interval_s: 60, config: s.file ? { file: s.file } : {} }
      })
    } catch (e) {
      toast.add({ title: `${s.name}: ${apiError(e)}`, color: 'error' })
    }
  }
  suggestOpen.value = false
  setTimeout(refresh, 1500)
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <slot name="nav" />
      <UInput v-if="monitors.length" v-model="q" icon="i-lucide-search" :placeholder="t('monitor.search')" size="sm" class="w-48" />
      <div v-if="isAdmin" class="ms-auto flex gap-2">
        <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-wand-sparkles" :label="t('monitor.suggest')" :disabled="!suggestions.length" @click="openSuggest" />
        <UButton size="sm" icon="i-lucide-plus" :label="t('monitor.add')" @click="openForm()" />
      </div>
    </div>

    <!-- summary -->
    <div v-if="monitors.length" class="grid grid-cols-2 overflow-hidden rounded-lg border border-(--ui-border) md:grid-cols-4">
      <div class="p-4">
        <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('monitor.summary.up') }}</p>
        <p class="mt-1 font-mono text-2xl font-semibold text-(--ui-success)">{{ data?.summary.up ?? 0 }} <span class="text-base text-(--ui-text-muted)">/ {{ monitors.length }}</span></p>
      </div>
      <div class="border-s border-(--ui-border) p-4">
        <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('monitor.summary.down') }}</p>
        <p class="mt-1 font-mono text-2xl font-semibold" :class="data?.summary.down ? 'text-(--ui-error)' : ''">{{ data?.summary.down ?? 0 }}</p>
      </div>
      <div class="border-t border-(--ui-border) p-4 md:border-s md:border-t-0">
        <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('monitor.summary.pendingPaused') }}</p>
        <p class="mt-1 font-mono text-2xl font-semibold">{{ (data?.summary.pending ?? 0) + (data?.summary.paused ?? 0) }}</p>
      </div>
      <div class="border-s border-t border-(--ui-border) p-4 md:border-t-0">
        <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('monitor.summary.avgUptime') }}</p>
        <p class="mt-1 font-mono text-2xl font-semibold text-(--ui-success)">{{ pct(avgUptime) }}</p>
      </div>
    </div>

    <div v-if="!loaded" class="space-y-2">
      <div class="h-20 animate-pulse rounded-lg border border-(--ui-border) bg-(--ui-bg-elevated)/50" />
      <div class="h-64 animate-pulse rounded-lg border border-(--ui-border) bg-(--ui-bg-elevated)/50" />
    </div>
    <div v-else-if="!monitors.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
      <UIcon name="i-lucide-heart-pulse" class="mx-auto size-8 text-(--ui-text-dimmed)" />
      <p class="mt-2 font-medium">{{ t('monitor.empty.title') }}</p>
      <p class="text-sm text-(--ui-text-muted)">{{ t('monitor.empty.desc') }}</p>
      <div v-if="isAdmin" class="mt-4 flex justify-center gap-2">
        <UButton v-if="suggestions.length" icon="i-lucide-wand-sparkles" :label="t('monitor.suggestFromOps')" @click="openSuggest" />
        <UButton color="neutral" variant="outline" icon="i-lucide-plus" :label="t('monitor.add')" @click="openForm()" />
      </div>
    </div>

    <div v-else class="grid gap-4 2xl:grid-cols-[minmax(0,1fr)_22rem]">
      <!-- monitors -->
      <div class="overflow-x-auto rounded-lg border border-(--ui-border)">
        <table class="w-full min-w-[46rem] text-sm">
          <thead class="text-left text-xs tracking-wide text-(--ui-text-muted) uppercase">
            <tr class="border-b border-(--ui-border)">
              <th class="px-4 py-2.5 font-medium">{{ t('monitor.table.monitor') }}</th>
              <th class="whitespace-nowrap px-3 py-2.5 font-medium">{{ t('monitor.table.uptime24h') }}</th>
              <th class="px-3 py-2.5 font-medium">{{ t('monitor.table.latency') }}</th>
              <th class="px-3 py-2.5 font-medium">{{ t('monitor.table.trend') }}</th>
              <th class="px-3 py-2.5 font-medium">
                <UTooltip :text="t('monitor.aiTooltip')">
                  <span class="inline-flex cursor-help items-center gap-1">AI <UIcon name="i-lucide-info" class="size-3" /></span>
                </UTooltip>
              </th>
              <th />
            </tr>
          </thead>
          <tbody class="divide-y divide-(--ui-border)">
            <tr v-for="m in shown" :key="m.id" :class="{ 'opacity-60': !m.enabled }">
              <td class="px-4 py-3">
                <div class="flex items-center gap-3">
                  <span class="grid size-8 shrink-0 place-items-center rounded-md border border-(--ui-border)">
                    <UIcon :name="typeMeta[m.type].icon" class="size-4 text-(--ui-text-muted)" />
                  </span>
                  <div class="min-w-0">
                    <div class="flex items-center gap-2">
                      <UBadge :color="statusMeta[m.status].color" variant="subtle" size="sm" :label="statusMeta[m.status].label" />
                      <span class="truncate font-medium">{{ m.name }}</span>
                    </div>
                    <p class="truncate text-xs text-(--ui-text-muted)" :title="m.last_message">
                      {{ typeMeta[m.type].label }} · {{ targetLabel(m) }}<template v-if="m.last_message"> · {{ m.last_message }}</template>
                    </p>
                  </div>
                </div>
              </td>
              <td class="px-3 py-3 font-mono tabular-nums" :class="m.uptime_24h !== null && m.uptime_24h < 99 ? 'text-(--ui-error)' : 'text-(--ui-success)'">{{ pct(m.uptime_24h) }}</td>
              <td class="px-3 py-3">
                <div v-if="m.avg_latency_ms" class="flex items-center gap-2">
                  <span class="h-1.5 w-12 overflow-hidden rounded-full bg-(--ui-bg-elevated)">
                    <span class="block h-full rounded-full bg-(--ui-success)" :style="{ width: `${Math.max(4, (m.avg_latency_ms / maxLatency) * 100)}%` }" />
                  </span>
                  <span class="whitespace-nowrap font-mono text-xs tabular-nums">{{ m.avg_latency_ms }} ms</span>
                </div>
                <span v-else class="text-(--ui-text-dimmed)">—</span>
              </td>
              <td class="px-3 py-3">
                <div class="flex h-5 items-end gap-px">
                  <span v-for="i in Math.max(0, 30 - m.trend.length)" :key="'e' + i" class="h-full w-1 rounded-sm bg-(--ui-bg-elevated)" />
                  <span
                    v-for="(p, i) in m.trend" :key="i" class="h-full w-1 rounded-sm"
                    :class="p.ok ? 'bg-(--ui-success)' : 'bg-(--ui-error)'" :title="t('monitor.trendTooltip', { time: when(p.at), status: p.ok ? t('monitor.ok') : t('monitor.error'), latency: p.latency_ms, message: p.message })"
                  />
                </div>
              </td>
              <td class="px-3 py-3">
                <USwitch :model-value="m.ai_enabled" size="sm" :disabled="!isAdmin" :aria-label="t('monitor.aiAriaLabel', { name: m.name })" @update:model-value="v => patch(m, { ai_enabled: v })" />
              </td>
              <td class="px-2 py-3 text-right">
                <UDropdownMenu v-if="isAdmin" :items="rowMenu(m)" :content="{ align: 'end' }">
                  <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-ellipsis" :aria-label="t('monitor.actions')" />
                </UDropdownMenu>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- events -->
      <div class="rounded-lg border border-(--ui-border)">
        <p class="flex items-center gap-2 border-b border-(--ui-border) px-4 py-2.5 text-xs font-semibold tracking-wide uppercase">
          <UIcon name="i-lucide-activity" /> {{ t('monitor.recentEvents') }} <UBadge color="neutral" variant="subtle" size="sm" :label="String(events.length)" />
        </p>
        <p v-if="!events.length" class="p-4 text-sm text-(--ui-text-muted)">{{ t('monitor.emptyEvents') }}</p>
        <div class="max-h-[36rem] divide-y 2xl:max-h-[36rem] divide-(--ui-border) overflow-auto">
          <div v-for="e in events" :key="e.id" class="space-y-1.5 px-4 py-3">
            <div class="flex items-start gap-2">
              <UBadge :color="e.kind === 'up' ? 'success' : 'error'" variant="subtle" size="sm" :label="e.kind === 'up' ? t('monitor.status.up') : t('monitor.status.down')" />
              <div class="min-w-0 flex-1">
                <p class="text-sm font-medium">{{ e.monitor_name || t('monitor.monitorDeleted') }}</p>
                <p class="text-xs text-(--ui-text-muted)">{{ e.message }}</p>
                <p class="text-xs text-(--ui-text-dimmed)">{{ when(e.at) }}</p>
              </div>
            </div>
            <div v-if="e.analysis_status === 'running'" class="flex items-center gap-2 text-xs text-(--ui-text-muted)">
              <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-primary" /> {{ t('monitor.aiAnalyzing') }}
            </div>
            <details v-else-if="e.analysis_status === 'done' || e.analysis_status === 'failed'" class="rounded-md bg-(--ui-bg-elevated) p-2 text-xs">
              <summary class="cursor-pointer font-medium">
                <UIcon name="i-lucide-bot" class="align-middle" /> {{ t('monitor.aiAnalysis') }}<span v-if="e.cost_usd" class="font-normal text-(--ui-text-muted)"> · ${{ e.cost_usd.toFixed(3) }}</span>
              </summary>
              <!-- eslint-disable-next-line vue/no-v-html -->
              <div class="markdown mt-2" v-html="renderMarkdown(e.analysis, false)" />
            </details>
            <p v-else-if="e.analysis_status === 'skipped'" class="text-xs text-(--ui-text-dimmed)">{{ e.analysis }}</p>
            <UButton
              v-if="e.kind === 'down' && isAdmin && e.id === events.find(x => x.monitor_id === e.monitor_id)?.id && monitors.find(m => m.id === e.monitor_id)?.status === 'down'"
              size="xs" color="error" variant="soft" icon="i-lucide-wrench" :label="t('monitor.fix')" @click="fixEvent(e)"
            />
          </div>
        </div>
      </div>
    </div>

    <!-- add / edit -->
    <UModal v-model:open="formOpen" :title="created ? t('monitor.form.createdTitle') : editing ? t('monitor.form.editTitle', { name: editing.name }) : t('monitor.form.addTitle')" :ui="{ content: 'max-w-xl' }">
      <template #body>
        <div v-if="created" class="space-y-3 text-sm">
          <p>{{ t('monitor.form.createdDesc', { every: every(created.interval_s) }) }}</p>
          <div class="flex items-center gap-2 rounded-md bg-(--ui-bg-elevated) p-2 font-mono text-xs">
            <span class="min-w-0 flex-1 break-all">{{ heartbeatURL(created) }}</span>
            <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-copy" :aria-label="t('monitor.form.copy')" @click="copy(heartbeatURL(created))" />
          </div>
          <p class="text-xs text-(--ui-text-muted)">{{ t('monitor.form.cronExample') }} <code>* * * * * curl -fsS {{ heartbeatURL(created) }} &gt; /dev/null</code></p>
        </div>
        <form v-else id="monitor-form" class="space-y-4" @submit.prevent="saveForm">
          <div class="flex flex-wrap gap-1.5">
            <button
              v-for="(tm, k) in typeMeta" :key="k" type="button" :disabled="!!editing"
              class="flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-sm transition disabled:opacity-60"
              :class="form.type === k ? 'border-(--ui-primary) bg-(--ui-primary)/5 text-(--ui-primary)' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
              @click="form.type = k"
            >
              <UIcon :name="tm.icon" class="size-4" /> {{ tm.label }}
            </button>
          </div>
          <UFormField :label="t('monitor.form.name')" required>
            <UInput v-model="form.name" class="w-full" :placeholder="t('monitor.form.namePlaceholder')" />
          </UFormField>
          <UFormField v-if="form.type === 'http'" :label="t('monitor.form.url')" required>
            <UInput v-model="form.target" class="w-full font-mono" :placeholder="t('monitor.form.urlPlaceholder')" />
          </UFormField>
          <div v-if="form.type === 'http'" class="grid gap-3 sm:grid-cols-2">
            <UFormField :label="t('monitor.form.expectStatus')" :help="t('monitor.form.expectStatusHelp')">
              <UInput v-model="form.expect_status" class="w-full font-mono" placeholder="200-399" />
            </UFormField>
            <UFormField :label="t('monitor.form.keyword')" :help="t('monitor.form.keywordHelp')">
              <UInput v-model="form.keyword" class="w-full" />
            </UFormField>
          </div>
          <UFormField v-if="form.type === 'tcp'" :label="t('monitor.form.hostPort')" required>
            <UInput v-model="form.target" class="w-full font-mono" placeholder="localhost:6379" />
          </UFormField>
          <UFormField v-if="form.type === 'process'" :label="t('monitor.form.process')" required>
            <USelect v-model="form.target" :items="procs.map(p => ({ label: p.name, value: p.id }))" class="w-full" :placeholder="t('monitor.form.processPlaceholder')" />
          </UFormField>
          <template v-if="form.type === 'container'">
            <UFormField :label="t('monitor.form.service')" required>
              <USelect v-model="form.target" :items="(composeData?.services ?? []).map(s => s.name)" class="w-full" :placeholder="t('monitor.form.servicePlaceholder')" />
            </UFormField>
          </template>
          <p v-if="form.type === 'heartbeat'" class="text-sm text-(--ui-text-muted)">{{ t('monitor.form.heartbeatDesc') }}</p>
          <UFormField :label="form.type === 'heartbeat' ? t('monitor.form.heartbeatInterval') : t('monitor.form.checkInterval')">
            <USelect v-model="form.interval_s" :items="intervals" class="w-48" />
          </UFormField>
          <div class="rounded-lg border border-(--ui-border) p-3">
            <USwitch v-model="form.ai_enabled" :label="t('monitor.form.aiEnabled')" :description="t('monitor.form.aiEnabledDesc')" />
            <UFormField v-if="form.ai_enabled" :label="t('monitor.form.aiBudget')" class="mt-3 w-64">
              <UInputNumber v-model="form.ai_budget_usd" :min="0" :step="0.25" size="sm" />
            </UFormField>
          </div>
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <template v-if="created">
            <UButton :label="t('monitor.form.done')" @click="formOpen = false" />
          </template>
          <template v-else>
            <UButton color="neutral" variant="ghost" :label="t('monitor.form.cancel')" @click="formOpen = false" />
            <UButton type="submit" form="monitor-form" :label="t('monitor.form.save')" :loading="saving" :disabled="saving" />
          </template>
        </div>
      </template>
    </UModal>

    <!-- suggestions -->
    <UModal v-model:open="suggestOpen" :title="t('monitor.suggest.title')" :ui="{ content: 'max-w-xl' }">
      <template #body>
        <p class="mb-3 text-sm text-(--ui-text-muted)">{{ t('monitor.suggest.desc') }}</p>
        <div class="max-h-96 divide-y divide-(--ui-border) overflow-auto rounded-lg border border-(--ui-border)">
          <label v-for="s in suggestions" :key="s.key" class="flex cursor-pointer items-start gap-3 px-3 py-2">
            <UCheckbox :model-value="picked.has(s.key)" class="mt-0.5" @update:model-value="toggle(s.key)" />
            <UIcon :name="typeMeta[s.type].icon" class="mt-0.5 size-4 text-(--ui-text-muted)" />
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium">{{ s.name }}</p>
              <p class="truncate text-xs text-(--ui-text-muted)">{{ s.hint }}</p>
            </div>
          </label>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('monitor.suggest.cancel')" @click="suggestOpen = false" />
          <UButton :label="t('monitor.suggest.add', { n: picked.size })" :disabled="!picked.size" @click="addSuggested" />
        </div>
      </template>
    </UModal>
  </div>
</template>

