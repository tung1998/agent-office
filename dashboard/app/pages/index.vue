<script setup lang="ts">
const { user, isAdmin } = useAuth()
// three tabs, the one open in the URL: the work, how it went, the machine (admin)
const route = useRoute()
type HomeTab = 'work' | 'stats' | 'machine'
const tab = computed<HomeTab>({
  get: () => {
    const q = route.query.tab
    return q === 'stats' || (q === 'machine' && isAdmin.value) ? q : 'work'
  },
  set: v => navigateTo({ query: { ...route.query, tab: v === 'work' ? undefined : v } }, { replace: true })
})
const { t, dateLocale } = useLang()

const _f2 = useLiveFetch<{ providers: Provider[] }>('/api/providers', { lazy: true })
const { data: prov } = _f2
const _f3 = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const { data: proj } = _f3
const _f4 = useLiveFetch<{ templates: OrgModel[] }>('/api/templates', { lazy: true })
const { data: tpl } = _f4

// the latest chats across projects (bots' too): where the work happens now
interface RecentChat { id: string, project_id: string, title: string, agent_name: string, source?: Source, updated_at: string, active_turn?: string }
const _f5 = useLiveFetch<{ conversations: RecentChat[], projects: Record<string, string> }>('/api/conversations/recent', { query: { limit: 6 }, lazy: true })
const { data: chatData } = _f5
const recentChats = computed(() => chatData.value?.conversations ?? [])
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })

// what needs a person, and the last day in numbers
interface Incident { kind: string, severity: 'error' | 'warning' | 'info', project_id: string, project_name: string, title: string, detail: string, link: string, id: string, key: string, prompt?: string }
const _f6 = useLiveFetch<{ incidents: Incident[], count: number }>('/api/incidents', { key: 'incidents', lazy: true })
const { data: incData, refresh: refreshInc } = _f6
const incidents = computed(() => incData.value?.incidents ?? [])
// the kind of each, as a word next to it
const incKind = (k: string) => t(`incidents.kind.${k}` as 'incidents.kind.monitor')
const _f7 = useLiveFetch<{ totals: { running: number, pending: number, failed_24h: number, cost_24h: number } }>('/api/jobs/stats?since=24h', { lazy: true })
const { data: stats } = _f7
// none is awaited: the page shows at once (a phone over a VPN), each block
// with its skeleton until its data comes (they all start together)
// what can be done from the list: decide a card; try again, let go or
// investigate something that went wrong
const toast = useToast()
const acting = reactive(new Set<string>()) // each button on its own: several can run at once
async function seen(x: Incident) {
  const key = x.key + 'seen'
  acting.add(key)
  try {
    await $fetch(`/api/conversations/${x.id}/seen`, { method: 'POST', body: { seen: true } })
    await refreshInc()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.delete(key)
  }
}
// skip: rejected, and its agent is not run again about it
async function act(x: Incident, what: 'approve' | 'reject' | 'skip' | 'retry' | 'dismiss' | 'continue') {
  const key = x.key + what
  if (acting.has(key)) return
  acting.add(key)
  try {
    const decide = what === 'skip' ? 'reject' : what
    const body = what === 'skip' ? { skip: true } : {}
    if ((decide === 'approve' || decide === 'reject') && x.kind === 'patch') await $fetch(`/api/patches/${x.id}/${decide}`, { method: 'POST', body })
    else if (decide === 'approve' || decide === 'reject') await $fetch(`/api/actions/${x.id}/${decide}`, { method: 'POST', body })
    else if (what === 'dismiss') await $fetch('/api/incidents/dismiss', { method: 'POST', body: { key: x.key } })
    // a chat stopped for tokens: the agent that stopped goes on in it
    else if (what === 'continue') await $fetch(`/api/conversations/${x.id}/messages`, { method: 'POST', body: { text: x.prompt } })
    else if (x.kind === 'jobs') await $fetch(`/api/jobs/${x.id}/retry`, { method: 'POST' })
    else await $fetch('/api/incidents/retry', { method: 'POST', body: { kind: x.kind, id: x.id } })
    toast.add({ title: t(`home.done_${what}`), color: 'success' })
    await refreshInc()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    acting.delete(key)
  }
}
// Bỏ qua tất cả: every card waiting, rejected at once (no agent goes on)
const proposalsN = computed(() => incidents.value.filter(x => x.kind === 'approval' || x.kind === 'patch').length)
const skipAllOpen = ref(false)
const skippingAll = ref(false)
async function skipAll() {
  skippingAll.value = true
  try {
    const res = await $fetch<{ skipped: number }>('/api/proposals/skip-all', { method: 'POST', body: {} })
    toast.add({ title: t('home.skipAllDone', { n: res.skipped }), color: 'success' })
    skipAllOpen.value = false
    await refreshInc()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    skippingAll.value = false
  }
}
// Điều tra: the project's lead looks into it in a new chat
const prefill = useState<{ text: string, files: [], send?: boolean } | null>('chat-prefill', () => null)
function investigate(x: Incident) {
  prefill.value = { text: t('home.investigatePrompt', { kind: incKind(x.kind), title: x.title, detail: x.detail || '—' }), files: [], send: true }
  navigateTo({ path: `/projects/${x.project_id}`, query: { tab: 'chat' } })
}
// a failure opened in place: what happened, its failed runs, its logs or checks
const shown = ref<Incident | null>(null)
const shownOpen = computed({ get: () => !!shown.value, set: (v: boolean) => { if (!v) shown.value = null } })
const isDecision = (x: Incident) => x.kind === 'approval' || x.kind === 'patch' || x.kind === 'unread' || x.kind === 'stalled' // a link: where to act on it
const runsQuery = (x: Incident) => ({
  jobs: `project=${x.project_id}&status=failed&since=24h&limit=30`,
  automation: `origin_id=${x.id}&limit=20`,
  monitor: `origin_id=${x.id}&limit=20`
} as Record<string, string>)[x.kind] ?? ''
interface MonitorEvent { id: string, monitor_id: string, kind: string, message: string, analysis: string, at: string }
const monitorEvents = ref<MonitorEvent[] | null>(null)
watch(shown, async (x) => {
  monitorEvents.value = null
  if (x?.kind !== 'monitor') return
  try {
    const res = await $fetch<{ events: MonitorEvent[] }>('/api/monitor-events', { query: { project: x.project_id, limit: 50 } })
    monitorEvents.value = res.events.filter(e => e.monitor_id === x.id).slice(0, 10)
  } catch { monitorEvents.value = [] }
})
const canRetry = (k: string) => ['monitor', 'process', 'automation', 'bot', 'jobs'].includes(k)
const incIcon: Record<string, string> = {
  monitor: 'i-lucide-activity', process: 'i-lucide-square-terminal', automation: 'i-lucide-alarm-clock-off',
  bot: 'i-lucide-bot', jobs: 'i-lucide-circle-x', approval: 'i-lucide-stamp', patch: 'i-lucide-file-diff', limit: 'i-lucide-gauge', unread: 'i-lucide-message-square-dot',
  stalled: 'i-lucide-battery-low'
}

