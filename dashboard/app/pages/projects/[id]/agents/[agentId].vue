<script setup lang="ts">
import type { Attachment } from '~/components/PromptInput.vue'
import type { Permissions } from '~/composables/useOffice'
// One agent of a project: its numbers, its settings (each card saves on its
// own), what it worked on, and how its settings changed (with a way back).
interface Stats {
  days: number, runs: number, ok: number, errors: number, success_rate: number, cost_usd: number, unknown_cost: number
  input_tokens: number, output_tokens: number, p50_ms: number, p95_ms: number, last_run_at: string | null
  per_day: { day: string, ok: number, errors: number, cost_usd: number }[]
  by_model: { model: string, runs: number, cost_usd: number }[]
  top_errors: { error: string, count: number, last: string }[]
  patches: { total: number, merged: number, pending: number, rejected: number, failed: number }
}
interface Item { kind: 'chat' | 'task', source?: Source, at: string, title: string, status: string, phases?: string[], steps?: number, cost_usd: number, conversation_id?: string, task_id?: string, answers?: number }
interface Change { field: string, before: unknown, after: unknown }
interface Entry { revision_id: string, at: string, actor: string, action: string, created: boolean, changes: Change[], restorable: boolean }

const route = useRoute()
const toast = useToast()
const { isAdmin } = useAuth()
const { t, dateLocale } = useLang()
const projectId = computed(() => route.params.id as string)
const agentId = computed(() => route.params.agentId as string)

const _f1 = useLiveFetch<{ agent: Agent, model: { id: string, name: string, kind: string, repo_id: string }, project?: { id: string, name: string } }>(() => `/api/agents/${agentId.value}`)
const { data, refresh } = _f1
const _f2 = useLiveFetch<{ providers: Provider[] }>('/api/providers')
const { data: provData } = _f2
const _f3 = useLiveFetch<{ model: OrgModel }>(() => `/api/org-models/${data.value?.model.id}`, { immediate: !!data.value })
const { data: modelData } = _f3
await Promise.all([_f1, _f2, _f3]) // started together: one round trip, not 3 (a phone over a VPN)
const agent = computed(() => data.value?.agent)
const providers = computed(() => provData.value?.providers ?? [])
const defaultProvider = computed(() => providers.value.find(p => p.is_default))
const providerOf = (a: { provider_id: string }) => providers.value.find(p => p.id === a.provider_id) ?? defaultProvider.value
const resolvedModel = computed(() => agent.value ? agent.value.llm_model || providerOf(agent.value)?.tier_models[agent.value.model_tier] || '—' : '—')
const others = computed(() => (modelData.value?.model.agents ?? []).filter(a => a.id !== agentId.value))

type Tab = 'overview' | 'config' | 'memory' | 'activity' | 'history'
const tab = computed<Tab>({
  get: () => (['config', 'memory', 'activity', 'history'] as const).find(v => v === route.query.tab) ?? 'overview',
  set: v => navigateTo({ query: { tab: v } }, { replace: true })
})

