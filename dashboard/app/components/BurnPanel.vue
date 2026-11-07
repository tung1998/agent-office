<script setup lang="ts">
import type { MessageKey } from '~/locales/vi'
// A project's Burn (spec 2026-10-01-burn-design): its main agent runs on its
// own with full access, finding work and doing it, one piece at a time in its
// own worktree, until stopped or its time is up. This page runs it; the chat
// page shows its conversation.
interface Burn {
  id?: string, conversation_id?: string, agent_id: string, model_tier: 'strong' | 'balanced' | 'fast', max_parallel: number,
  run_branch?: string, run_tree?: string, focus: string, order: 'roadmap' | 'bugs' | 'auto', template: BurnTemplate, hunt_prompt: string, ends_at: string | null, state: 'running' | 'stopped' | 'waiting_limit' | 'blocked' | 'draining', notify_channel_id?: string, notify_chat_id?: string,
  waiting_until?: string, started_by?: string, started_at?: string,
  review_profile_id: string, verify: string, code_map: string, lessons: string, coverage: string, review_cap: number, stop_after: number,
  features?: { id: string, title: string, passes: boolean }[]
  plan: Plan, next_start?: string
}
// how a run goes once on, and when it turns on by itself (saved: turning it on asks nothing)
interface Plan { end?: 'reset' | 'hours' | 'clock' | 'none', hours?: number, clock?: string, auto?: boolean, days?: number[], at?: string, tz?: string }
interface Item {
  id: string, title: string, kind: '' | 'unfinished' | 'upgrade' | 'bug' | 'idea' | 'quest', detail: string, status: string, priority: number,
  branch: string, worktree: string, summary: string, subagents: number, cost_usd: number, updated_at: string,
  reviewed: ReviewStage[], review_note: string, review_conversations: Partial<Record<ReviewStage, string>>,
  work_conversation_id: string, run_branch: string, owner?: string
}
// what its workers look for (ADR-128): a template, or the person's own prompt
const templates = ['general', 'ux', 'ideas', 'security', 'performance', 'test', 'docs', 'builder', 'custom'] as const
type BurnTemplate = typeof templates[number]
interface AgentLite { id: string, name: string, tier: string, enabled?: boolean }
const props = defineProps<{ projectId: string }>()
const { t, when } = useLang()
const toast = useToast()