const providers = computed(() => prov.value?.providers ?? [])
const projects = computed(() => proj.value?.projects ?? [])
const okProviders = computed(() => providers.value.filter(p => p.status === 'ok').length)
const withModel = computed(() => projects.value.filter(p => p.model).length)

const setupDone = computed(() => steps.value.every(s => s.done))
const steps = computed(() => [
  {
    done: okProviders.value > 0,
    title: t('home.step1Title'),
    text: okProviders.value ? t('home.step1TextDone', { n: okProviders.value, total: providers.value.length }) : t('home.step1TextTodo'),
    to: '/providers', icon: 'i-lucide-plug'
  },
  {
    done: projects.value.length > 0,
    title: t('home.step2Title'),
    text: projects.value.length ? t('home.step2TextDone', { n: projects.value.length }) : t('home.step2TextTodo'),
    to: '/projects', icon: 'i-lucide-folder-git-2'
  },
  {
    done: projects.value.length > 0 && withModel.value === projects.value.length,
    title: t('home.step3Title'),
    text: projects.value.length ? t('home.step3TextDone', { n: withModel.value, total: projects.value.length }) : t('home.step3TextTodo'),
    to: projects.value.length ? '/projects' : '/templates', icon: 'i-lucide-network'
  }
])
</script>

<template>
  <PageShell :title="t('nav.overview')">
    <UTabs
      v-model="tab" :content="false" variant="link" class="mb-4"
      :items="[
        { value: 'work', label: t('home.tabWork'), icon: 'i-lucide-list-checks', badge: incData?.count ? { label: String(incData.count), color: 'error', variant: 'subtle' } : undefined },
        { value: 'stats', label: t('home.tabStats'), icon: 'i-lucide-chart-column' },
        ...(isAdmin ? [{ value: 'machine', label: t('home.tabMachine'), icon: 'i-lucide-cpu' }] : [])
      ]"
    />
    <SystemPanel v-if="tab === 'machine'" />
    <OverviewCharts v-else-if="tab === 'stats'" />
    <div v-else class="space-y-6">

      <!-- what needs a person now -->
      <UCard :ui="{ body: 'p-0 sm:p-0' }">
        <template #header>
          <div class="flex items-center justify-between gap-2">
            <p class="flex items-center gap-2 font-semibold">
              <UIcon name="i-lucide-siren" class="size-5" :class="incData?.count ? 'text-(--ui-error)' : 'text-(--ui-success)'" />
              {{ t('home.attention') }}
              <UBadge v-if="incData?.count" :label="String(incData.count)" color="error" variant="subtle" size="sm" />
            </p>
            <UTooltip v-if="isAdmin && proposalsN >= 2" :text="t('action.skipInfo')">
              <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-skip-forward" :label="t('home.skipAll')" @click="skipAllOpen = true" />
            </UTooltip>
          </div>
        </template>
        <LoadingRows v-if="!incData" :n="2" />
        <p v-else-if="!incidents.length" class="flex items-center gap-2 p-4 text-sm text-(--ui-text-muted)">
          <UIcon name="i-lucide-circle-check" class="size-4 text-(--ui-success)" />{{ t('home.allGood') }}
        </p>
        <div v-else class="divide-y divide-(--ui-border)">
          <div v-for="(x, i) in incidents" :key="i" class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5">
            <component
              :is="isDecision(x) ? 'NuxtLink' : 'button'" :to="isDecision(x) ? x.link : undefined" :type="isDecision(x) ? undefined : 'button'"
              class="flex min-w-0 flex-1 basis-60 items-center gap-3 text-left" @click="!isDecision(x) && (shown = x)"
            >
              <UIcon :name="incIcon[x.kind] ?? 'i-lucide-circle-alert'" class="size-4 shrink-0" :class="x.severity === 'error' ? 'text-(--ui-error)' : x.severity === 'info' ? 'text-primary' : 'text-(--ui-warning)'" />
              <span class="min-w-0 flex-1">
                <span class="flex items-center gap-2">
                  <span class="truncate text-sm font-medium hover:underline">{{ x.title }}</span>
                  <UBadge :label="incKind(x.kind)" color="neutral" variant="subtle" size="sm" />
                </span>
                <span class="block truncate text-xs text-(--ui-text-muted)">{{ x.project_name }}<template v-if="x.detail"> · {{ x.detail }}</template></span>
              </span>
            </component>
            <!-- a chat answered since the person looked: theirs, whoever they are -->
            <div v-if="x.kind === 'unread'" class="flex shrink-0 items-center gap-1 max-sm:w-full max-sm:ps-7">
              <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-eye" :label="t('home.view')" :to="x.link" />
              <UButton size="xs" color="neutral" variant="ghost" :label="t('chat.markSeen')" :loading="acting.has(x.key + 'seen')" @click="seen(x)" />
            </div>
            <!-- a chat stopped for tokens: go on, or let it be (the person who sent it, or an admin) -->
            <div v-else-if="x.kind === 'stalled'" class="flex shrink-0 items-center gap-1 max-sm:w-full max-sm:ps-7">
              <UTooltip :text="t('home.continueInfo')">
                <UButton size="xs" icon="i-lucide-play" :label="t('home.continue')" :loading="acting.has(x.key + 'continue')" @click="act(x, 'continue')" />
              </UTooltip>
              <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-eye" :label="t('home.view')" :to="x.link" />
              <UButton v-if="isAdmin" size="xs" color="neutral" variant="ghost" :label="t('home.dismiss')" :loading="acting.has(x.key + 'dismiss')" @click="act(x, 'dismiss')" />
            </div>
            <!-- what to do about it, right here -->
            <div v-else-if="isAdmin" class="flex shrink-0 flex-wrap items-center gap-1 max-sm:w-full max-sm:ps-7">
              <template v-if="x.kind === 'approval' || x.kind === 'patch'">
                <UButton size="xs" icon="i-lucide-check" :label="t('home.approve')" :loading="acting.has(x.key + 'approve')" @click="act(x, 'approve')" />
                <UButton size="xs" color="neutral" variant="ghost" :label="t('home.reject')" :loading="acting.has(x.key + 'reject')" @click="act(x, 'reject')" />
                <UTooltip :text="t('action.skipInfo')">
                  <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-skip-forward" :label="t('action.skip')" :loading="acting.has(x.key + 'skip')" @click="act(x, 'skip')" />
                </UTooltip>
              </template>
              <template v-else>
                <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-eye" :label="t('home.view')" :to="x.link" />
                <UButton v-if="canRetry(x.kind)" size="xs" color="neutral" variant="outline" icon="i-lucide-rotate-cw" :label="t('home.retry')" :loading="acting.has(x.key + 'retry')" @click="act(x, 'retry')" />
                <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-search-check" :label="t('home.investigate')" @click="investigate(x)" />
                <UButton size="xs" color="neutral" variant="ghost" :label="t('home.dismiss')" :loading="acting.has(x.key + 'dismiss')" @click="act(x, 'dismiss')" />
              </template>
            </div>
          </div>
        </div>
      </UCard>

      <!-- the last day -->
      <div class="grid grid-cols-2 gap-2 sm:grid-cols-4 sm:gap-3">
        <UCard :ui="{ body: 'p-3 sm:p-4' }">
          <p class="text-xs text-(--ui-text-muted)">{{ t('job.running') }}</p>
          <div class="text-xl font-semibold tabular-nums sm:text-2xl"><USkeleton v-if="!stats" class="mt-1 h-7 w-12" /><template v-else>{{ (stats?.totals.running ?? 0) + (stats?.totals.pending ?? 0) }}</template></div>
        </UCard>
        <UCard :ui="{ body: 'p-3 sm:p-4' }">
          <p class="text-xs text-(--ui-text-muted)">{{ t('job.failed24') }}</p>
          <div class="text-xl font-semibold tabular-nums sm:text-2xl" :class="stats?.totals.failed_24h ? 'text-(--ui-error)' : ''"><USkeleton v-if="!stats" class="mt-1 h-7 w-12" /><template v-else>{{ stats?.totals.failed_24h ?? 0 }}</template></div>
        </UCard>
        <UCard :ui="{ body: 'p-3 sm:p-4' }">
          <p class="text-xs text-(--ui-text-muted)">{{ t('job.cost24') }}</p>
          <div class="text-xl font-semibold tabular-nums sm:text-2xl"><USkeleton v-if="!stats" class="mt-1 h-7 w-12" /><template v-else>${{ (stats?.totals.cost_24h ?? 0).toFixed(2) }}</template></div>
        </UCard>
        <NuxtLink to="/projects">
          <UCard :ui="{ body: 'p-3 sm:p-4' }" class="h-full hover:bg-(--ui-bg-elevated)/40">
            <p class="text-xs text-(--ui-text-muted)">{{ t('home.project') }}</p>
            <div class="text-xl font-semibold tabular-nums sm:text-2xl"><USkeleton v-if="!proj" class="mt-1 h-7 w-12" /><template v-else>{{ projects.length }}</template></div>
          </UCard>
        </NuxtLink>
      </div>

      <UCard v-if="prov && proj && tpl && !setupDone">
        <template #header>
          <p class="font-medium">{{ t('home.setupTitle') }}</p>
          <p class="text-sm text-(--ui-text-muted)">{{ t('home.setupDesc') }}</p>
        </template>
        <ol class="space-y-3">
          <li v-for="(s, i) in steps" :key="s.title">
            <NuxtLink :to="s.to" class="flex items-center gap-3 rounded-md p-2 transition hover:bg-(--ui-bg-muted)">
              <span
                class="flex size-7 shrink-0 items-center justify-center rounded-full text-sm font-medium"
                :class="s.done ? 'bg-(--ui-success) text-white' : 'bg-(--ui-bg-accented)'"
              >
                <UIcon v-if="s.done" name="i-lucide-check" class="size-4" />
                <span v-else>{{ i + 1 }}</span>
              </span>
              <UIcon :name="s.icon" class="size-5 text-primary" />
              <div class="min-w-0 flex-1">
                <p class="font-medium">{{ s.title }}</p>
                <p class="text-sm text-(--ui-text-muted)">{{ s.text }}</p>
              </div>
              <UIcon name="i-lucide-chevron-right" class="size-4 text-(--ui-text-dimmed)" />
            </NuxtLink>
          </li>
        </ol>
      </UCard>

      <UCard v-if="!chatData || recentChats.length" :ui="{ body: 'p-0 sm:p-0' }">
        <template #header>
          <p class="font-semibold">{{ t('home.recentChats') }}</p>
        </template>
        <LoadingRows v-if="!chatData" :n="4" />
        <div class="divide-y divide-(--ui-border)">
          <NuxtLink
            v-for="c in recentChats" :key="c.id" :to="`/projects/${c.project_id}?tab=chat&c=${c.id}`"
            class="flex items-center gap-3 px-4 py-2.5 transition hover:bg-(--ui-bg-elevated)"
          >
            <UIcon
              :name="c.active_turn ? 'i-lucide-loader-circle' : c.source && c.source !== 'web' ? sourceIcon[c.source] : 'i-lucide-messages-square'"
              class="size-4 shrink-0 text-(--ui-text-muted)" :class="{ 'animate-spin text-primary': c.active_turn }"
            />
            <div class="min-w-0 flex-1">
              <p class="truncate text-sm font-medium">{{ c.title || t('chat.newThreadTitle') }}</p>
              <p class="truncate text-xs text-(--ui-text-muted)">{{ chatData?.projects[c.project_id] ?? c.project_id }} · {{ c.agent_name }} · {{ when(c.updated_at) }}</p>
            </div>
          </NuxtLink>
        </div>
      </UCard>
    </div>
    <WorkDetailModal
      v-if="shown" v-model:open="shownOpen" :title="shown.title" :subtitle="`${incKind(shown.kind)} · ${shown.project_name}`" :query="runsQuery(shown)"
    >
      <div v-if="shown.detail" class="rounded-lg border p-3" :class="shown.severity === 'error' ? 'border-(--ui-error)/40 bg-(--ui-error)/5' : 'border-(--ui-warning)/40 bg-(--ui-warning)/5'">
        <p class="text-xs font-medium" :class="shown.severity === 'error' ? 'text-(--ui-error)' : 'text-(--ui-warning)'">{{ t('home.whatHappened') }}</p>
        <pre class="mt-1 max-h-48 overflow-auto font-mono text-xs leading-5 whitespace-pre-wrap break-anywhere">{{ shown.detail }}</pre>
      </div>
      <!-- a process: its log -->
      <div v-if="shown.kind === 'process'" class="h-72">
        <LogTerminal :url="`/api/processes/${shown.id}/stream`" :title="shown.title" :empty="t('ops.emptyLog')" />
      </div>
      <!-- a monitor: its latest checks -->
      <div v-if="shown.kind === 'monitor'">
        <p class="mb-1.5 text-xs font-medium text-(--ui-text-muted)">{{ t('home.monitorEvents') }}</p>
        <LoadingRows v-if="!monitorEvents" :n="2" :icon="false" />
        <div v-else class="space-y-2">
          <div v-for="e in monitorEvents" :key="e.id" class="rounded-lg border border-(--ui-border) px-3 py-2 text-xs">
            <p><span class="font-medium">{{ e.kind }}</span> · {{ when(e.at) }}</p>
            <p class="break-anywhere text-(--ui-text-muted)">{{ e.message }}</p>
            <p v-if="e.analysis" class="mt-1 whitespace-pre-wrap break-anywhere">{{ e.analysis }}</p>
          </div>
        </div>
      </div>
      <template #actions>
        <UButton color="neutral" variant="outline" icon="i-lucide-eye" :label="t('home.view')" :to="shown.link" />
        <template v-if="isAdmin">
          <UButton v-if="canRetry(shown.kind)" color="neutral" variant="outline" icon="i-lucide-rotate-cw" :label="t('home.retry')" :loading="acting.has(shown.key + 'retry')" @click="act(shown, 'retry')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-search-check" :label="t('home.investigate')" @click="investigate(shown)" />
          <UButton color="neutral" variant="ghost" :label="t('home.dismiss')" :loading="acting.has(shown.key + 'dismiss')" @click="act(shown, 'dismiss').then(() => { shown = null })" />
        </template>
      </template>
    </WorkDetailModal>
    <UModal v-model:open="skipAllOpen" :title="t('home.skipAllTitle', { n: proposalsN })" :description="t('home.skipAllDesc')">
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="skipAllOpen = false" />
          <UButton color="error" icon="i-lucide-skip-forward" :label="t('home.skipAll')" :loading="skippingAll" @click="skipAll" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
