<script setup lang="ts">
// Jobs (every chat answer, task and automation run) with filters, and
// actions: open the chat or task, stop, run again (ADR-040).
const props = defineProps<{ filter?: { project?: string, origin_id?: string, origin_ids?: string[], kind?: string }, showFilters?: boolean, projects?: { id: string, name: string }[] }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t, dateLocale } = useLang()

const kind = ref(props.filter?.kind ?? '')
const origin = ref<'all' | Source>('all') // where it came from: web, a bot, an automation
const search = ref('')
const searchQ = ref('') // the search, once typing pauses
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { searchQ.value = v.trim() }, 300)
})
const status = ref('')
const project = ref(props.filter?.project ?? '')
const since = ref('168h')
const jobs = ref<Job[]>([])
const nextBefore = ref('')
const loading = ref(false)

function query(before = '') {
  const q = new URLSearchParams()
  const set = (k: string, v?: string) => { if (v) q.set(k, v) }
  set('project', project.value)
  set('origin_id', props.filter?.origin_id)
  set('origin_ids', props.filter?.origin_ids?.join(','))
  set('kind', kind.value)
  set('source', origin.value === 'all' ? '' : origin.value)
  set('q', searchQ.value)
  set('status', status.value)
  set('since', since.value)
  set('before', before)
  set('limit', '20') // a page: the rest comes with "Xem thêm"
  return q.toString()
}
async function load(more = false) {
  loading.value = true
  try {
    const res = await $fetch<{ jobs: Job[], next_before: string }>(`/api/jobs?${query(more ? nextBefore.value : '')}`)
    jobs.value = more ? [...jobs.value, ...res.jobs] : res.jobs
    nextBefore.value = res.next_before
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loading.value = false
  }
}
watch([kind, origin, searchQ, status, project, since, () => props.filter?.origin_id], () => load(), { immediate: true })

// follow running and waiting jobs
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    if (jobs.value.some(j => j.status === 'pending' || j.status === 'running')) load()
  }, 5000)
})
onBeforeUnmount(() => clearInterval(timer))

const selectItems = computed(() => ({
  kind: [{ label: t('job.kindAll'), value: '' }, ...(['chat_turn', 'script'] as const).map(v => ({ label: t(`job.kind.${v}`), value: v }))],
  status: [{ label: t('job.statusAll'), value: '' }, ...(['running', 'pending', 'done', 'failed', 'needs_input', 'cancelled', 'skipped'] as const).map(v => ({ label: t(`job.status.${v}`), value: v }))],
  since: [{ label: t('job.range24h'), value: '24h' }, { label: t('job.range7d'), value: '168h' }, { label: t('job.range30d'), value: '720h' }],
  project: [{ label: t('job.projectAll'), value: '' }, ...(props.projects ?? []).map(p => ({ label: p.name, value: p.id }))]
}))
// USelect cannot hold "" as a value: use a sentinel for "all"
const ALL = '__all'
const bind = (r: Ref<string>) => computed({ get: () => r.value || ALL, set: (v: string) => { r.value = v === ALL ? '' : v } })
// how many filters differ from the default (shown on the filter icon)
const activeFilters = computed(() => [origin.value !== 'all', !!kind.value, !!status.value, !!project.value && !props.filter?.project, since.value !== '168h'].filter(Boolean).length)
const kindSel = bind(kind)
const statusSel = bind(status)
const projectSel = bind(project)
const withAll = (items: { label: string, value: string }[]) => items.map(i => ({ ...i, value: i.value || ALL }))

const usd = (v: number) => v >= 1 ? `$${v.toFixed(2)}` : v > 0 ? `$${v.toFixed(3)}` : '—'
const secs = (ms: number) => !ms ? '—' : ms >= 60000 ? `${(ms / 60000).toFixed(1)}m` : `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)}s`
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
// where a job came from, as the source filter names it
const sourceOf = (j: Job): Source => j.trigger === 'discord' || j.trigger === 'telegram' ? j.trigger : j.origin === 'automation' ? 'auto' : 'web'
const source = (j: Job) => j.origin === 'automation'
  ? `${j.automation_name || t('job.origin.automation')} · ${t(`job.trigger.${j.trigger}` as 'job.trigger.ui')}`
  : j.created_by || t('job.origin.user')

