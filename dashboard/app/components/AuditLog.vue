<script setup lang="ts">
// Change log (ADR-043): who changed what (a person, an agent and who approved
// it, an automation), where it came from and the value before/after.
import type { MessageKey } from '~/locales/vi'

const props = defineProps<{ filter?: { project?: string, resource?: string, resource_id?: string }, showFilters?: boolean, projects?: { id: string, name: string }[], compact?: boolean }>()
const toast = useToast()
const { t, dateLocale } = useLang()

const kind = ref('')
const via = ref('')
const resource = ref(props.filter?.resource ?? '')
const project = ref(props.filter?.project ?? '')
// with the filters hidden (a page's own history) nothing limits the range
const since = ref(props.showFilters ? '168h' : '')
const entries = ref<AuditEntry[]>([])
const nextBefore = ref('')
const loading = ref(false)
const open = ref<string | null>(null)

function query(before = '') {
  const q = new URLSearchParams()
  const set = (k: string, v?: string) => { if (v) q.set(k, v) }
  set('project', project.value)
  set('resource', resource.value)
  set('resource_id', props.filter?.resource_id)
  set('actor_kind', kind.value)
  set('via', via.value)
  set('since', since.value)
  set('before', before)
  q.set('limit', '50')
  return q.toString()
}
async function load(more = false) {
  loading.value = true
  try {
    const res = await $fetch<{ entries: AuditEntry[], next_before: string }>(`/api/audit?${query(more ? nextBefore.value : '')}`)
    entries.value = more ? [...entries.value, ...res.entries] : res.entries
    nextBefore.value = res.next_before
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loading.value = false
  }
}
watch([kind, via, resource, project, since, () => props.filter?.resource_id], () => load(), { immediate: true })

const resources = ['automation', 'agent', 'monitor', 'process', 'project', 'policy', 'provider', 'org_model', 'usage_settings', 'action', 'patch', 'job'] as const
// USelect cannot hold "" as a value: use a sentinel for "all"
const ALL = '__all'
const bind = (r: Ref<string>) => computed({ get: () => r.value || ALL, set: (v: string) => { r.value = v === ALL ? '' : v } })
const kindSel = bind(kind)
const viaSel = bind(via)
const resourceSel = bind(resource)
const projectSel = bind(project)
const selectItems = computed(() => ({
  kind: [{ label: t('audit.kindAll'), value: ALL }, ...(['human', 'agent', 'automation', 'system'] as const).map(v => ({ label: t(`audit.kind.${v}`), value: v }))],
  via: [{ label: t('audit.viaAll'), value: ALL }, ...(['ui', 'chat', 'assistant', 'mcp', 'automation', 'api'] as const).map(v => ({ label: t(`audit.via.${v}`), value: v }))],
  resource: [{ label: t('audit.resourceAll'), value: ALL }, ...resources.map(v => ({ label: t(`audit.resource.${v}`), value: v }))],
  project: [{ label: t('job.projectAll'), value: ALL }, ...(props.projects ?? []).map(p => ({ label: p.name, value: p.id }))],
  since: [{ label: t('job.range24h'), value: '24h' }, { label: t('job.range7d'), value: '168h' }, { label: t('job.range30d'), value: '720h' }]
}))

// a known key, or the backend's raw value
function label(prefix: string, v: string) {
  const k = `${prefix}${v}` as MessageKey
  const s = t(k)
  return s === k ? v : s
}
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
const targetName = (e: AuditEntry) => String(e.after?.name ?? e.before?.name ?? e.resource_id ?? '')
const show = (v: unknown) => v === '***' ? t('audit.secretChanged') : v === undefined ? '—' : typeof v === 'string' ? v : JSON.stringify(v)

const prefill = useState<{ text: string, files: unknown[], conversationId?: string } | null>('chat-prefill', () => null)
const jobId = ref<string | null>(null)
function openChat(e: AuditEntry) {
  prefill.value = { text: '', files: [], conversationId: e.conversation_id }
  return navigateTo({ path: `/projects/${e.project_id}`, query: { tab: 'chat' } })
}
function openJob(j: Job) {
  jobId.value = null
  if (j.kind === 'chat_turn' && j.conversation_id) {
    prefill.value = { text: '', files: [], conversationId: j.conversation_id }
    return navigateTo({ path: `/projects/${j.project_id}`, query: { tab: 'chat' } })
  }
}
// how many filters differ from the default (shown on the filter icon)
const activeFilters = computed(() => [!!kind.value, !!via.value, !!resource.value && !props.filter?.resource, !!project.value && !props.filter?.project, since.value !== '168h'].filter(Boolean).length)
defineExpose({ reload: () => load() })
useLive(['audit_log'], () => load())
</script>

