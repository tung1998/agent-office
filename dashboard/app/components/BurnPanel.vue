<script setup lang="ts">
// A project's Burn (spec 2026-10-01-burn-design): its main agent runs on its
// own with full access, finding work and doing it, one piece at a time in its
// own worktree, until stopped or its time is up. This page runs it; the chat
// page shows its conversation.
interface Burn {
  id?: string, conversation_id?: string, agent_id: string, model_tier: 'strong' | 'balanced' | 'fast', max_subagents: number,
  result_mode: 'branch' | 'patch', focus: string, order: 'roadmap' | 'bugs' | 'auto', ends_at: string | null, state: 'running' | 'stopped' | 'waiting_limit',
  waiting_until?: string, started_by?: string, started_at?: string
}
interface Item {
  id: string, title: string, kind: 'unfinished' | 'upgrade' | 'bug', detail: string, status: string, priority: number,
  branch: string, worktree: string, summary: string, subagents: number, cost_usd: number, updated_at: string
}
interface AgentLite { id: string, name: string, tier: string, enabled?: boolean }
const props = defineProps<{ projectId: string }>()
const { t, dateLocale } = useLang()
const toast = useToast()
const copy = useCopy()

const base = computed(() => `/api/projects/${props.projectId}/burn`)
const { data, refresh, error: loadError } = useLiveFetch<{ burn: Burn, items: Item[], weekly_reset?: string }>(base)
const { data: agentsData } = useLiveFetch<{ agents: AgentLite[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const burn = computed(() => data.value?.burn)
const items = computed(() => data.value?.items ?? [])
const agents = computed(() => agentsData.value?.agents ?? [])
const running = computed(() => burn.value?.state === 'running' || burn.value?.state === 'waiting_limit')

// the time left, ticking
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => { clock = setInterval(() => { now.value = Date.now() }, 1000) })
onBeforeUnmount(() => clearInterval(clock))
const when = (iso: string) => new Date(iso).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
function left(iso: string) {
  const s = Math.max(0, Math.floor((new Date(iso).getTime() - now.value) / 1000))
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60)
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m ${s % 60}s`
}

// settings (a drawer): saved as they are
const settingsOpen = ref(false)
const form = reactive({ agent_id: '', model_tier: 'balanced' as Burn['model_tier'], max_subagents: 2, result_mode: 'branch' as Burn['result_mode'], order: 'roadmap' as Burn['order'], focus: '' })
const { stale, reset: resync } = useDraft(burn, form, (b) => {
  Object.assign(form, { agent_id: b.agent_id, model_tier: b.model_tier, max_subagents: b.max_subagents, result_mode: b.result_mode, order: b.order ?? 'roadmap', focus: b.focus })
})
const saving = ref(false)
async function saveSettings() {
  saving.value = true
  try {
    await $fetch(base.value, { method: 'PUT', body: { ...form } })
    await refresh()
    resync()
    settingsOpen.value = false
    toast.add({ title: t('burn.saved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
// the agent picked is paused: saving and starting are refused until it is changed or turned on
const offAgent = computed(() => agents.value.find(a => a.id === form.agent_id && a.enabled === false))
const agentItems = computed(() => agents.value.map(a => ({ value: a.id, label: a.enabled === false ? `${a.name} (${t('burn.agentOffTag')})` : a.name })))
const tierItems = computed(() => (['strong', 'balanced', 'fast'] as const).map(v => ({ value: v, label: t(`burn.tier.${v}`) })))

// starting asks first: what it means, and when it stops
const confirmOpen = ref(false)
const endMode = ref<'reset' | 'hours' | 'at' | 'none'>('reset')
const endHours = ref(4)
const endAt = ref('')
function openStart() {
  endMode.value = data.value?.weekly_reset ? 'reset' : 'hours'
  const d = new Date(Date.now() + 4 * 3600e3)
  endAt.value = new Date(d.getTime() - d.getTimezoneOffset() * 60e3).toISOString().slice(0, 16)
  confirmOpen.value = true
}
const endsAt = computed<string | null>(() => {
  switch (endMode.value) {
    case 'reset': return data.value?.weekly_reset ?? null
    case 'hours': return new Date(Date.now() + Math.max(1, endHours.value) * 3600e3).toISOString()
    case 'at': return endAt.value ? new Date(endAt.value).toISOString() : null
    default: return null
  }
})
const acting = ref('')
async function start() {
  acting.value = 'start'
  try {
    await $fetch(`${base.value}/start`, { method: 'POST', body: { ...form, ends_at: endsAt.value ?? undefined, no_end: endMode.value === 'none' } })
    confirmOpen.value = false
    await refresh()
    toast.add({ title: t('burn.started'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.value = ''
  }
}
async function stop() {
  acting.value = 'stop'
  try {
    await $fetch(`${base.value}/stop`, { method: 'POST' })
    await refresh()
    toast.add({ title: t('burn.stopped'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.value = ''
  }
}

// the board
const columns = computed(() => [
  { key: 'found', label: t('burn.col.found'), statuses: ['found'] },
  { key: 'doing', label: t('burn.col.doing'), statuses: ['doing', 'queued'] },
  { key: 'paused', label: t('burn.col.paused'), statuses: ['paused'] },
  { key: 'done', label: t('burn.col.done'), statuses: ['done'] },
  { key: 'other', label: t('burn.col.other'), statuses: ['failed', 'skipped'] }
].map(c => ({ ...c, items: items.value.filter(i => c.statuses.includes(i.status)) })))
const kindColor = (k: Item['kind']) => ({ bug: 'error', unfinished: 'warning', upgrade: 'info' } as const)[k]
const itemActing = ref('')
async function itemAction(it: Item, action: 'skip' | 'first' | 'drop-worktree') {
  if (action === 'drop-worktree' && !confirm(t('burn.dropConfirm', { branch: it.branch }))) return
  itemActing.value = it.id + action
  try {
    await $fetch(`/api/burn-items/${it.id}/${action}`, { method: 'POST' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    itemActing.value = ''
  }
}
const totalCost = computed(() => items.value.reduce((n, i) => n + i.cost_usd, 0))
</script>

<template>
  <div class="space-y-4">
    <UAlert v-if="loadError" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
        <p class="flex items-center gap-2 font-semibold">
          <UIcon name="i-lucide-flame" class="size-5" :class="running ? 'text-(--ui-warning)' : 'text-(--ui-text-muted)'" />{{ t('burn.title') }}
        </p>
        <UBadge v-if="burn" :label="t(`burn.state.${burn.state}`)" :color="burn.state === 'running' ? 'warning' : burn.state === 'waiting_limit' ? 'info' : 'neutral'" variant="subtle" />
        <span v-if="running && burn?.ends_at" class="text-xs tabular-nums text-(--ui-text-muted)">{{ t('burn.endsIn', { left: left(burn.ends_at), at: when(burn.ends_at) }) }}</span>
        <span v-else-if="running" class="text-xs text-(--ui-text-muted)">{{ t('burn.noEnd') }}</span>
        <span v-if="burn?.state === 'waiting_limit' && burn.waiting_until" class="text-xs text-(--ui-text-muted)">{{ t('burn.waitingUntil', { at: when(burn.waiting_until) }) }}</span>
        <div class="ms-auto flex flex-wrap items-center gap-1.5">
          <UButton v-if="burn?.conversation_id" size="sm" color="neutral" variant="ghost" icon="i-lucide-messages-square" :label="t('burn.openChat')" :to="`/projects/${projectId}?tab=chat&c=${burn.conversation_id}`" />
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-settings-2" :label="t('burn.settings')" @click="settingsOpen = true" />
          <UButton v-if="running" size="sm" color="neutral" icon="i-lucide-square" :label="t('burn.stop')" :loading="acting === 'stop'" @click="stop" />
          <UButton v-else size="sm" color="warning" icon="i-lucide-flame" :label="t('burn.start')" @click="openStart" />
        </div>
      </div>
      <p class="text-xs text-(--ui-text-muted)">
        {{ t('burn.summary', { tier: t(`burn.tier.${form.model_tier}`), n: form.max_subagents, mode: t(`burn.mode.${form.result_mode}`) }) }} · {{ t(`burn.order.${form.order}`) }}
        <template v-if="items.length"> · {{ t('burn.cost', { usd: totalCost.toFixed(2), n: items.filter(i => i.status === 'done').length }) }}</template>
      </p>
      <UAlert v-if="offAgent" color="warning" variant="subtle" icon="i-lucide-power-off" :title="t('burn.agentOff', { name: offAgent.name })"
        :actions="[{ label: t('burn.agentOffChange'), color: 'warning', variant: 'outline', onClick: () => { settingsOpen = true } }, { label: t('burn.agentOffOpen'), color: 'neutral', variant: 'ghost', to: `/projects/${projectId}/agents/${offAgent.id}` }]" />
      <p v-if="form.focus" class="text-xs"><span class="text-(--ui-text-muted)">{{ t('burn.focus') }}:</span> {{ form.focus }}</p>
    </UCard>

    <!-- the pieces of work, by where they are -->
    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
      <div v-for="col in columns" :key="col.key" class="min-w-0 space-y-2 rounded-xl border border-(--ui-border) p-2">
        <p class="flex items-center gap-2 px-1 text-xs font-medium text-(--ui-text-muted)">{{ col.label }}<UBadge :label="String(col.items.length)" color="neutral" variant="subtle" size="sm" /></p>
        <p v-if="!col.items.length" class="px-1 py-2 text-xs text-(--ui-text-dimmed)">—</p>
        <div v-for="it in col.items" :key="it.id" class="space-y-1.5 rounded-lg bg-(--ui-bg-elevated)/60 p-2.5 text-sm">
          <div class="flex items-start gap-2">
            <UIcon v-if="it.status === 'doing'" name="i-lucide-loader-circle" class="mt-0.5 size-4 shrink-0 animate-spin text-(--ui-warning)" />
            <span class="min-w-0 flex-1 font-medium">{{ it.title }}</span>
            <UBadge :label="t(`burn.kind.${it.kind}`)" :color="kindColor(it.kind)" variant="subtle" size="sm" />
          </div>
          <p v-if="it.summary" class="line-clamp-4 whitespace-pre-line text-xs text-(--ui-text-muted)">{{ it.summary }}</p>
          <p v-else-if="it.detail" class="line-clamp-3 text-xs text-(--ui-text-muted)">{{ it.detail }}</p>
          <button v-if="it.branch && it.status !== 'found'" type="button" class="flex max-w-full items-center gap-1 truncate font-mono text-xs text-(--ui-text-toned) hover:text-(--ui-text)" :title="t('burn.copyBranch')" @click="copy(`git switch ${it.branch}`)">
            <UIcon name="i-lucide-git-branch" class="size-3.5 shrink-0" /><span class="truncate">{{ it.branch }}</span>
          </button>
          <div class="flex flex-wrap items-center gap-x-2 text-xs text-(--ui-text-dimmed)">
            <span v-if="it.cost_usd">${{ it.cost_usd.toFixed(2) }}</span>
            <span v-if="it.subagents">{{ t('burn.subagents', { n: it.subagents }) }}</span>
            <span>{{ when(it.updated_at) }}</span>
          </div>
          <div class="flex flex-wrap gap-1">
            <UButton v-if="['found', 'skipped', 'failed'].includes(it.status)" size="xs" color="neutral" variant="outline" icon="i-lucide-arrow-up-to-line" :label="t('burn.first')" :loading="itemActing === it.id + 'first'" @click="itemAction(it, 'first')" />
            <UButton v-if="['found', 'queued', 'paused'].includes(it.status)" size="xs" color="neutral" variant="ghost" :label="t('burn.skip')" :loading="itemActing === it.id + 'skip'" @click="itemAction(it, 'skip')" />
            <UButton v-if="it.worktree && ['done', 'failed', 'skipped'].includes(it.status)" size="xs" color="neutral" variant="ghost" icon="i-lucide-trash-2" :label="t('burn.dropWorktree')" :loading="itemActing === it.id + 'drop-worktree'" @click="itemAction(it, 'drop-worktree')" />
          </div>
        </div>
      </div>
    </div>

    <!-- settings -->
    <USlideover v-model:open="settingsOpen" :title="t('burn.settings')">
      <template #body>
        <div class="space-y-4">
          <StaleNotice :show="stale" @reload="resync()" />
          <UFormField :label="t('burn.agent')">
            <USelect v-model="form.agent_id" :items="agentItems" class="w-full" />
          </UFormField>
          <UAlert v-if="offAgent" color="warning" variant="subtle" icon="i-lucide-power-off" :title="t('burn.agentOff', { name: offAgent.name })"
            :actions="[{ label: t('burn.agentOffOpen'), color: 'warning', variant: 'outline', to: `/projects/${projectId}/agents/${offAgent.id}` }]" />

          <UFormField :label="t('burn.tierLabel')" :help="t('burn.tierHelp')">
            <USelect v-model="form.model_tier" :items="tierItems" class="w-full" />
          </UFormField>
          <UFormField :label="t('burn.subagentsLabel')" :help="t('burn.subagentsHelp')">
            <UInputNumber v-model="form.max_subagents" :min="0" :max="5" class="w-full" />
          </UFormField>
          <UFormField :label="t('burn.orderLabel')">
            <URadioGroup v-model="form.order" :items="(['roadmap', 'bugs', 'auto'] as const).map(o => ({ value: o, label: t(`burn.order.${o}`), description: t(`burn.order.${o}Help`) }))" />
          </UFormField>
          <UFormField :label="t('burn.modeLabel')">
            <URadioGroup v-model="form.result_mode" :items="[{ value: 'branch', label: t('burn.mode.branch'), description: t('burn.mode.branchHelp') }, { value: 'patch', label: t('burn.mode.patch'), description: t('burn.mode.patchHelp') }]" />
          </UFormField>
          <UFormField :label="t('burn.focus')" :help="t('burn.focusHelp')">
            <UTextarea v-model="form.focus" :rows="3" autoresize class="w-full" :placeholder="t('burn.focusPlaceholder')" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <UButton :label="t('common.save')" :loading="saving" :disabled="!!offAgent" @click="saveSettings" />
      </template>
    </USlideover>

    <!-- starting: what it means, when it stops -->
    <UModal v-model:open="confirmOpen" :title="t('burn.confirmTitle')">
      <template #body>
        <div class="space-y-3 text-sm">
          <UAlert v-if="offAgent" color="error" variant="subtle" icon="i-lucide-power-off" :title="t('burn.agentOff', { name: offAgent.name })" />
          <UAlert color="warning" variant="subtle" icon="i-lucide-shield-alert" :title="t('burn.confirmWarn')" :description="t('burn.confirmDesc', { mode: t(`burn.mode.${form.result_mode}`) })" />
          <UFormField :label="t('burn.endLabel')">
            <URadioGroup
              v-model="endMode"
              :items="[
                ...(data?.weekly_reset ? [{ value: 'reset', label: t('burn.endReset', { at: when(data.weekly_reset) }) }] : []),
                { value: 'hours', label: t('burn.endHours') },
                { value: 'at', label: t('burn.endAt') },
                { value: 'none', label: t('burn.endNone') }
              ]"
            />
          </UFormField>
          <UInputNumber v-if="endMode === 'hours'" v-model="endHours" :min="1" :max="168" class="w-40" />
          <UInput v-if="endMode === 'at'" v-model="endAt" type="datetime-local" class="w-60" />
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="confirmOpen = false" />
          <UButton color="warning" icon="i-lucide-flame" :label="t('burn.confirmStart')" :loading="acting === 'start'" :disabled="!!offAgent" @click="start" />
        </div>
      </template>
    </UModal>
  </div>
</template>