const prefill = useState<{ text: string, files: unknown[], conversationId?: string } | null>('chat-prefill', () => null)
const detail = ref<string | null>(null) // a script job shown in the modal
function openJob(j: Job) {
  if (j.kind === 'script') {
    detail.value = j.id
    return
  }
  if (j.kind === 'chat_turn' && j.conversation_id) {
    prefill.value = { text: '', files: [], conversationId: j.conversation_id }
    return navigateTo({ path: `/projects/${j.project_id}`, query: { tab: 'chat' } })
  }
  detail.value = j.id // a task from before Việc was dropped: its job
}
async function act(j: Job, what: 'cancel' | 'retry') {
  try {
    await $fetch(`/api/jobs/${j.id}/${what}`, { method: 'POST', body: {} })
    toast.add({ title: what === 'cancel' ? t('job.cancelled') : t('job.retried'), color: 'success' })
    await load()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
defineExpose({ reload: () => load() })
useLive(['jobs'], () => load())
</script>

<template>
  <div class="space-y-3">
    <div v-if="showFilters" class="flex items-center gap-2">
      <UInput v-model="search" size="sm" icon="i-lucide-search" class="min-w-0 flex-1 sm:max-w-72" :placeholder="t('job.search')" />
      <FilterButton :active="activeFilters">
        <SourceFilter v-model="origin" />
        <USelect v-model="kindSel" size="sm" :items="withAll(selectItems.kind)" class="w-full" />
        <USelect v-model="statusSel" size="sm" :items="withAll(selectItems.status)" class="w-full" />
        <USelect v-if="projects?.length" v-model="projectSel" size="sm" :items="withAll(selectItems.project)" class="w-full" />
        <USelect v-model="since" size="sm" :items="selectItems.since" class="w-full" />
      </FilterButton>
    </div>

    <UCard :ui="{ body: 'p-0 sm:p-0' }">
      <p v-if="!jobs.length && !loading" class="p-4 text-sm text-(--ui-text-muted)">{{ t('job.empty') }}</p>
      <div class="overflow-x-auto">
        <table v-if="jobs.length" class="w-full text-sm">
          <thead class="text-left text-xs text-(--ui-text-muted)">
            <tr class="border-b border-(--ui-border)">
              <th class="px-4 py-2 font-medium">{{ t('job.colStatus') }}</th>
              <th class="px-2 py-2 font-medium">{{ t('job.colTitle') }}</th>
              <th class="px-2 py-2 font-medium">{{ t('job.colSource') }}</th>
              <th class="px-2 py-2 font-medium">{{ t('job.colAgent') }}</th>
              <th class="px-2 py-2 text-right font-medium">{{ t('job.colCost') }}</th>
              <th class="px-2 py-2 text-right font-medium">{{ t('job.colTime') }}</th>
              <th class="px-2 py-2 font-medium">{{ t('job.colCreated') }}</th>
              <th class="px-4 py-2" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="j in jobs" :key="j.id" class="border-b border-(--ui-border) last:border-0 hover:bg-(--ui-bg-elevated)/40">
              <td class="px-4 py-2"><JobStatusBadge :status="j.status" /></td>
              <td class="max-w-72 px-2 py-2">
                <span class="flex items-center gap-1.5">
                  <UIcon :name="j.kind === 'task' ? 'i-lucide-list-todo' : j.kind === 'script' ? 'i-lucide-square-terminal' : 'i-lucide-messages-square'" class="size-3.5 shrink-0 text-(--ui-text-muted)" />
                  <span class="truncate" :title="j.title">{{ j.title || '—' }}</span>
                </span>
                <span v-if="j.error" class="block truncate text-xs text-(--ui-error)" :title="j.error">{{ j.error }}</span>
                <span v-if="!filter?.project && j.project_name" class="block truncate text-xs text-(--ui-text-muted)">{{ j.project_name }}</span>
              </td>
              <td class="max-w-48 px-2 py-2 text-xs text-(--ui-text-muted)">
                <span class="flex items-center gap-1.5">
                  <UIcon :name="sourceIcon[sourceOf(j)]" class="size-3.5 shrink-0" :title="t(`source.${sourceOf(j)}`)" />
                  <span class="truncate">{{ source(j) }}</span>
                </span>
              </td>
              <td class="max-w-36 truncate px-2 py-2 text-xs">{{ j.agent_name || '—' }}</td>
              <td class="px-2 py-2 text-right text-xs tabular-nums">{{ usd(j.cost_usd) }}</td>
              <td class="px-2 py-2 text-right text-xs tabular-nums">{{ secs(j.duration_ms) }}</td>
              <td class="whitespace-nowrap px-2 py-2 text-xs text-(--ui-text-muted)">{{ when(j.created_at) }}</td>
              <td class="whitespace-nowrap px-4 py-2 text-right">
                <UButton v-if="j.conversation_id || j.task_id || j.kind === 'script'" size="xs" color="neutral" variant="ghost" icon="i-lucide-external-link" :title="t('job.open')" :aria-label="t('job.open')" @click="openJob(j)" />
                <UButton v-if="isAdmin && (j.status === 'pending' || j.status === 'running')" size="xs" color="neutral" variant="ghost" icon="i-lucide-square" :title="t('job.cancel')" :aria-label="t('job.cancel')" @click="act(j, 'cancel')" />
                <UButton v-if="isAdmin && ['failed', 'cancelled', 'skipped'].includes(j.status) && (j.origin === 'automation' || j.kind === 'task')" size="xs" color="neutral" variant="ghost" icon="i-lucide-rotate-ccw" :title="t('job.retry')" :aria-label="t('job.retry')" @click="act(j, 'retry')" />
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="nextBefore" class="border-t border-(--ui-border) p-2 text-center">
        <UButton size="xs" color="neutral" variant="ghost" :loading="loading" :label="t('job.more')" @click="load(true)" />
      </div>
    </UCard>
    <JobDetailModal :job-id="detail" @close="detail = null" @open="(j: Job) => { detail = null; openJob(j) }" />
  </div>
</template>