<template>
  <div class="space-y-3">
    <div v-if="showFilters" class="flex justify-end">
      <FilterButton :active="activeFilters">
        <USelect v-model="kindSel" size="sm" :items="selectItems.kind" class="w-full" />
        <USelect v-model="viaSel" size="sm" :items="selectItems.via" class="w-full" />
        <USelect v-model="resourceSel" size="sm" :items="selectItems.resource" class="w-full" />
        <USelect v-if="projects?.length" v-model="projectSel" size="sm" :items="selectItems.project" class="w-full" />
        <USelect v-model="since" size="sm" :items="selectItems.since" class="w-full" />
      </FilterButton>
    </div>

    <UCard :ui="{ body: 'p-0 sm:p-0' }">
      <p v-if="!entries.length && !loading" class="p-4 text-sm text-(--ui-text-muted)">{{ t('audit.empty') }}</p>
      <div class="overflow-x-auto">
        <table v-if="entries.length" class="w-full text-sm">
          <thead class="text-left text-xs text-(--ui-text-muted)">
            <tr class="border-b border-(--ui-border)">
              <th class="px-4 py-2 font-medium">{{ t('audit.colTime') }}</th>
              <th class="px-2 py-2 font-medium">{{ t('audit.colWho') }}</th>
              <th class="px-2 py-2 font-medium">{{ t('audit.colChange') }}</th>
              <th class="px-2 py-2 font-medium">{{ t('audit.colTarget') }}</th>
              <th v-if="!compact" class="px-4 py-2 font-medium">{{ t('audit.colSource') }}</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="e in entries" :key="e.id">
              <tr class="cursor-pointer border-b border-(--ui-border) last:border-0 hover:bg-(--ui-bg-elevated)/40" @click="open = open === e.id ? null : e.id">
                <td class="whitespace-nowrap px-4 py-2 text-xs text-(--ui-text-muted)">{{ when(e.at) }}</td>
                <td class="max-w-56 px-2 py-2">
                  <span class="flex items-center gap-1.5">
                    <UIcon :name="whoIcon(e.actor_kind)" class="size-3.5 shrink-0 text-(--ui-text-muted)" :title="label('audit.kind.', e.actor_kind)" />
                    <span class="truncate">{{ e.actor_name || label('audit.kind.', e.actor_kind) }}</span>
                  </span>
                  <span v-if="e.approved_by" class="block truncate text-xs text-(--ui-text-muted)">{{ t('audit.approvedBy', { name: e.approved_by }) }}</span>
                  <span v-else-if="e.actor_kind === 'agent' && e.detail?.auto" class="block truncate text-xs text-(--ui-text-muted)">{{ t('audit.autoApplied') }}</span>
                </td>
                <td class="px-2 py-2">
                  <span class="flex items-center gap-1.5">
                    <span>{{ label('audit.action.', e.action) }}</span>
                    <UBadge v-if="!e.ok" color="error" variant="subtle" size="sm" :label="t('audit.failed')" />
                  </span>
                </td>
                <td class="max-w-64 px-2 py-2">
                  <span class="block truncate" :title="targetName(e)">{{ targetName(e) || '—' }}</span>
                  <span v-if="!compact && !filter?.project && e.project_name" class="block truncate text-xs text-(--ui-text-muted)">{{ e.project_name }}</span>
                </td>
                <td v-if="!compact" class="whitespace-nowrap px-4 py-2" @click.stop>
                  <span class="mr-1 text-xs text-(--ui-text-muted)">{{ label('audit.via.', e.via) }}</span>
                  <UButton v-if="e.conversation_id && e.project_id" size="xs" color="neutral" variant="ghost" icon="i-lucide-messages-square" :title="t('audit.openChat')" :aria-label="t('audit.openChat')" @click="openChat(e)" />
                  <UButton v-if="e.job_id" size="xs" color="neutral" variant="ghost" icon="i-lucide-list-checks" :title="t('audit.openJob')" :aria-label="t('audit.openJob')" @click="jobId = e.job_id" />
                </td>
              </tr>
              <tr v-if="open === e.id" class="border-b border-(--ui-border) bg-(--ui-bg-elevated)/30">
                <td :colspan="compact ? 4 : 5" class="px-4 py-3">
                  <table v-if="auditDiff(e.before, e.after).length" class="w-full text-xs">
                    <thead class="text-left text-(--ui-text-muted)">
                      <tr>
                        <th class="w-48 py-1 pr-2 font-medium">{{ t('audit.colField') }}</th>
                        <th class="py-1 pr-2 font-medium">{{ t('audit.colBefore') }}</th>
                        <th class="py-1 font-medium">{{ t('audit.colAfter') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="d in auditDiff(e.before, e.after)" :key="d.key" class="align-top">
                        <td class="py-1 pr-2 font-mono">{{ d.key }}</td>
                        <td class="max-w-80 break-all py-1 pr-2 text-(--ui-error)"><span class="line-through decoration-(--ui-error)/40">{{ show(d.before) }}</span></td>
                        <td class="max-w-80 break-all py-1 text-(--ui-success)">{{ show(d.after) }}</td>
                      </tr>
                    </tbody>
                  </table>
                  <template v-else>
                    <p class="text-xs text-(--ui-text-muted)">{{ t('audit.noDiff') }}</p>
                    <code v-if="e.detail && Object.keys(e.detail).length" class="mt-1 block break-all text-xs text-(--ui-text-muted)">{{ JSON.stringify(e.detail) }}</code>
                  </template>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
      <div v-if="nextBefore" class="border-t border-(--ui-border) p-2 text-center">
        <UButton size="xs" color="neutral" variant="ghost" :loading="loading" :label="t('audit.more')" @click="load(true)" />
      </div>
    </UCard>
    <JobDetailModal :job-id="jobId" @close="jobId = null" @open="openJob" />
  </div>
</template>