const usd = (v: number) => v >= 100 ? `$${v.toFixed(0)}` : v >= 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(3)}`
const secs = (ms: number) => ms >= 60000 ? `${(ms / 60000).toFixed(1)}m` : `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)}s`
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })

// ---- overview ----
const days = ref(7)
const { data: statsData } = useLiveFetch<{ stats: Stats }>(() => `/api/agents/${agentId.value}/stats?days=${days.value}`, { lazy: true })
const stats = computed(() => statsData.value?.stats)
const maxDay = computed(() => Math.max(1, ...(stats.value?.per_day.map(d => d.ok + d.errors) ?? [0])))
const maxModel = computed(() => Math.max(0.0001, ...(stats.value?.by_model.map(m => m.cost_usd) ?? [0])))
const hovered = ref<Stats['per_day'][number] | null>(null)
const showTable = ref(false)
const dayLabel = (key: string) => new Date(key + 'T00:00:00').toLocaleDateString(dateLocale.value, { day: '2-digit', month: '2-digit' })
const tickEvery = computed(() => Math.max(1, Math.ceil((stats.value?.per_day.length ?? 7) / 8)))
const tiles = computed(() => {
  const s = stats.value
  if (!s) return []
  return [
    { label: t('agentPage.tileRuns'), value: String(s.runs), sub: s.last_run_at ? t('agentPage.lastRun', { when: when(s.last_run_at) }) : t('agentPage.noRuns') },
    { label: t('agentPage.tileSuccess'), value: s.runs ? `${Math.round(s.success_rate * 100)}%` : '—', sub: t('agentPage.errorsN', { n: s.errors }) },
    { label: t('agentPage.tileCost'), value: usd(s.cost_usd), sub: s.unknown_cost ? t('agentPage.unknownCost', { n: s.unknown_cost }) : `${Math.round((s.input_tokens + s.output_tokens) / 1000)}K tokens` },
    { label: t('agentPage.tileSpeed'), value: s.p95_ms ? secs(s.p95_ms) : '—', sub: s.p50_ms ? t('agentPage.median', { v: secs(s.p50_ms) }) : '' },
    { label: t('agentPage.tileMerged'), value: s.patches.total ? `${s.patches.merged}/${s.patches.total}` : '—', sub: s.patches.pending ? t('agentPage.pendingN', { n: s.patches.pending }) : '' }
  ]
})

// ---- config: three cards, each saved on its own ----
const form = reactive({ name: '', key: '', tier: 'worker' as AgentTier, role: '', description: '', reports_to: [] as string[], instructions: '', provider_id: '', fallback_provider_ids: [] as string[], model_tier: 'balanced' as ModelTier, llm_model: '', effort: '', permissions: { level: 'propose', read_only: false } as Permissions, avatar: {} as AvatarSpec })
const editedFrom = ref('') // the agent as the form was filled: a save over someone else's change is refused
const saveError = useSaveError()
function load() {
  const a = agent.value
  if (!a) return
  editedFrom.value = (a as { version?: string }).version ?? ''
  Object.assign(form, JSON.parse(JSON.stringify({
    name: a.name, key: a.key, tier: a.tier, role: a.role, description: a.description, reports_to: a.reports_to, instructions: a.instructions,
    provider_id: a.provider_id, fallback_provider_ids: a.fallback_provider_ids ?? [], model_tier: a.model_tier, llm_model: a.llm_model, effort: a.effort ?? '', permissions: a.permissions, avatar: a.avatar ?? {}
  })))
}
const { stale, reset: resync } = useDraft(agent, form, () => load()) // never over what is being edited
const DEFAULT_PROVIDER = '__default'
const providerChoice = computed({
  get: () => form.provider_id || DEFAULT_PROVIDER,
  set: (v: string) => { form.provider_id = v === DEFAULT_PROVIDER ? '' : v }
})
const providerOptions = computed(() => [
  { label: t('org.form.providerDefault', { suffix: defaultProvider.value ? ` (${defaultProvider.value.name})` : '' }), value: DEFAULT_PROVIDER },
  ...providers.value.map(p => ({ label: p.name, value: p.id }))
])
const formProvider = computed(() => providers.value.find(p => p.id === form.provider_id) ?? defaultProvider.value)
const providerName = (id: string) => providers.value.find(p => p.id === id)?.name ?? id
const bossOptions = computed(() => others.value.filter(a => a.tier !== 'worker').map(a => ({ label: `${a.name} (${a.key})`, value: a.key })))
const saving = ref('')
const avatarEditing = ref(false)
const cardFields = { role: ['key', 'name', 'tier', 'role', 'description', 'reports_to', 'instructions'], model: ['provider_id', 'fallback_provider_ids', 'model_tier', 'llm_model', 'effort'], perm: ['permissions'], avatar: ['avatar'] } as const
async function save(card: 'role' | 'model' | 'perm' | 'avatar') {
  if (saving.value) return
  const a = agent.value!
  // each card sends its own fields on top of the saved agent
  const base = { version: editedFrom.value, key: a.key, name: a.name, tier: a.tier, role: a.role, description: a.description, reports_to: a.reports_to, provider_id: a.provider_id, model_tier: a.model_tier, llm_model: a.llm_model, instructions: a.instructions, permissions: a.permissions }
  const body = card === 'role'
    ? { ...base, key: form.key, name: form.name, tier: form.tier, role: form.role, description: form.description, reports_to: form.tier === 'lead' ? [] : form.reports_to, instructions: form.instructions }
    : card === 'model' ? { ...base, provider_id: form.provider_id, fallback_provider_ids: form.fallback_provider_ids.filter(id => id !== formProvider.value?.id), model_tier: form.model_tier, llm_model: form.llm_model, effort: form.effort }
      : card === 'avatar' ? { ...base, avatar: form.avatar } : { ...base, permissions: form.permissions }
  saving.value = card
  try {
    await $fetch(`/api/agents/${a.id}`, { method: 'PATCH', body })
    // what is typed in the other cards, not saved yet, stays
    const others = (Object.keys(cardFields) as (keyof typeof cardFields)[]).filter(c => c !== card).flatMap(c => [...cardFields[c]])
    const kept = JSON.parse(JSON.stringify(Object.fromEntries(others.map(k => [k, form[k]]))))
    await refresh()
    resync() // saved: take what the server has now
    Object.assign(form, kept)
    history.value = null
    toast.add({ title: t('org.editor.savedAgent', { name: form.name }), color: 'success' })
  } catch (e) {
    const d = (e as { data?: { code?: string, error?: string, problems?: string[] } })?.data
    if (d?.code === 'conflict') saveError(e, async () => { await refresh(); resync() })
    else toast.add({ title: d?.error ?? apiError(e), description: d?.problems?.join('\n'), color: 'error' })
  } finally {
    saving.value = ''
  }
}
async function remove() {
  if (!agent.value || !confirm(t('org.editor.deleteAgentConfirm', { name: agent.value.name }))) return
  try {
    await $fetch(`/api/agents/${agent.value.id}`, { method: 'DELETE' })
    await navigateTo({ path: `/projects/${projectId.value}`, query: { tab: 'model' } })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// ---- chat / activity ----
const prefill = useState<{ text: string, files: Attachment[], send?: boolean, agentId?: string, conversationId?: string } | null>('chat-prefill', () => null)
function chat(conversationId?: string) {
  prefill.value = { text: '', files: [], agentId: agentId.value, conversationId }
  navigateTo({ path: `/projects/${projectId.value}`, query: { tab: 'chat' } })
}
const activity = ref<Item[] | null>(null)
const history = ref<Entry[] | null>(null)
watch(tab, async (v) => {
  try {
    if (v === 'activity' && !activity.value) activity.value = (await $fetch<{ items: Item[] }>(`/api/agents/${agentId.value}/activity`)).items
    if (v === 'history' && !history.value) history.value = (await $fetch<{ history: Entry[] }>(`/api/agents/${agentId.value}/history`)).history
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}, { immediate: true })
const openItem = (it: Item) => chat(it.conversation_id)
const phaseName = (p: string) => ({ plan: t('phase.plan'), revise: t('phase.revise'), vote: t('phase.vote'), work: t('phase.work'), review: t('phase.review'), synthesize: t('phase.synthesize') } as Record<string, string>)[p] ?? p
const statusColor = (s: string) => s === 'done' ? 'success' : s === 'running' ? 'info' : s === 'needs_input' ? 'warning' : s === 'failed' || s === 'rejected' ? 'error' : 'neutral'

// ---- history ----
const fieldLabel = (f: string) => ({
  name: t('org.form.name'), key: t('org.form.key'), tier: t('org.form.tier'), role: t('org.form.role'), description: t('org.settings.description'),
  reports_to: t('org.form.reportsTo'), provider_id: t('org.form.provider'), fallback_provider_ids: t('org.form.fallbacks'), model_tier: t('org.form.modelTier'), llm_model: t('org.form.specificModel'), effort: t('org.form.effort'),
  instructions: t('org.form.instructions'), permissions: t('org.form.permissions')
} as Record<string, string>)[f] ?? f
// connection ids read as their names (empty main connection = the default one)
const showField = (f: string, v: unknown) => f === 'provider_id'
  ? (v ? providerName(String(v)) : t('org.form.providerDefault', { suffix: '' }))
  : f === 'fallback_provider_ids' && Array.isArray(v) ? show(v.map(id => providerName(String(id))))
    : f === 'effort' ? (v ? effortLabel(String(v), t) : t('effort.default')) : show(v)
const show = (v: unknown): string => v === '' || v == null ? '—' : typeof v === 'string' ? v : Array.isArray(v) ? (v.length ? v.join(', ') : '—') : JSON.stringify(v, null, 1)
// line diff of long text: lines only before (−) and only after (+)
function lines(before: unknown, after: unknown) {
  const b = String(before ?? '').split('\n')
  const a = String(after ?? '').split('\n')
  return [...b.filter(l => !a.includes(l)).map(l => ({ l, k: 'del' })), ...a.filter(l => !b.includes(l)).map(l => ({ l, k: 'add' }))]
}
const restoring = ref('')
async function restore(e: Entry) {
  if (!confirm(t('agentPage.restoreConfirm', { when: when(e.at) }))) return
  restoring.value = e.revision_id
  try {
    await $fetch(`/api/agents/${agentId.value}/restore`, { method: 'POST', body: { revision_id: e.revision_id } })
    await refresh()
    resync() // saved: take what the server has now
    history.value = (await $fetch<{ history: Entry[] }>(`/api/agents/${agentId.value}/history`)).history
    toast.add({ title: t('agentPage.restored'), color: 'success' })
  } catch (err) {
    toast.add({ title: apiError(err), color: 'error' })
  } finally {
    restoring.value = ''
  }
}
</script>

<template>
  <PageShell :title="agent?.name ?? ''">
    <template #actions>
      <UButton v-if="agent" icon="i-lucide-messages-square" size="sm" :label="t('agentPage.chat')" @click="chat()" />
      <UDropdownMenu v-if="isAdmin && agent" :content="{ align: 'end' }" :items="[[{ label: t('org.form.delete'), icon: 'i-lucide-trash', color: 'error', onSelect: remove }]]">
        <UButton icon="i-lucide-ellipsis" color="neutral" variant="ghost" :aria-label="t('project.actionsAria')" />
      </UDropdownMenu>
    </template>

    <div v-if="agent" class="space-y-4">
      <UButton
        :to="{ path: `/projects/${projectId}`, query: { tab: 'model' } }" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost"
        class="-ms-2" :label="t('project.sectionModel')"
      />
      <div class="flex flex-wrap items-center gap-2 text-xs text-(--ui-text-muted)">
        <NuxtLink v-if="data?.project" :to="{ path: `/projects/${projectId}`, query: { tab: 'model' } }" class="hover:text-(--ui-text)">
          {{ data.project.name }} · {{ data.model.name }}
        </NuxtLink>
        <AgentSwitch :agent="agent" @changed="refresh()" />
        <UBadge v-if="agent.enabled === false" :label="t('org.editor.paused')" color="warning" variant="subtle" size="sm" icon="i-lucide-pause" />
        <UBadge :label="tierLabel[agent.tier]" color="neutral" variant="subtle" size="sm" />
        <UBadge :label="resolvedModel" color="neutral" variant="outline" size="sm" icon="i-lucide-cpu" class="font-mono" />
        <UBadge :label="permOf(agentLevel(agent.permissions)).label" :icon="permOf(agentLevel(agent.permissions)).icon" color="neutral" variant="outline" size="sm" />
        <span v-if="agent.role" class="truncate">{{ agent.role }}</span>
      </div>

      <UTabs
        v-model="tab" :content="false" variant="link" class="w-full" :ui="{ list: 'overflow-x-auto', trigger: 'shrink-0' }"
        :items="[
          { label: t('agentPage.tabOverview'), value: 'overview', icon: 'i-lucide-chart-column' },
          { label: t('agentPage.tabConfig'), value: 'config', icon: 'i-lucide-sliders-horizontal' },
          { label: t('agentPage.tabMemory'), value: 'memory', icon: 'i-lucide-notebook-pen' },
          { label: t('agentPage.tabActivity'), value: 'activity', icon: 'i-lucide-activity' },
          { label: t('agentPage.tabHistory'), value: 'history', icon: 'i-lucide-history' }
        ]"
      />

      <!-- overview -->
      <template v-if="tab === 'overview'">
        <div class="flex justify-end">
          <USelect v-model="days" size="xs" :items="[7, 30, 90].map(n => ({ label: t('agentPage.lastDays', { n }), value: n }))" />
        </div>
        <div v-if="stats" class="grid gap-3 sm:grid-cols-3 lg:grid-cols-5">
          <UCard v-for="x in tiles" :key="x.label" :ui="{ body: 'p-3 sm:p-4' }">
            <p class="text-xs text-(--ui-text-muted)">{{ x.label }}</p>
            <p class="mt-1 text-2xl font-semibold tabular-nums">{{ x.value }}</p>
            <p class="mt-0.5 truncate text-xs text-(--ui-text-muted)">{{ x.sub }}</p>
          </UCard>
        </div>

        <UCard v-if="stats">
          <template #header>
            <div class="flex flex-wrap items-center gap-3">
              <p class="font-medium">{{ t('agentPage.runsPerDay') }}</p>
              <span class="flex items-center gap-1 text-xs text-(--ui-text-muted)"><span class="size-2 rounded-sm bg-(--ui-success)" />{{ t('agentPage.ok') }}</span>
              <span class="flex items-center gap-1 text-xs text-(--ui-text-muted)"><span class="size-2 rounded-sm bg-(--ui-error)" />{{ t('agentPage.failed') }}</span>
              <UButton size="xs" color="neutral" variant="ghost" class="ms-auto" :label="showTable ? t('costs.viewChart') : t('costs.viewTable')" @click="showTable = !showTable" />
            </div>
          </template>
          <div v-if="!showTable" class="relative">
            <div class="flex h-40 items-end gap-0.5 border-b border-(--ui-border) pb-px" @mouseleave="hovered = null">
              <div v-for="d in stats.per_day" :key="d.day" class="flex h-full flex-1 cursor-default flex-col justify-end gap-0.5" @mouseenter="hovered = d">
                <div
                  v-if="d.errors" class="w-full rounded-t bg-(--ui-error) transition-opacity" :class="hovered && hovered.day !== d.day ? 'opacity-40' : ''"
                  :style="{ height: `max(2px, ${(d.errors / maxDay) * 100}%)` }"
                />
                <div
                  v-if="d.ok" class="w-full bg-(--ui-success) transition-opacity" :class="[hovered && hovered.day !== d.day ? 'opacity-40' : '', d.errors ? '' : 'rounded-t']"
                  :style="{ height: `max(2px, ${(d.ok / maxDay) * 100}%)` }"
                />
              </div>
            </div>
            <div class="mt-1 flex gap-0.5 text-[10px] text-(--ui-text-muted)">
              <span v-for="(d, i) in stats.per_day" :key="d.day" class="flex-1 text-center">{{ i % tickEvery === 0 ? dayLabel(d.day) : '' }}</span>
            </div>
            <p class="absolute left-0 top-0 text-[10px] text-(--ui-text-muted)">{{ maxDay }}</p>
            <div v-if="hovered" class="pointer-events-none absolute right-0 top-0 rounded-md border border-(--ui-border) bg-(--ui-bg) px-3 py-2 text-xs shadow-sm">
              <p class="font-medium">{{ new Date(hovered.day + 'T00:00:00').toLocaleDateString(dateLocale) }}</p>
              <p class="tabular-nums">{{ t('agentPage.ok') }}: {{ hovered.ok }} · {{ t('agentPage.failed') }}: {{ hovered.errors }}</p>
              <p class="tabular-nums text-(--ui-text-muted)">{{ usd(hovered.cost_usd) }}</p>
            </div>
          </div>
          <table v-else class="w-full text-sm">
            <thead class="text-left text-xs text-(--ui-text-muted)">
              <tr><th class="py-1">{{ t('costs.colDate') }}</th><th>{{ t('agentPage.ok') }}</th><th>{{ t('agentPage.failed') }}</th><th>{{ t('costs.colCost') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="d in [...stats.per_day].reverse()" :key="d.day" class="border-t border-(--ui-border)">
                <td class="py-1">{{ dayLabel(d.day) }}</td><td class="tabular-nums">{{ d.ok }}</td><td class="tabular-nums">{{ d.errors }}</td><td class="tabular-nums">{{ usd(d.cost_usd) }}</td>
              </tr>
            </tbody>
          </table>
        </UCard>

        <div v-if="stats" class="grid gap-4 lg:grid-cols-2">
          <UCard>
            <template #header><p class="font-medium">{{ t('agentPage.byModel') }}</p></template>
            <p v-if="!stats.by_model.length" class="text-sm text-(--ui-text-muted)">{{ t('agentPage.noRuns') }}</p>
            <div v-for="m in stats.by_model" :key="m.model" class="space-y-1 py-1">
              <div class="flex items-center justify-between gap-2 text-sm">
                <code class="truncate text-xs">{{ m.model || '—' }}</code>
                <span class="shrink-0 tabular-nums text-(--ui-text-muted)">{{ usd(m.cost_usd) }} · {{ t('costs.runsUnit', { n: m.runs }) }}</span>
              </div>
              <div class="h-1.5 rounded-full bg-(--ui-bg-elevated)"><div class="h-full rounded-full bg-(--ui-primary)" :style="{ width: `${(m.cost_usd / maxModel) * 100}%` }" /></div>
            </div>
          </UCard>
          <UCard>
            <template #header><p class="font-medium">{{ t('agentPage.topErrors') }}</p></template>
            <p v-if="!stats.top_errors.length" class="flex items-center gap-1.5 text-sm text-(--ui-text-muted)"><UIcon name="i-lucide-circle-check" class="size-4 text-(--ui-success)" />{{ t('agentPage.noErrors') }}</p>
            <div v-for="e in stats.top_errors" :key="e.error" class="flex items-start gap-2 py-1 text-sm">
              <UIcon name="i-lucide-triangle-alert" class="mt-0.5 size-4 shrink-0 text-(--ui-error)" />
              <span class="min-w-0 flex-1 break-words">{{ e.error }}</span>
              <span class="shrink-0 text-xs tabular-nums text-(--ui-text-muted)">×{{ e.count }} · {{ when(e.last) }}</span>
            </div>
          </UCard>
        </div>
      </template>

      <!-- config -->
      <fieldset v-else-if="tab === 'config'" :disabled="!isAdmin" class="max-w-4xl space-y-3">
        <StaleNotice :show="stale" @reload="resync" />
        <!-- avatar: just the picture until the person edits it -->
        <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
          <div class="flex items-center gap-3">
            <AgentAvatar :agent="{ ...agent, avatar: form.avatar }" size="md" />
            <p class="flex-1 text-sm font-medium">{{ t('avatar.title') }}</p>
            <template v-if="isAdmin">
              <template v-if="avatarEditing">
                <UButton size="xs" color="neutral" variant="ghost" :label="t('common.cancel')" @click="form.avatar = agent?.avatar ?? {}; avatarEditing = false" />
                <UButton size="xs" icon="i-lucide-save" :label="t('common.save')" :loading="saving === 'avatar'" :disabled="!!saving" @click="save('avatar').then(() => { avatarEditing = false })" />
              </template>
              <UButton v-else size="xs" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('avatar.edit')" @click="avatarEditing = true" />
            </template>
          </div>
          <AvatarPicker v-if="agent && avatarEditing" v-model="form.avatar" :agent="agent" />
        </UCard>
        <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
          <p class="text-sm font-medium">{{ t('agentPage.cardRole') }}</p>
          <div class="grid gap-3 sm:grid-cols-2">
            <UFormField :label="t('org.form.name')" required><UInput v-model="form.name" class="w-full" /></UFormField>
            <UFormField :label="t('org.form.key')" required><UInput v-model="form.key" class="w-full font-mono" /></UFormField>
            <UFormField :label="t('org.form.tier')">
              <USelect v-model="form.tier" :items="Object.entries(tierLabel).map(([value, label]) => ({ label, value }))" class="w-full" />
            </UFormField>
            <UFormField v-if="form.tier !== 'lead'" :label="t('org.form.reportsTo')">
              <USelectMenu v-model="form.reports_to" multiple value-key="value" :items="bossOptions" class="w-full" />
            </UFormField>
          </div>
          <UFormField :label="t('org.form.role')"><UInput v-model="form.role" class="w-full" :placeholder="t('org.form.rolePlaceholder')" /></UFormField>
          <UFormField :label="t('org.settings.description')"><UTextarea v-model="form.description" :rows="2" class="w-full" autoresize /></UFormField>
          <UFormField :label="t('org.form.instructions')"><UTextarea v-model="form.instructions" :rows="8" class="w-full font-mono text-xs" autoresize /></UFormField>
          <UButton v-if="isAdmin" size="sm" icon="i-lucide-save" :label="t('org.form.save')" :loading="saving === 'role'" :disabled="!!saving" @click="save('role')" />
        </UCard>

        <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
          <p class="text-sm font-medium">{{ t('agentPage.cardModel') }}</p>
          <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <UFormField :label="t('org.form.provider')"><USelect v-model="providerChoice" :items="providerOptions" class="w-full" /></UFormField>
            <UFormField :label="t('org.form.modelTier')">
              <USelect v-model="form.model_tier" :items="Object.entries(modelTierLabel).map(([value, label]) => ({ label, value }))" class="w-full" />
            </UFormField>
            <!-- the tier's model as the placeholder: a hint beside the label pushed it onto two lines -->
            <UFormField :label="t('org.form.specificModel')">
              <UInput v-model="form.llm_model" list="agent-page-models" :placeholder="formProvider?.tier_models[form.model_tier] || ''" class="w-full font-mono" />
              <datalist id="agent-page-models"><option v-for="m in formProvider?.models ?? []" :key="m" :value="m" /></datalist>
            </UFormField>
            <UFormField :label="t('org.form.effort')">
              <template #hint>
                <UTooltip :text="t('org.form.effortHint')"><UIcon name="i-lucide-info" class="size-4 text-(--ui-text-muted)" /></UTooltip>
              </template>
              <EffortSelect v-model="form.effort" class="w-full" :disabled="!isAdmin" />
            </UFormField>
          </div>
          <FallbackPicker v-model="form.fallback_provider_ids" :providers="providers" :main-id="formProvider?.id" :disabled="!isAdmin" />
          <UButton v-if="isAdmin" size="sm" icon="i-lucide-save" :label="t('org.form.save')" :loading="saving === 'model'" :disabled="!!saving" @click="save('model')" />
        </UCard>

        <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
          <p class="text-sm font-medium">{{ t('org.form.permissions') }}</p>
          <AgentPermEditor v-model="form.permissions" :project-id="projectId" :disabled="!isAdmin" />
          <UButton v-if="isAdmin" size="sm" icon="i-lucide-save" :label="t('org.form.save')" :loading="saving === 'perm'" :disabled="!!saving" @click="save('perm')" />
        </UCard>
      </fieldset>

      <!-- long-term notes (ADR-068) -->
      <AgentMemory v-else-if="tab === 'memory'" :project-id="projectId" :agent-id="agentId" />

      <!-- activity -->
      <UCard v-else-if="tab === 'activity'" :ui="{ body: 'p-0 sm:p-0' }">
        <LoadingRows v-if="!activity" :n="5" />
        <p v-else-if="!activity.length" class="p-4 text-sm text-(--ui-text-muted)">{{ t('agentPage.noActivity') }}</p>
        <!-- one row per place it took part in -->
        <div v-for="it in activity ?? []" :key="(it.conversation_id ?? '') + (it.task_id ?? '')" class="border-b border-(--ui-border) last:border-0">
          <div class="flex items-center gap-3 px-4 py-2.5">
            <UIcon
              :name="it.kind === 'chat' ? (it.source && it.source !== 'web' ? sourceIcon[it.source] : 'i-lucide-messages-square') : 'i-lucide-list-todo'"
              class="size-4 shrink-0 text-(--ui-text-muted)"
            />
            <button type="button" class="min-w-0 flex-1 text-left" @click="openItem(it)">
              <span class="block truncate text-sm hover:underline">{{ it.title || t('agentPage.untitled') }}</span>
              <span class="block truncate text-xs text-(--ui-text-muted)">
                <template v-if="it.kind === 'task'">{{ t('agentPage.taskSteps', { n: it.steps ?? 0, phases: (it.phases ?? []).map(p => phaseName(p)).join(', ') }) }}</template>
                <template v-else>{{ t('agentPage.answers', { n: it.answers ?? 0 }) }}</template>
                · {{ when(it.at) }}
              </span>
            </button>
            <UBadge v-if="it.status" :label="it.status" :color="statusColor(it.status)" variant="subtle" size="sm" class="max-sm:hidden" />
            <span v-if="it.cost_usd" class="text-xs tabular-nums text-(--ui-text-muted) max-sm:hidden">{{ usd(it.cost_usd) }}</span>
          </div>
        </div>
      </UCard>

      <!-- history -->
      <div v-else class="max-w-4xl space-y-3">
        <UCard v-if="!history" :ui="{ body: 'p-0 sm:p-0' }"><LoadingRows :n="3" :icon="false" /></UCard>
        <p v-else-if="!history.length" class="text-sm text-(--ui-text-muted)">{{ t('agentPage.noHistory') }}</p>
        <UCard v-for="e in history ?? []" :key="e.revision_id" :ui="{ body: 'space-y-2 sm:p-4' }">
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <UIcon :name="e.created ? 'i-lucide-circle-plus' : 'i-lucide-pencil'" class="size-4 text-(--ui-text-muted)" />
            <span class="font-medium">{{ e.created ? t('agentPage.created') : t('agentPage.changedN', { n: e.changes.length }) }}</span>
            <span class="text-xs text-(--ui-text-muted)">{{ when(e.at) }} · {{ e.actor || '—' }}</span>
            <UButton
              v-if="isAdmin && e.restorable" size="xs" color="neutral" variant="outline" icon="i-lucide-undo-2" class="ms-auto"
              :label="t('agentPage.restore')" :loading="restoring === e.revision_id" @click="restore(e)"
            />
          </div>
          <div v-for="c in e.changes" :key="c.field" class="rounded-md bg-(--ui-bg-elevated)/60 px-3 py-2 text-xs">
            <p class="mb-1 font-medium text-(--ui-text-muted)">{{ fieldLabel(c.field) }}</p>
            <pre v-if="c.field === 'instructions' || c.field === 'description'" class="whitespace-pre-wrap leading-5"><span
              v-for="(x, i) in lines(c.before, c.after)" :key="i" class="block"
              :class="x.k === 'add' ? 'text-(--ui-success)' : 'text-(--ui-error) line-through'"
            >{{ x.k === 'add' ? '+ ' : '− ' }}{{ x.l || ' ' }}</span></pre>
            <div v-else class="flex flex-wrap items-center gap-2">
              <code class="whitespace-pre-wrap text-(--ui-error) line-through">{{ showField(c.field, c.before) }}</code>
              <UIcon name="i-lucide-arrow-right" class="size-3.5 text-(--ui-text-dimmed)" />
              <code class="whitespace-pre-wrap text-(--ui-success)">{{ showField(c.field, c.after) }}</code>
            </div>
          </div>
        </UCard>
      </div>
    </div>
  </PageShell>
</template>
