<script setup lang="ts">
// The Job page: one row per piece of work (a chat, a task, an automation's
// runs) with how its jobs went, not one per answer. A row opens that work.
const props = defineProps<{ project?: string, projects?: { id: string, name: string }[] }>()
const toast = useToast()
const { t, dateLocale } = useLang()

interface Group {
  key: string, kind: 'chat' | 'task' | 'automation' | 'job', project_id: string, project_name: string, source: Source,
  title: string, status: string, runs: number, failed: number, active: number, cost_usd: number, last_at: string, link: string, job_id?: string, conversation_id?: string
}

const origin = ref<'all' | Source>('all')
const search = ref('')
const searchQ = ref('')
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { searchQ.value = v.trim() }, 300)
})
const status = ref('')
const project = ref(props.project ?? '')
const since = ref('168h')
const groups = ref<Group[]>([])
const nextBefore = ref('')
const loading = ref(false)

function query(before = '') {
  const q = new URLSearchParams()
  const set = (k: string, v?: string) => { if (v) q.set(k, v) }
  set('project', project.value)
  set('source', origin.value === 'all' ? '' : origin.value)
  set('q', searchQ.value)
  set('status', status.value)
  set('since', since.value)
  set('before', before)
  set('limit', '20')
  return q.toString()
}
async function load(more = false) {
  loading.value = true
  try {
    const res = await $fetch<{ groups: Group[], next_before: string }>(`/api/jobs/groups?${query(more ? nextBefore.value : '')}`)
    groups.value = more ? [...groups.value, ...res.groups] : res.groups
    nextBefore.value = res.next_before
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loading.value = false
  }
}
watch([origin, searchQ, status, project, since], () => load(), { immediate: true })
useLive(['jobs', 'conversations'], () => load()) // retried, dismissed, removed: the table follows

// follow what is still running
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    if (groups.value.some(g => g.active > 0)) load()
  }, 5000)
})
onBeforeUnmount(() => clearInterval(timer))

const ALL = '__all'
const bind = (r: Ref<string>) => computed({ get: () => r.value || ALL, set: (v: string) => { r.value = v === ALL ? '' : v } })
const statusSel = bind(status)
const projectSel = bind(project)
const selectItems = computed(() => ({
  status: [{ label: t('job.statusAll'), value: ALL }, ...(['running', 'pending', 'done', 'failed', 'needs_input', 'cancelled', 'skipped'] as const).map(v => ({ label: t(`job.status.${v}`), value: v }))],
  since: [{ label: t('job.range24h'), value: '24h' }, { label: t('job.range7d'), value: '168h' }, { label: t('job.range30d'), value: '720h' }],
  project: [{ label: t('job.projectAll'), value: ALL }, ...(props.projects ?? []).map(p => ({ label: p.name, value: p.id }))]
}))
const activeFilters = computed(() => [origin.value !== 'all', !!status.value, !!project.value && !props.project, since.value !== '168h'].filter(Boolean).length)

const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
const usd = (v: number) => v >= 1 ? `$${v.toFixed(2)}` : v > 0 ? `$${v.toFixed(3)}` : ''
const kindIcon = (g: Group) => g.active ? 'i-lucide-loader-circle'
  : g.source !== 'web' ? sourceIcon[g.source]
    : ({ chat: 'i-lucide-messages-square', task: 'i-lucide-list-todo', automation: 'i-lucide-alarm-clock', job: 'i-lucide-square-terminal' })[g.kind]
