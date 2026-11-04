<script setup lang="ts">
// A project's Burn review profiles (ADR-113): saved review setups, each stage
// (problem, plan, result) with its own reviewer agent and workflow. The
// Burn's settings pick one.
interface AgentLite { id: string, name: string, enabled?: boolean }

const route = useRoute()
const { t } = useLang()
const toast = useToast()
const projectId = computed(() => String(route.params.id))
const base = computed(() => `/api/projects/${projectId.value}/burn/review-profiles`)
const { data, error, refresh } = useLiveFetch<{ profiles: BurnReviewProfile[] }>(base)
const { data: agentsData } = useLiveFetch<{ agents: AgentLite[] }>(() => `/api/projects/${projectId.value}/chat/agents`, { lazy: true })
const { data: wfData } = useLiveFetch<{ workflows: ProjectWorkflow[] }>(() => `/api/projects/${projectId.value}/workflows`, { lazy: true })
const profiles = computed(() => data.value?.profiles ?? [])
useHead({ title: () => t('burn.review.profiles') })

// one pick per stage: an agent reviews itself, or a workflow reviews
// ('a:<id>' / 'w:<key>'; 'a:' is the Burn's own agent)
const reviewerItems = computed(() => {
  const agents = [{ value: 'a:', label: t('burn.review.agentSame'), icon: 'i-lucide-bot' },
    ...(agentsData.value?.agents ?? []).map(a => ({ value: `a:${a.id}`, label: a.enabled === false ? `${a.name} (${t('burn.agentOffTag')})` : a.name, icon: 'i-lucide-bot' }))]
  const wfs = (wfData.value?.workflows ?? []).filter(w => w.enabled && !w.error && w.callable !== 'sub')
    .map(w => ({ value: `w:${w.key}`, label: `#${w.key} · ${w.name}`, icon: 'i-lucide-workflow' }))
  const groups: { type?: 'label', value?: string, label: string, icon?: string }[][] = [[{ type: 'label', label: t('burn.review.groupAgents') }, ...agents]]
  if (wfs.length) groups.push([{ type: 'label', label: t('burn.review.groupWorkflows') }, ...wfs])
  return groups
})
const reviewerIcon = (v: string) => v.startsWith('w:') ? 'i-lucide-workflow' : 'i-lucide-bot'

// the profile being edited ('' = a new one)
const selected = ref<string | null>(null)
// agent is kept for a workflow pick: the agent that runs it, as loaded
interface StageForm { on: boolean, who: string, agent: string }
const blank = (): StageForm => ({ on: false, who: 'a:', agent: '' })
const form = reactive({ name: '', stages: { issue: blank(), plan: blank(), result: blank() } as Record<ReviewStage, StageForm> })
const { errors, check, reset: resetCheck } = useFormCheck(() => ({ name: !form.name.trim() && t('common.required') }))
let loadGen = 0
function load(p?: BurnReviewProfile) {
  loadGen++
  selected.value = p?.id ?? ''
  form.name = p?.name ?? ''
  resetCheck()
  for (const k of reviewStageKeys) {
    const st = p?.stages[k]
    form.stages[k] = { on: !!st, who: st?.workflow ? `w:${st.workflow}` : `a:${st?.agent_id ?? ''}`, agent: st?.agent_id ?? '' }
  }
}
watch(data, (d) => { if (d && selected.value === null) load(d.profiles[0]) }, { immediate: true }) // the first one, once loaded

const saving = ref(false)
async function save() {
  if (saving.value || !check()) return
  saving.value = true
  const gen = loadGen
  const stages: Partial<Record<ReviewStage, BurnReviewStage>> = {}
  for (const k of reviewStageKeys) {
    const st = form.stages[k]
    if (!st.on) continue
    const wf = st.who.startsWith('w:')
    stages[k] = { agent_id: wf ? st.agent : st.who.slice(2), workflow: wf ? st.who.slice(2) : '' }
  }
  try {
    const body = { name: form.name, stages }
    const res = selected.value
      ? await $fetch<{ profile: BurnReviewProfile }>(`/api/burn-review-profiles/${selected.value}`, { method: 'PUT', body })
      : await $fetch<{ profile: BurnReviewProfile }>(base.value, { method: 'POST', body })
    await refresh()
    if (gen === loadGen) load(res.profile)
    toast.add({ title: t('burn.saved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
async function remove() {
  const p = profiles.value.find(x => x.id === selected.value)
  if (!p || !confirm(t('burn.review.deleteConfirm', { name: p.name }))) return
  try {
    await $fetch(`/api/burn-review-profiles/${p.id}`, { method: 'DELETE' })
    selected.value = null
    await refresh()
    if (!profiles.value.length) load()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <PageShell :title="t('burn.review.profiles')">
    <div>
      <UButton :to="{ path: `/projects/${projectId}`, query: { tab: 'burn' } }" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2 mb-2" :label="t('burn.review.back')" />
    </div>
    <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    <LoadingRows v-else-if="!data" />
    <div v-else class="grid gap-4 md:grid-cols-[16rem_1fr]">
      <!-- the profiles -->
      <div class="space-y-1">
        <UButton block color="neutral" variant="outline" icon="i-lucide-plus" :label="t('burn.review.new')" @click="load()" />
        <button
          v-for="p in profiles" :key="p.id" type="button"
          class="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-start text-sm hover:bg-(--ui-bg-elevated)"
          :class="selected === p.id ? 'bg-(--ui-bg-elevated) font-medium' : ''"
          @click="load(p)"
        >
          <UIcon name="i-lucide-scan-eye" class="size-4 shrink-0 text-(--ui-text-muted)" />
          <span class="min-w-0 flex-1 truncate">{{ p.name }}</span>
          <UBadge v-if="p.in_use" :label="t('burn.review.inUse')" color="warning" variant="subtle" size="sm" />
        </button>
        <p v-if="!profiles.length" class="px-3 py-2 text-xs text-(--ui-text-dimmed)">{{ t('burn.review.empty') }}</p>
      </div>

      <!-- one profile -->
      <UCard :ui="{ body: 'space-y-4 sm:p-4' }">
        <UFormField :label="t('burn.review.name')" required data-field="name" :error="errors.name">
          <UInput v-model="form.name" class="w-full" :placeholder="t('burn.review.namePlaceholder')" />
        </UFormField>
        <div v-for="k in reviewStageKeys" :key="k" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
          <USwitch v-model="form.stages[k].on" :label="t('burn.review.stageTitle', { stage: t(`burn.review.${k}`) })" :description="t(`burn.review.${k}Help`)" />
          <div v-if="form.stages[k].on" class="space-y-1.5">
            <p class="flex items-center gap-1.5 text-sm font-medium">
              {{ t('burn.review.reviewer') }}
              <InfoTip :text="t('burn.review.reviewerHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
            </p>
            <USelect v-model="form.stages[k].who" :items="reviewerItems" :icon="reviewerIcon(form.stages[k].who)" class="w-full sm:max-w-md" :aria-label="t('burn.review.reviewer')" />
          </div>
        </div>
        <div class="flex justify-end gap-2">
          <UButton v-if="selected" color="error" variant="ghost" icon="i-lucide-trash-2" :label="t('common.delete')" @click="remove" />
          <UButton :label="t('common.save')" :loading="saving" @click="save" />
        </div>
      </UCard>
    </div>
  </PageShell>
</template>
