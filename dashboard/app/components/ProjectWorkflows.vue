<script setup lang="ts">
// A project's workflows (ADR-098): its own copies of the library's, each role
// filled by one of its agents; a chat runs one with /key. Runs: every run,
// the chat that called it, its input and output. Desktop shows both side by
// side; mobile switches between them (?wv=runs). ?w= filters the runs.
import type { MessageKey } from '~/locales/vi'

const props = defineProps<{ projectId: string }>()
const toast = useToast()
const saveError = useSaveError()
const { t, when } = useLang()
const { isAdmin } = useAuth()
const route = useRoute()
const router = useRouter()
const view = ref(route.query.wv === 'runs' ? 'runs' : 'list')
watch(view, v => router.replace({ query: { ...route.query, wv: v === 'runs' ? 'runs' : undefined } }))
const views = computed(() => [
  { value: 'list', label: t('wf.section'), icon: 'i-lucide-workflow' },
  { value: 'runs', label: t('wf.runs.title'), icon: 'i-lucide-history' }
])

const { data, refresh, pending, error: loadError } = useLiveFetch<{ workflows: ProjectWorkflow[], library: LibraryWorkflow[] }>(() => `/api/projects/${props.projectId}/workflows`, { lazy: true })
const { data: agentsData } = useLiveFetch<{ agents: Agent[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const workflows = computed(() => data.value?.workflows ?? [])

// the library's not installed yet (by the key it was copied from)
const installable = computed(() => {
  const have = new Set(workflows.value.flatMap(w => [w.key, w.source_key]))
  return (data.value?.library ?? []).filter(l => !have.has(l.def.key))
})
const installing = ref(false)
const installItems = computed(() => [installable.value.length
  ? installable.value.map(l => ({ label: `${l.def.name} · /${l.def.key}`, icon: 'i-lucide-workflow', disabled: !!l.error, onSelect: () => install(l.def.key) }))
  : [{ label: t('wf.allInstalled'), disabled: true }]])

// a role's agent: "_" leaves it to the coordinator (a select item needs a value)
const NONE = '_'
const agentItems = computed(() => [
  { label: t('wf.unbound'), value: NONE },
  ...(agentsData.value?.agents ?? []).filter(a => a.enabled !== false).map(a => ({ label: a.name, value: a.id }))
])

// a sub-workflow role: who coordinates that run ("_": this run's coordinator)
const flowItems = computed(() => [{ label: t('wf.subCoordinator'), value: NONE }, ...agentItems.value.slice(1)])
const installedKeys = computed(() => new Set(workflows.value.map(w => w.key)))

const busy = ref('') // the workflow being changed
async function call(w: ProjectWorkflow | null, fn: () => Promise<unknown>, done?: string) {
  busy.value = w?.id ?? '*'
  try {
    await fn()
    if (done) toast.add({ title: done, color: 'success' })
    await refresh()
  } catch (e) {
    saveError(e, refresh)
  } finally {
    busy.value = ''
  }
}
// version (ADR-072): CAS so a stale bindings/enabled save 409s instead of blind-overwriting a concurrent edit
const patch = (w: ProjectWorkflow, body: object) => call(w, () => $fetch(`/api/workflows/${w.id}`, { method: 'PATCH', body: { ...body, version: w.version } }))
function bind(w: ProjectWorkflow, role: string, agent: string) {
  const bindings = { ...w.bindings }
  if (agent === NONE) delete bindings[role]
  else bindings[role] = agent
  return patch(w, { bindings })
}
async function install(key: string) {
  installing.value = true
  await call(null, () => $fetch(`/api/projects/${props.projectId}/workflows`, { method: 'POST', body: { key } }), t('wf.installed'))
  installing.value = false
}
function remove(w: ProjectWorkflow) {
  if (!confirm(t('wf.deleteConfirm', { name: w.name }))) return
  return call(w, () => $fetch(`/api/workflows/${w.id}`, { method: 'DELETE' }))
}
function toLibrary(w: ProjectWorkflow) {
  const there = (data.value?.library ?? []).some(l => l.def.key === w.key)
  if (there && !confirm(t('wf.overwrite', { key: w.key }))) return
  return call(w, () => $fetch(`/api/workflows/${w.id}/to-library`, { method: 'POST', body: {} }), t('wf.savedToLibrary'))
}
const fromLibrary = (w: ProjectWorkflow) => call(w, () => $fetch(`/api/workflows/${w.id}/from-library`, { method: 'POST', body: {} }), t('wf.updated'))

const editTo = (w?: ProjectWorkflow) => ({ path: `/projects/${props.projectId}/workflows/edit`, query: w ? { w: w.id } : {} })
function menu(w: ProjectWorkflow) {
  return [[
    { label: t('wf.edit'), icon: 'i-lucide-pencil', to: editTo(w) },
    { label: t('wf.runs.of'), icon: 'i-lucide-history', onSelect: () => runsOf(w) },
    ...(w.has_update ? [{ label: t('wf.fromLibrary'), icon: 'i-lucide-download', onSelect: () => fromLibrary(w) }] : []),
    { label: t('wf.toLibrary'), icon: 'i-lucide-library', disabled: !!w.error, onSelect: () => toLibrary(w) }
  ], [{ label: t('wf.delete'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => remove(w) }]]
}

const statusColor: Record<WorkflowRun['status'], 'info' | 'success' | 'error' | 'neutral'> = { running: 'info', done: 'success', failed: 'error', stopped: 'neutral' }
const runLink = (r: WorkflowRun) => `/projects/${props.projectId}/workflows/runs/${r.id}`
// one workflow's runs: the runs view, filtered (its ?w=)
const runsOf = (w: ProjectWorkflow) => router.replace({ query: { ...route.query, wv: 'runs', w: w.id } }).then(() => { view.value = 'runs' })
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <SegmentedNav v-model="view" :items="views" class="lg:hidden" />
      <div class="flex items-center gap-2" :class="view !== 'list' && 'max-lg:hidden'">
        <p class="text-sm text-(--ui-text-muted)">{{ t('wf.count', { n: workflows.length }) }}</p>
        <InfoTip :text="t('wf.projectInfo')" icon-class="size-4 text-(--ui-text-muted)" />
      </div>
      <div v-if="isAdmin" class="ms-auto flex gap-2" :class="view !== 'list' && 'max-lg:hidden'">
        <UDropdownMenu :items="installItems" :content="{ align: 'end' }" :ui="{ content: 'max-h-80' }">
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-download" :label="t('wf.install')" :loading="installing" />
        </UDropdownMenu>
        <UButton size="sm" icon="i-lucide-plus" :label="t('wf.new')" :to="editTo()" />
      </div>
    </div>

    <div class="lg:grid lg:grid-cols-2 lg:items-start lg:gap-4">
      <div class="space-y-4" :class="view !== 'list' && 'max-lg:hidden'">
        <UAlert v-if="loadError" color="error" variant="subtle" :title="apiError(loadError)" />
        <LoadingRows v-else-if="pending && !data" />
        <div v-else-if="!workflows.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
          <UIcon name="i-lucide-workflow" class="mx-auto size-8 text-(--ui-text-dimmed)" />
          <p class="mt-2 font-medium">{{ t('wf.empty') }}</p>
          <p class="text-sm text-(--ui-text-muted)">{{ t('wf.emptyHint') }}</p>
        </div>

        <template v-else>
          <div v-for="w in workflows" :key="w.id" class="space-y-3 rounded-lg border p-4" :class="[!w.enabled && 'opacity-70', route.query.w === w.id ? 'border-primary lg:ring-1 lg:ring-primary' : 'border-(--ui-border)']">
            <div class="flex flex-wrap items-start gap-x-3 gap-y-1">
              <UIcon name="i-lucide-workflow" class="mt-0.5 size-5 shrink-0 text-primary" />
              <div class="min-w-0 flex-1">
                <p class="flex flex-wrap items-center gap-2">
                  <NuxtLink :to="editTo(w)" class="font-semibold hover:text-primary">{{ w.name }}</NuxtLink>
                  <span class="font-mono text-xs text-(--ui-text-muted)">/{{ w.key }}</span>
                  <UBadge v-if="w.callable" color="neutral" variant="outline" size="sm" :label="t(`wf.callable.${w.callable}` as MessageKey)" :title="t('wf.callableInfo')" />
                  <UBadge v-if="w.error" color="error" variant="subtle" size="sm" icon="i-lucide-circle-alert" :label="t('wf.invalid')" :title="w.error" />
                  <UButton v-if="w.has_update && isAdmin" size="xs" color="primary" variant="soft" icon="i-lucide-download" :label="t('wf.fromLibrary')" :loading="busy === w.id" @click="fromLibrary(w)" />
                </p>
                <p v-if="w.description" class="line-clamp-2 text-sm text-(--ui-text-muted)">{{ w.description }}</p>
                <p v-if="w.error" class="mt-1 text-xs text-(--ui-error)">{{ w.error }}</p>
              </div>
              <USwitch :model-value="w.enabled" :disabled="!isAdmin || busy === w.id" :aria-label="t('wf.enabled')" :title="t('wf.enabled')" @update:model-value="(v: boolean) => patch(w, { enabled: v })" />
              <UDropdownMenu v-if="isAdmin" :items="menu(w)" :content="{ align: 'end' }">
                <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis-vertical" :aria-label="t('chat.more')" />
              </UDropdownMenu>
            </div>

            <div v-if="w.roles.length" class="grid gap-2 sm:grid-cols-2 lg:grid-cols-1 2xl:grid-cols-2">
              <div v-for="r in w.roles" :key="r.key" class="flex items-center gap-2 text-sm">
                <UTooltip :text="t(`wf.access.${accessKey(r.access)}` as MessageKey)">
                  <UIcon :name="accessIcon[accessKey(r.access)]" class="size-4 shrink-0 text-(--ui-text-muted)" />
                </UTooltip>
                <span class="min-w-0 flex-1 truncate" :title="r.hint">{{ r.name }}</span>
                <UBadge
                  v-if="r.workflow" :color="installedKeys.has(r.workflow) ? 'primary' : 'error'" variant="soft" size="sm" icon="i-lucide-corner-down-right"
                  :label="`#${r.workflow}`" :title="installedKeys.has(r.workflow) ? t('wf.subInfo') : t('wf.subMissing', { key: r.workflow })"
                />
                <USelect
                  :model-value="w.bindings[r.key] || NONE" :items="r.workflow ? flowItems : agentItems" size="sm" class="w-44 shrink-0" :disabled="!isAdmin || busy === w.id"
                  :aria-label="t('wf.roleAgent', { role: r.name })" @update:model-value="(v: string) => bind(w, r.key, v)"
                />
              </div>
            </div>

            <p v-if="w.last_run" class="flex flex-wrap items-center gap-2 text-xs text-(--ui-text-muted)">
              <span>{{ t('wf.lastRun') }}</span>
              <UBadge :color="statusColor[w.last_run.status]" variant="subtle" size="sm" :label="t(`wf.status.${w.last_run.status}` as MessageKey)" />
              <NuxtLink :to="runLink(w.last_run)" class="hover:text-primary">{{ when(w.last_run.started_at) }}</NuxtLink>
              <span>· ${{ w.last_run.cost_usd.toFixed(3) }}</span>
              <button type="button" class="hover:text-primary" @click="runsOf(w)">· {{ t('wf.runs.all') }}</button>
            </p>
          </div>
        </template>
      </div>
      <WorkflowRuns :project-id="projectId" :workflows="workflows" class="lg:sticky lg:top-4" :class="view !== 'runs' && 'max-lg:hidden'" />
    </div>
  </div>
</template>