// sorting the page shown, and bars that compare rows at a glance
type SortKey = 'last' | 'runs' | 'failed' | 'cost'
const sortBy = ref<SortKey>('last')
const sorted = computed(() => {
  const v = (g: Group) => sortBy.value === 'runs' ? g.runs : sortBy.value === 'failed' ? g.failed / Math.max(g.runs, 1) : sortBy.value === 'cost' ? g.cost_usd : new Date(g.last_at).getTime()
  return [...groups.value].sort((a, b) => v(b) - v(a))
})
const maxRuns = computed(() => Math.max(1, ...groups.value.map(g => g.runs)))
const maxCost = computed(() => Math.max(0.0001, ...groups.value.map(g => g.cost_usd)))
const failRate = (g: Group) => g.runs ? Math.round(g.failed / g.runs * 100) : 0
// what the page shown adds up to
const totals = computed(() => ({
  works: groups.value.length,
  runs: groups.value.reduce((n, g) => n + g.runs, 0),
  failed: groups.value.reduce((n, g) => n + g.failed, 0),
  cost: groups.value.reduce((n, g) => n + g.cost_usd, 0)
}))
const ago = (d: string) => {
  const m = Math.round((Date.now() - new Date(d).getTime()) / 60000)
  if (m < 1) return t('job.agoNow')
  if (m < 60) return t('job.agoMin', { n: m })
  if (m < 60 * 24) return t('job.agoHour', { n: Math.round(m / 60) })
  return when(d)
}
const statusColor = (s: string) => s === 'done' ? 'success' : s === 'failed' ? 'error' : s === 'running' || s === 'pending' ? 'info' : s === 'needs_input' ? 'warning' : 'neutral'
const kindLabel = (k: Group['kind']) => t(`job.groupKind.${k}`)
const detail = ref<string | null>(null) // a job of its own, shown in place
// a chat: its runs at a glance (each one's error), opening the chat is a click away
const work = ref<Group | null>(null)
const workOpen = computed({ get: () => !!work.value, set: (v: boolean) => { if (!v) work.value = null } })
function open(g: Group) {
  if (g.job_id) detail.value = g.job_id
  else if (g.conversation_id) work.value = g
  else navigateTo(g.link)
}
</script>