const base = computed(() => `/api/projects/${props.projectId}/burn`)
const { data, refresh, error: loadError } = useLiveFetch<{ burn: Burn, items: Item[], runs?: Record<string, string>, weekly_reset?: string }>(base)
const { data: agentsData } = useLiveFetch<{ agents: AgentLite[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const burn = computed(() => data.value?.burn)
const loading = computed(() => !data.value && !loadError.value)
const items = computed(() => data.value?.items ?? [])
const agents = computed(() => agentsData.value?.agents ?? [])
const running = computed(() => burn.value?.state === 'running' || burn.value?.state === 'waiting_limit' || burn.value?.state === 'blocked' || burn.value?.state === 'draining')
const draining = computed(() => burn.value?.state === 'draining')
// a technical error (ADR-152): it waits, then tries again; Thử lại ngay tries now
const blocked = computed(() => burn.value?.state === 'blocked')
// the switch: on while it works (draining: turned off, finishing what it has)
const on = computed(() => burn.value?.state === 'running' || burn.value?.state === 'waiting_limit' || burn.value?.state === 'blocked')

// the time left, ticking
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => { clock = setInterval(() => { now.value = Date.now() }, 1000) })
onBeforeUnmount(() => clearInterval(clock))
function left(iso: string) {
  const s = Math.max(0, Math.floor((new Date(iso).getTime() - now.value) / 1000))
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60)
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m ${s % 60}s`
}

// settings (a drawer): saved as they are
const settingsOpen = ref(false)
const browserTz = Intl.DateTimeFormat().resolvedOptions().timeZone
const planOf = (p?: Plan) => ({ end: p?.end || 'reset', hours: p?.hours || 8, clock: p?.clock || '07:00', auto: !!p?.auto, days: [...(p?.days ?? [1, 2, 3, 4, 5])], at: p?.at || '22:00', tz: p?.tz || browserTz })
const form = reactive({
  agent_id: '', model_tier: 'balanced' as Burn['model_tier'], max_parallel: 1, order: 'roadmap' as Burn['order'], template: 'general' as BurnTemplate, hunt_prompt: '', focus: '',
  review_profile_id: '', notify_channel_id: '', notify_chat_id: '', verify: '', review_cap: 0, stop_after: 0, plan: planOf()
})
const { stale, reset: resync } = useDraft(burn, form, (b) => {
  Object.assign(form, {
    agent_id: b.agent_id, model_tier: b.model_tier, max_parallel: b.max_parallel, order: b.order ?? 'roadmap', template: b.template ?? 'general', hunt_prompt: b.hunt_prompt ?? '', focus: b.focus,
    review_profile_id: b.review_profile_id ?? '', notify_channel_id: b.notify_channel_id ?? '', notify_chat_id: b.notify_chat_id ?? '', verify: b.verify ?? '', review_cap: b.review_cap ?? 0, stop_after: b.stop_after ?? 0,
    plan: planOf(b.plan)
  })
})
// the plan as saved: only what its choices use
function planBody() {
  const p = form.plan
  return { end: p.end, hours: p.end === 'hours' ? p.hours : undefined, clock: p.end === 'clock' ? p.clock : undefined, auto: p.auto, days: p.auto ? p.days : undefined, at: p.auto ? p.at : undefined, tz: p.tz }
}
// Monday first; 0 is Sunday
const weekDays = [1, 2, 3, 4, 5, 6, 0] as const
function toggleDay(d: typeof weekDays[number]) {
  form.plan.days = form.plan.days.includes(d) ? form.plan.days.filter(x => x !== d) : [...form.plan.days, d]
}
const endItems = computed(() => (['reset', 'hours', 'clock', 'none'] as const).map(v => ({
  value: v, label: t(`burn.plan.end.${v}`),
  description: v === 'reset' ? (data.value?.weekly_reset ? t('burn.plan.end.resetAt', { at: when(data.value.weekly_reset) }) : t('burn.plan.end.resetNone')) : t(`burn.plan.end.${v}Help`)
})))
// what turning it on does, said in a line (the plan as saved)
const untilText = computed(() => {
  const p = planOf(burn.value?.plan)
  return p.end === 'hours' ? t('burn.plan.until.hours', { n: p.hours }) : p.end === 'clock' ? t('burn.plan.until.clock', { at: p.clock }) : t(`burn.plan.until.${p.end}`)
})
// what it does now, or what turning it on will do: the state badge's tooltip
const stateHint = computed(() => {
  const b = burn.value
  if (!b) return ''
  if (b.state === 'waiting_limit' && b.waiting_until) return t('burn.waitingUntil', { at: when(b.waiting_until) })
  if (blocked.value) return b.waiting_until ? t('burn.blockedUntil', { at: when(b.waiting_until) }) : t('burn.blockedHint')
  if (draining.value) return t('burn.drainingHint')
  if (on.value) return b.ends_at ? t('burn.endsIn', { left: left(b.ends_at), at: when(b.ends_at) }) : t('burn.noEnd')
  return `${t('burn.plan.onRuns', { until: untilText.value })} · ${b.next_start ? t('burn.plan.nextStart', { at: when(b.next_start) }) : t('burn.plan.manual')}`
})
const saving = ref(false)
async function saveSettings() {
  saving.value = true
  try {
    await $fetch(base.value, { method: 'PUT', body: { ...form, plan: planBody() } })
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
const reviewStages = reviewStageKeys
const templateItems = computed(() => templates.map(v => ({ value: v, label: t(`burn.template.${v}`) })))
// where its summary goes when it stops (ADR-120): a bot (the project's, or
// one borrowed), its chat
const notifyKind = ref<string>()
// a piece's own chats, hidden, read here: its work (ADR-116), its reviews (ADR-114)
type ChatTab = ReviewStage | 'work'
const reviewingId = ref<string | null>(null)
const reviewing = computed(() => items.value.find(i => i.id === reviewingId.value) ?? null)
const reviewTab = ref<ChatTab>('work')
const chatOf = (it: Item | null, v: ChatTab) => v === 'work' ? it?.work_conversation_id : it?.review_conversations?.[v]
const chatTabs = (it: Item | null): ChatTab[] => (['work', ...reviewStages] as ChatTab[]).filter(v => chatOf(it, v))
const reviewTabs = computed(() => chatTabs(reviewing.value).map(v => ({ value: v, label: t(v === 'work' ? 'burn.workChat' : `burn.review.${v}`) })))
const hasReviews = (it: Item) => chatTabs(it).length > 0
function openReviews(it: Item) {
  reviewingId.value = it.id
  reviewTab.value = chatTabs(it).at(-1) ?? 'work' // the latest
}
// the settings in tabs, review set up in its own tab (no separate page)
const settingsTab = ref<'find' | 'do'>('find')
const settingsTabs = computed(() => [
  { value: 'find', label: t('burn.sec.find'), icon: 'i-lucide-search' },
  { value: 'do', label: t('burn.sec.do'), icon: 'i-lucide-hammer' }
])
function openSettings(tab: 'find' | 'do' = 'find') {
  settingsTab.value = tab
  settingsOpen.value = true
}
const tierItems = computed(() => (['strong', 'balanced', 'fast'] as const).map(v => ({ value: v, label: t(`burn.tier.${v}`) })))

// turning it on asks nothing: it runs by its saved plan
const acting = ref('')
async function start() {
  acting.value = 'start'
  try {
    const res = await $fetch<{ burn: Burn }>(`${base.value}/start`, { method: 'POST', body: {} })
    // shown at once from the answer; the board catches up without holding the switch
    if (data.value) data.value = { ...data.value, burn: res.burn }
    toast.add({ title: t('burn.started'), color: 'success' })
    void refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.value = ''
  }
}
function toggle(v: boolean) {
  if (acting.value) return
  if (v) void start()
  else void stop('drain')
}
// stop: drain (finish what is in progress), resume, or stop now
async function stop(how: 'drain' | 'resume' | 'stop') {
  acting.value = how
  try {
    await $fetch(`${base.value}/${how}`, { method: 'POST' })
    await refresh()
    toast.add({ title: t(how === 'drain' ? 'burn.draining' : how === 'resume' ? 'burn.resumed' : 'burn.stopped'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.value = ''
  }
}

// the board, one run at a time: the current run by default. What is not
// finished (found, waiting, doing, paused) goes on in whichever run is on, so
// it shows in every run; finished pieces stay with the run they were done in.
const runs = computed(() => [...new Set([burn.value?.run_branch, ...items.value.map(i => i.run_branch)].filter((r): r is string => !!r))].sort().reverse())
const runPick = ref<string | null>(null)
const run = computed({
  get: () => runPick.value ?? (burn.value?.run_branch || runs.value[0] || 'all'),
  set: (v: string) => { runPick.value = v }
})
// the branch is named in the office machine's time: the start comes from it
function runLabel(branch: string) {
  const at = data.value?.runs?.[branch]
  return at ? t('burn.run.label', { at: when(at) }) : branch
}
const finished = ['done', 'failed', 'skipped']
const runItems = computed(() => [
  ...runs.value.map(r => ({ value: r, label: runLabel(r) })),
  ...(items.value.some(i => !i.run_branch && i.kind !== 'quest') ? [{ value: '__none', label: t('burn.run.none') }] : []),
  { value: 'all', label: t('burn.run.all') }
])
const shown = computed(() => run.value === 'all'
  ? items.value
  // a task an agent did outside any run (handed, ADR-210) shows in every run
  : items.value.filter(i => run.value === '__none' ? !i.run_branch && i.kind !== 'quest' : i.run_branch === run.value || !finished.includes(i.status) || (i.kind === 'quest' && !i.run_branch)))

// a quest: work the person gives, done as given (not a scan)
const questOpen = ref(false)
const quest = reactive({ title: '', detail: '' })
async function addQuest() {
  acting.value = 'quest'
  try {
    await $fetch(`${base.value}/quests`, { method: 'POST', body: { ...quest } })
    Object.assign(quest, { title: '', detail: '' })
    questOpen.value = false
    toast.add({ title: t('burn.quest.added'), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.value = ''
  }
}

// a column shows its first colMax, the rest on asking
const colMax = 10
const expanded = ref(new Set<string>())
function expand(key: string) { expanded.value = new Set([...expanded.value, key]) }
const columns = computed(() => [
  { key: 'found', label: t('burn.col.found'), statuses: ['found'] },
  { key: 'doing', label: t('burn.col.doing'), statuses: ['doing', 'review', 'queued', 'handed'] }, // handed: an agent has it (delegate, ADR-210)
  { key: 'paused', label: t('burn.col.paused'), statuses: ['paused'] },
  { key: 'done', label: t('burn.col.done'), statuses: ['done'] },
  { key: 'other', label: t('burn.col.other'), statuses: ['failed', 'skipped'] }
].map((c) => {
  // a scan (no kind) is the finding itself: it shows under found while it runs
  const all = shown.value.filter(i => !i.kind && !finished.includes(i.status) ? c.key === 'found' : c.statuses.includes(i.status))
  return { ...c, all, items: expanded.value.has(c.key) ? all : all.slice(0, colMax) }
}))
const kindColor = (k: Item['kind']) => ({ '': 'neutral', bug: 'error', unfinished: 'warning', upgrade: 'info', idea: 'primary', quest: 'success' } as const)[k]
// a status icon on the card, on top of its column: doing/handed/review already move, the rest just need telling apart at a glance
const statusIcons: Record<string, string> = {
  doing: 'i-lucide-loader-circle', handed: 'i-lucide-user-round-cog', review: 'i-lucide-scan-eye',
  queued: 'i-lucide-clock', failed: 'i-lucide-circle-x', skipped: 'i-lucide-circle-slash', done: 'i-lucide-circle-check'
}
const statusColors: Record<string, string> = {
  doing: 'text-(--ui-warning)', handed: 'text-(--ui-warning)', review: 'text-(--ui-info)',
  queued: 'text-(--ui-text-dimmed)', failed: 'text-(--ui-error)', skipped: 'text-(--ui-text-dimmed)', done: 'text-(--ui-success)'
}
const statusTitleKeys: Record<string, MessageKey> = {
  doing: 'burn.col.doing', handed: 'burn.handed', review: 'burn.review.waiting',
  queued: 'burn.status.queued', failed: 'burn.status.failed', skipped: 'burn.status.skipped', done: 'burn.status.done'
}
const statusTitle = (s: string) => statusTitleKeys[s] ? t(statusTitleKeys[s]) : ''
const itemActing = ref<Record<string, string>>({})
async function itemAction(it: Item, action: 'skip' | 'first' | 'drop-worktree') {
  if (itemActing.value[it.id]) return
  if (action === 'drop-worktree' && !confirm(t('burn.dropConfirm'))) return
  itemActing.value = { ...itemActing.value, [it.id]: action }
  try {
    await $fetch(`/api/burn-items/${it.id}/${action}`, { method: 'POST' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    const { [it.id]: _, ...rest } = itemActing.value
    itemActing.value = rest
  }
}
// the scans' coverage plan (ADR-141): items checked of all; scans go on until every one is
const coverage = computed(() => {
  const lines = (burn.value?.coverage ?? '').split('\n').map(l => l.trim().replace(/^[-*+ ]+/, ''))
  return { done: lines.filter(l => /^\[[xX]\]/.test(l)).length, total: lines.filter(l => /^\[[ xX]\]/.test(l)).length }
})

// a builder Burn (ADR-139): idle before its first Start, no cue what it does;
// started, its progress is a feature list (features.json), not the generic board
const isBuilder = computed(() => burn.value?.template === 'builder')
const showBuilderBanner = computed(() => isBuilder.value && burn.value?.state === 'stopped' && items.value.length === 0)
const builderFeatures = computed(() => burn.value?.features ?? [])
const builderDone = computed(() => builderFeatures.value.filter(f => f.passes).length)
</script>

<template>
  <div class="space-y-4">
    <UAlert v-if="loadError" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    <!-- one bar, two groups: Burn itself (state, switch, its chat and settings) on the left; the board's (run picker, add a task) on the right -->
    <div class="flex flex-wrap items-center gap-2">
      <div class="flex items-center gap-1 rounded-xl border border-(--ui-border) bg-(--ui-bg)/40 py-1 ps-1.5 pe-1">
        <span class="flex size-7 shrink-0 items-center justify-center rounded-lg" :class="running ? 'bg-(--ui-warning)/15 text-(--ui-warning)' : 'bg-(--ui-bg-elevated) text-(--ui-text-muted)'">
          <UIcon name="i-lucide-flame" class="size-4" :class="burn?.state === 'running' && 'animate-pulse'" />
        </span>
        <span class="ps-1 text-sm font-semibold">{{ t('burn.title') }}</span>
        <USkeleton v-if="loading" class="ms-1 h-5 w-16" />
        <UBadge v-else-if="burn" :label="t(`burn.state.${burn.state}`)" :color="burn.state === 'running' ? 'warning' : burn.state === 'blocked' ? 'error' : burn.state === 'waiting_limit' || burn.state === 'draining' ? 'info' : 'neutral'" variant="subtle" size="sm" class="ms-1 shrink-0" :title="stateHint" />
        <UButton v-if="blocked" size="xs" color="error" variant="soft" icon="i-lucide-rotate-cw" :label="t('burn.retryNow')" :loading="acting === 'resume'" class="ms-1" @click="stop('resume')" />
        <template v-if="draining">
          <UButton size="xs" color="warning" variant="soft" icon="i-lucide-play" :label="t('burn.resume')" :loading="acting === 'resume'" class="ms-1" @click="stop('resume')" />
          <UButton size="xs" color="neutral" variant="soft" icon="i-lucide-square" :label="t('burn.stopNow')" :loading="acting === 'stop'" @click="stop('stop')" />
        </template>
        <!-- on is on: it runs by the saved plan, nothing more is asked -->
        <USwitch v-else :model-value="on" color="warning" :loading="acting === 'start' || acting === 'drain'" :disabled="loading || !!offAgent || !!acting" :aria-label="t('burn.toggle')" :title="t('burn.confirmDesc')" class="mx-1.5" @update:model-value="toggle" />
        <span class="mx-0.5 h-5 w-px bg-(--ui-border)" />
        <UButton v-if="burn?.conversation_id" size="sm" color="neutral" variant="ghost" icon="i-lucide-messages-square" :title="t('burn.openChat')" :aria-label="t('burn.openChat')" :to="`/projects/${projectId}?tab=chat&c=${burn.conversation_id}`" />
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-settings-2" :title="t('burn.settings')" :aria-label="t('burn.settings')" :disabled="loading" @click="openSettings()" />
      </div>
      <div class="ms-auto flex items-center gap-2">
        <USelect v-model="run" :items="runItems" size="sm" class="w-44 max-w-full" :aria-label="t('burn.run.pick')" />
        <UButton size="sm" color="primary" variant="soft" icon="i-lucide-plus" :label="t('burn.quest.add')" @click="questOpen = true" />
      </div>
    </div>
    <UAlert v-if="offAgent" color="warning" variant="subtle" icon="i-lucide-power-off" :title="t('burn.agentOff', { name: offAgent.name })"
      :actions="[{ label: t('burn.agentOffChange'), color: 'warning', variant: 'outline', onClick: () => openSettings('do') }, { label: t('burn.agentOffOpen'), color: 'neutral', variant: 'ghost', to: `/projects/${projectId}/agents/${offAgent.id}` }]" />
    <UAlert v-if="showBuilderBanner" color="primary" variant="subtle" icon="i-lucide-sparkles" :title="t('burn.builder.startBanner')"
      :actions="[{ label: t('burn.start'), color: 'primary', onClick: start }]" />

    <!-- side by side on every screen: scrolled across below xl -->
    <div class="flex snap-x snap-mandatory items-start gap-3 overflow-x-auto pb-2 xl:grid xl:grid-cols-5 xl:overflow-visible xl:pb-0">
      <div v-for="col in columns" :key="col.key" class="w-[80%] min-w-0 shrink-0 snap-start space-y-2 rounded-xl border border-(--ui-border) bg-(--ui-bg)/40 p-2.5 sm:w-[45%] xl:w-auto">
        <p class="flex items-center gap-2 px-0.5 text-xs font-semibold tracking-wide text-(--ui-text-muted) uppercase">
          {{ col.label }}<UBadge :label="String(col.all.length)" color="neutral" variant="subtle" size="sm" />
          <span v-if="col.key === 'found' && coverage.total" class="ms-auto text-xs font-normal normal-case tabular-nums text-(--ui-text-dimmed)" :title="t('burn.col.coverage', { n: coverage.done, max: coverage.total })">{{ coverage.done }}/{{ coverage.total }}</span>
        </p>
        <LoadingRows v-if="loading" :n="2" :icon="false" />
        <div v-else-if="!col.items.length" class="flex flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-(--ui-border) px-2 py-6 text-center">
          <UIcon name="i-lucide-inbox" class="size-4 text-(--ui-text-dimmed)" />
          <p class="text-xs text-(--ui-text-dimmed)">{{ t('burn.col.empty') }}</p>
        </div>
        <div v-for="it in col.items" :key="it.id" class="space-y-2 rounded-lg border border-transparent bg-(--ui-bg-elevated)/60 p-3 text-sm transition-colors hover:border-(--ui-border)">
          <!-- a small meta row on top (status, kind, when), so the title has the whole width -->
          <div class="flex items-center gap-1.5 text-xs text-(--ui-text-dimmed)">
            <UIcon v-if="statusIcons[it.status]" :name="statusIcons[it.status]" class="size-3.5 shrink-0" :class="[statusColors[it.status], it.status === 'doing' && 'animate-spin']" :title="statusTitle(it.status)" />
            <UBadge v-if="it.kind" :label="t(`burn.kind.${it.kind}`)" :color="kindColor(it.kind)" variant="subtle" size="sm" />
            <UBadge v-else :label="t('burn.kind.hunting')" color="neutral" variant="subtle" size="sm" />
            <span class="ms-auto shrink-0 tabular-nums">{{ when(it.updated_at) }}</span>
          </div>
          <p class="line-clamp-3 text-sm leading-snug font-medium" :title="it.title">{{ it.title }}</p>
          <ClampText v-if="it.summary" :text="it.summary" :lines="4" class="text-xs text-(--ui-text-muted)" />
          <ClampText v-else-if="it.detail" :text="it.detail" :lines="3" class="text-xs text-(--ui-text-muted)" />
          <p v-if="it.review_note" class="flex gap-1 rounded-md bg-(--ui-bg)/60 p-1.5 text-xs text-(--ui-text-muted)" :title="it.review_note">
            <UIcon name="i-lucide-scan-eye" class="mt-0.5 size-3.5 shrink-0" /><span class="line-clamp-2">{{ it.review_note }}</span>
          </p>
          <div v-if="it.owner || it.cost_usd || it.subagents || it.status === 'review' || it.reviewed?.length" class="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-(--ui-text-dimmed)">
            <span v-if="it.owner" class="inline-flex items-center gap-1" :title="t('burn.owner')"><UIcon name="i-lucide-user-round" class="size-3" />{{ it.owner }}</span>
            <span v-if="it.cost_usd" class="inline-flex items-center gap-1 tabular-nums"><UIcon name="i-lucide-dollar-sign" class="size-3" />{{ it.cost_usd.toFixed(2) }}</span>
            <span v-if="it.subagents" class="inline-flex items-center gap-1"><UIcon name="i-lucide-users" class="size-3" />{{ t('burn.subagents', { n: it.subagents }) }}</span>
            <span v-if="it.status === 'review'" class="inline-flex items-center gap-1 text-(--ui-info)"><UIcon name="i-lucide-scan-eye" class="size-3" />{{ t('burn.review.waiting') }}</span>
            <span v-else-if="it.reviewed?.length" class="inline-flex items-center gap-1 text-(--ui-success)"><UIcon name="i-lucide-check" class="size-3" />{{ t('burn.review.passed', { stages: it.reviewed.map(v => t(`burn.review.${v}`)).join(', ') }) }}</span>
          </div>
          <div v-if="hasReviews(it) || ['found', 'skipped', 'failed', 'queued', 'paused', 'review'].includes(it.status) || (it.worktree && ['done', 'failed', 'skipped'].includes(it.status))" class="flex flex-wrap items-center gap-1 border-t border-(--ui-border)/60 pt-2">
            <UButton v-if="hasReviews(it)" size="xs" color="neutral" variant="ghost" icon="i-lucide-scan-eye" :label="t('burn.review.view')" @click="openReviews(it)" />
            <UButton v-if="['found', 'skipped', 'failed'].includes(it.status)" size="xs" color="neutral" variant="outline" icon="i-lucide-arrow-up-to-line" :label="t('burn.first')" :loading="itemActing[it.id] === 'first'" :disabled="!!itemActing[it.id] && itemActing[it.id] !== 'first'" @click="itemAction(it, 'first')" />
            <UButton v-if="['found', 'queued', 'paused', 'review'].includes(it.status)" size="xs" color="neutral" variant="ghost" :label="t('burn.skip')" :loading="itemActing[it.id] === 'skip'" :disabled="!!itemActing[it.id] && itemActing[it.id] !== 'skip'" @click="itemAction(it, 'skip')" />
            <UButton v-if="it.worktree && ['done', 'failed', 'skipped'].includes(it.status)" size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" :label="t('burn.dropWorktree')" class="ms-auto" :loading="itemActing[it.id] === 'drop-worktree'" :disabled="!!itemActing[it.id] && itemActing[it.id] !== 'drop-worktree'" @click="itemAction(it, 'drop-worktree')" />
          </div>
        </div>
        <UButton v-if="col.all.length > col.items.length" size="xs" color="neutral" variant="ghost" block :label="t('burn.showMore', { n: col.all.length - col.items.length })" @click="expand(col.key)" />
      </div>
    </div>



    <!-- a builder Burn's feature progress (ADR-139), above the generic board -->
    <UCard v-if="isBuilder && (builderFeatures.length || running)" :ui="{ body: 'space-y-2 sm:p-4' }">
      <p class="text-xs font-medium text-(--ui-text-muted)">
        <template v-if="builderFeatures.length">{{ t('burn.builder.progress', { done: builderDone, total: builderFeatures.length }) }}</template>
        <template v-else>{{ t('burn.builder.preparing') }}</template>
      </p>
      <ul v-if="builderFeatures.length" class="grid gap-1 sm:grid-cols-2">
        <li v-for="f in builderFeatures" :key="f.id" class="flex items-center gap-1.5 text-xs">
          <UIcon :name="f.passes ? 'i-lucide-circle-check' : 'i-lucide-circle'" class="size-3.5 shrink-0" :class="f.passes ? 'text-(--ui-success)' : 'text-(--ui-text-dimmed)'" />
          <span class="truncate" :class="{ 'text-(--ui-text-muted)': !f.passes }">{{ f.title }}</span>
        </li>
      </ul>
    </UCard>

    <!-- settings in two parts: finding tasks, and doing them (with review, set up right here) -->
    <USlideover v-model:open="settingsOpen" :title="t('burn.settingsTitle')" :ui="{ content: 'sm:max-w-2xl' }">
      <template #body>
        <div class="space-y-5">
          <StaleNotice :show="stale" @reload="resync()" />
          <UTabs v-model="settingsTab" :items="settingsTabs" :content="false" size="sm" class="w-full" />

          <!-- finding tasks: what it looks for, and what it has mapped so far -->
          <div v-show="settingsTab === 'find'" class="space-y-5">
            <p class="text-xs text-(--ui-text-muted)">{{ t('burn.sec.findDesc') }}</p>
            <UFormField>
              <template #label><InfoLabel :label="t('burn.templateLabel')" :info="t(`burn.template.${form.template}Help`)" /></template>
              <USelect v-model="form.template" :items="templateItems" class="w-full" />
            </UFormField>
            <UFormField v-if="form.template === 'custom'" required>
              <template #label><InfoLabel :label="t('burn.huntPrompt')" :info="t('burn.huntPromptHelp')" /></template>
              <UTextarea v-model="form.hunt_prompt" :rows="5" :maxlength="4000" autoresize class="w-full" :placeholder="t('burn.huntPromptPlaceholder')" />
            </UFormField>
            <UFormField v-if="form.template === 'general'" :label="t('burn.orderLabel')">
              <URadioGroup v-model="form.order" :items="(['roadmap', 'bugs', 'auto'] as const).map(o => ({ value: o, label: t(`burn.order.${o}`), description: t(`burn.order.${o}Help`) }))" />
            </UFormField>
            <UFormField>
              <template #label><InfoLabel :label="t('burn.focus')" :info="t('burn.focusHelp')" /></template>
              <UTextarea v-model="form.focus" :rows="3" :maxlength="2000" autoresize class="w-full" :placeholder="t('burn.focusPlaceholder')" />
            </UFormField>
            <details v-if="burn?.coverage || burn?.code_map" class="group rounded-lg border border-(--ui-border)">
              <summary class="flex cursor-pointer items-center gap-2 p-3 text-sm font-medium">
                <UIcon name="i-lucide-chevron-right" class="size-4 transition group-open:rotate-90" />{{ t('burn.learned') }}
              </summary>
              <div class="space-y-2 px-3 pb-3 text-xs">
                <template v-if="burn?.coverage">
                  <p class="font-medium text-(--ui-text-muted)">{{ t('burn.coverage', { n: coverage.done, max: coverage.total }) }}</p>
                  <p class="max-h-64 overflow-y-auto whitespace-pre-line font-mono">{{ burn.coverage }}</p>
                </template>
                <template v-if="burn?.code_map">
                  <p class="font-medium text-(--ui-text-muted)">{{ t('burn.codeMap') }}</p>
                  <p class="max-h-64 overflow-y-auto whitespace-pre-line font-mono">{{ burn.code_map }}</p>
                </template>
              </div>
            </details>
          </div>

          <!-- doing tasks: when, who, checks and review, report -->
          <div v-show="settingsTab === 'do'" class="space-y-6">
            <p class="text-xs text-(--ui-text-muted)">{{ t('burn.sec.doDesc') }}</p>
            <section class="space-y-3">
              <p class="text-xs font-semibold tracking-wide text-(--ui-text-muted) uppercase">{{ t('burn.sec.plan') }}</p>
              <UFormField :label="t('burn.plan.endLabel')">
                <URadioGroup v-model="form.plan.end" :items="endItems" variant="card" :ui="{ fieldset: 'grid gap-2 sm:grid-cols-2' }" />
              </UFormField>
              <div v-if="form.plan.end === 'hours'" class="flex items-center gap-2">
                <UInputNumber v-model="form.plan.hours" :min="1" :max="168" class="w-32" />
                <span class="text-sm text-(--ui-text-muted)">{{ t('burn.plan.hoursUnit') }}</span>
              </div>
              <div v-else-if="form.plan.end === 'clock'" class="flex items-center gap-2">
                <span class="text-sm text-(--ui-text-muted)">{{ t('burn.plan.clockPrefix') }}</span>
                <UInput v-model="form.plan.clock" type="time" class="w-32" />
              </div>
              <div class="space-y-3 rounded-lg border border-(--ui-border) p-3">
                <div class="flex items-center gap-3">
                  <div class="min-w-0 flex-1">
                    <p class="text-sm font-medium">{{ t('burn.plan.auto') }}</p>
                    <p class="text-xs text-(--ui-text-muted)">{{ t('burn.plan.autoHelp') }}</p>
                  </div>
                  <USwitch v-model="form.plan.auto" color="warning" />
                </div>
                <template v-if="form.plan.auto">
                  <div class="grid max-w-sm grid-cols-7 gap-1">
                    <UButton v-for="d in weekDays" :key="d" size="sm" :color="form.plan.days.includes(d) ? 'warning' : 'neutral'" :variant="form.plan.days.includes(d) ? 'solid' : 'outline'"
                      :label="t(`burn.plan.day.${d}`)" class="justify-center px-0" :aria-pressed="form.plan.days.includes(d)" @click="toggleDay(d)" />
                  </div>
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="text-sm text-(--ui-text-muted)">{{ t('burn.plan.atPrefix') }}</span>
                    <UInput v-model="form.plan.at" type="time" class="w-32" />
                    <span class="text-xs text-(--ui-text-dimmed)">{{ form.plan.tz }}</span>
                  </div>
                  <p v-if="!form.plan.days.length" class="text-xs text-(--ui-error)">{{ t('burn.plan.noDays') }}</p>
                </template>
              </div>
            </section>

            <section class="space-y-3">
              <p class="text-xs font-semibold tracking-wide text-(--ui-text-muted) uppercase">{{ t('burn.sec.who') }}</p>
              <UFormField :label="t('burn.agent')">
                <USelect v-model="form.agent_id" :items="agentItems" class="w-full" />
              </UFormField>
              <UAlert v-if="offAgent" color="warning" variant="subtle" icon="i-lucide-power-off" :title="t('burn.agentOff', { name: offAgent.name })"
                :actions="[{ label: t('burn.agentOffOpen'), color: 'warning', variant: 'outline', to: `/projects/${projectId}/agents/${offAgent.id}` }]" />
              <div class="grid gap-3 sm:grid-cols-2">
                <UFormField :label="t('burn.tierLabel')" :help="t('burn.tierHelp')">
                  <USelect v-model="form.model_tier" :items="tierItems" class="w-full" />
                </UFormField>
                <UFormField>
                  <template #label><InfoLabel :label="t('burn.parallelLabel')" :info="t('burn.parallelHelp')" /></template>
                  <UInputNumber v-model="form.max_parallel" :min="1" :max="5" class="w-full" />
                </UFormField>
              </div>
            </section>

            <section class="space-y-3">
              <p class="text-xs font-semibold tracking-wide text-(--ui-text-muted) uppercase">{{ t('burn.sec.check') }}</p>
              <UFormField>
                <template #label><InfoLabel :label="t('burn.verify')" :info="t('burn.verifyHelp')" /></template>
                <UTextarea v-model="form.verify" :rows="3" :maxlength="2000" autoresize class="w-full font-mono text-xs" placeholder="go build ./...&#10;go test ./..." />
              </UFormField>
              <BurnReviewSetup v-if="settingsOpen" v-model="form.review_profile_id" :project-id="projectId" />
              <UFormField class="sm:max-w-[50%]">
                <template #label><InfoLabel :label="t('burn.reviewCap')" :info="t('burn.reviewCapHelp')" /></template>
                <UInputNumber v-model="form.review_cap" :min="0" :max="100" class="w-full" />
              </UFormField>
            </section>

            <section class="space-y-3">
              <p class="text-xs font-semibold tracking-wide text-(--ui-text-muted) uppercase">{{ t('burn.sec.report') }}</p>
              <UFormField>
                <template #label><InfoLabel :label="t('burn.notify.label')" :info="t('burn.notify.help')" /></template>
                <div class="grid gap-2 sm:grid-cols-2">
                  <NotifyBotPicker v-model="form.notify_channel_id" v-model:kind="notifyKind" :project-id="projectId" :none-label="t('burn.notify.off')" />
                  <UInput v-model="form.notify_chat_id" class="w-full font-mono text-xs" :disabled="!form.notify_channel_id" :placeholder="notifyKind === 'telegram' ? t('alert.chatTelegram') : t('alert.chatDiscord')" />
                </div>
              </UFormField>
              <UFormField class="sm:max-w-[50%]">
                <template #label><InfoLabel :label="t('burn.stopAfter')" :info="t('burn.stopAfterHelp')" /></template>
                <UInputNumber v-model="form.stop_after" :min="0" :max="100" class="w-full" />
              </UFormField>
              <div v-if="burn?.lessons" class="space-y-1 text-xs">
                <p class="font-medium text-(--ui-text-muted)">{{ t('burn.lessons') }}</p>
                <p class="whitespace-pre-line">{{ burn.lessons }}</p>
              </div>
            </section>
          </div>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full items-center justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="settingsOpen = false" />
          <UButton icon="i-lucide-save" :label="t('common.save')" :loading="saving" :disabled="!!offAgent || (form.plan.auto && !form.plan.days.length)" @click="saveSettings" />
        </div>
      </template>
    </USlideover>

    <!-- a piece's reviews, to read -->
    <USlideover :open="!!reviewing" :title="t('burn.review.viewTitle', { title: reviewing?.title ?? '' })" :ui="{ content: 'sm:max-w-2xl' }" @update:open="v => { if (!v) reviewingId = null }">
      <template #body>
        <div v-if="reviewing" class="space-y-3">
          <UTabs v-if="reviewTabs.length > 1" v-model="reviewTab" :items="reviewTabs" :content="false" size="sm" />
          <RunTranscript v-if="chatOf(reviewing, reviewTab)" :key="reviewTab" :project-id="projectId" :conversation-id="chatOf(reviewing, reviewTab)!" />
        </div>
      </template>
    </USlideover>

    <!-- a quest to give -->
    <UModal v-model:open="questOpen" :title="t('burn.quest.add')" :description="t('burn.quest.help')">
      <template #body>
        <div class="space-y-3">
          <UFormField :label="t('burn.quest.title')" required>
            <UInput v-model="quest.title" :maxlength="160" class="w-full" :placeholder="t('burn.quest.titlePlaceholder')" autofocus />
          </UFormField>
          <UFormField :label="t('burn.quest.detail')">
            <UTextarea v-model="quest.detail" :rows="4" :maxlength="4000" autoresize class="w-full" />
          </UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="questOpen = false" />
          <UButton icon="i-lucide-plus" :label="t('burn.quest.add')" :loading="acting === 'quest'" :disabled="!quest.title.trim()" @click="addQuest" />
        </div>
      </template>
    </UModal>

  </div>
</template>