<template>
  <div class="space-y-3">
    <div class="flex items-center gap-2">
      <UInput v-model="search" size="sm" icon="i-lucide-search" class="min-w-0 flex-1 sm:max-w-72" :placeholder="t('job.search')" />
      <FilterButton :active="activeFilters">
        <SourceFilter v-model="origin" />
        <USelect v-model="statusSel" size="sm" :items="selectItems.status" class="w-full" />
        <USelect v-if="projects?.length" v-model="projectSel" size="sm" :items="selectItems.project" class="w-full" />
        <USelect v-model="since" size="sm" :items="selectItems.since" class="w-full" />
      </FilterButton>
    </div>

    <!-- the page shown in numbers -->
    <div class="flex flex-wrap gap-x-6 gap-y-1 text-xs text-(--ui-text-muted)">
      <span><b class="text-sm text-(--ui-text) tabular-nums">{{ totals.works }}</b> {{ t('job.sumWorks') }}</span>
      <span><b class="text-sm text-(--ui-text) tabular-nums">{{ totals.runs }}</b> {{ t('job.sumRuns') }}</span>
      <span><b class="text-sm tabular-nums" :class="totals.failed ? 'text-(--ui-error)' : 'text-(--ui-text)'">{{ totals.failed }}</b> {{ t('job.sumFailed') }}</span>
      <span><b class="text-sm text-(--ui-text) tabular-nums">${{ totals.cost.toFixed(2) }}</b> {{ t('job.sumCost') }}</span>
    </div>

    <UCard :ui="{ body: 'p-0 sm:p-0' }">
      <LoadingRows v-if="loading && !groups.length" :n="6" />
      <p v-else-if="!groups.length" class="p-4 text-sm text-(--ui-text-muted)">{{ t('job.empty') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="border-b border-(--ui-border) text-left text-xs text-(--ui-text-muted)">
            <tr>
              <th class="px-4 py-2 font-medium">{{ t('job.colWork') }}</th>
              <th
                v-for="c in ([['runs', 'job.colRuns'], ['failed', 'job.colFailRate'], ['cost', 'job.colCost'], ['last', 'job.colLast']] as const)" :key="c[0]"
                class="px-3 py-2 font-medium whitespace-nowrap" :class="[c[0] === 'last' ? 'text-right' : '', c[0] !== 'cost' ? 'max-sm:hidden' : 'max-sm:text-right']"
              >
                <button type="button" class="inline-flex items-center gap-1 hover:text-(--ui-text)" :class="sortBy === c[0] && 'text-(--ui-text)'" @click="sortBy = c[0]">
                  {{ t(c[1]) }}<UIcon v-if="sortBy === c[0]" name="i-lucide-arrow-down" class="size-3" />
                </button>
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-(--ui-border)">
            <tr v-for="g in sorted" :key="g.key" class="cursor-pointer transition hover:bg-(--ui-bg-elevated)/60" @click="open(g)">
              <td class="max-w-0 px-4 py-2.5 max-sm:px-3">
                <div class="flex min-w-0 items-center gap-3">
                  <span class="flex size-8 shrink-0 items-center justify-center rounded-lg bg-(--ui-bg-elevated)">
                    <UIcon :name="kindIcon(g)" class="size-4 text-(--ui-text-muted)" :class="{ 'animate-spin text-primary': g.active }" />
                  </span>
                  <div class="min-w-0">
                    <p class="truncate font-medium">{{ g.title || t('chat.newThreadTitle') }}</p>
                    <p class="flex items-center gap-1.5 truncate text-xs text-(--ui-text-muted)">
                      <span>{{ kindLabel(g.kind) }}</span>·<span class="truncate">{{ g.project_name }}</span>
                      <UBadge v-if="g.active || g.status !== 'done'" :label="t(`job.status.${g.status}` as 'job.status.done')" :color="statusColor(g.status)" variant="subtle" size="xs" class="shrink-0" />
                    </p>
                  </div>
                </div>
              </td>
              <td class="w-36 px-3 py-2.5 max-sm:hidden">
                <div class="flex items-center gap-2">
                  <span class="w-8 text-right tabular-nums">{{ g.runs }}</span>
                  <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-(--ui-bg-elevated)"><span class="block h-full rounded-full bg-primary/70" :style="{ width: `${g.runs / maxRuns * 100}%` }" /></span>
                </div>
              </td>
              <td class="w-24 px-3 py-2.5 tabular-nums max-sm:hidden">
                <span v-if="g.failed" class="text-(--ui-error)">{{ failRate(g) }}%<span class="text-xs text-(--ui-text-muted)"> · {{ g.failed }}</span></span>
                <span v-else class="text-(--ui-text-dimmed)">0%</span>
              </td>
              <td class="w-36 px-3 py-2.5 max-sm:w-auto max-sm:text-right">
                <!-- a phone: the cost, and when, under it -->
                <p class="text-xs text-(--ui-text-muted) sm:hidden">{{ ago(g.last_at) }}</p>
                <div class="flex items-center gap-2 max-sm:justify-end">
                  <span class="w-14 text-right tabular-nums max-sm:w-auto">{{ usd(g.cost_usd) || '—' }}</span>
                  <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-(--ui-bg-elevated) max-sm:hidden"><span class="block h-full rounded-full bg-primary/70" :style="{ width: `${g.cost_usd / maxCost * 100}%` }" /></span>
                </div>
              </td>
              <td class="w-28 px-3 py-2.5 text-right text-xs whitespace-nowrap text-(--ui-text-muted) max-sm:hidden">{{ ago(g.last_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="nextBefore" class="border-t border-(--ui-border) p-2 text-center">
        <UButton size="xs" color="neutral" variant="ghost" :loading="loading" :label="t('job.more')" @click="load(true)" />
      </div>
    </UCard>
    <JobDetailModal :job-id="detail" @close="detail = null" />
    <WorkDetailModal
      v-if="work" v-model:open="workOpen" :title="work.title || t('chat.newThreadTitle')" :subtitle="`${kindLabel(work.kind)} · ${work.project_name}`"
      :query="`conversation=${work.conversation_id}&limit=50`"
      :summary="[
        { label: t('job.colRuns'), value: String(work.runs) },
        { label: t('job.sumFailed'), value: String(work.failed), bad: work.failed > 0 },
        { label: t('job.colCost'), value: usd(work.cost_usd) || '—' },
        { label: t('job.colLast'), value: ago(work.last_at) }
      ]"
    >
      <template #actions>
        <UButton :to="work.link" icon="i-lucide-messages-square" :label="t('job.openChat')" color="neutral" variant="outline" />
      </template>
    </WorkDetailModal>
  </div>
</template>
