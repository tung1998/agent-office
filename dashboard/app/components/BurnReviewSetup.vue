<script setup lang="ts">
// The Burn's review, set up inside its settings (ADR-113): pick the profile it
// follows, and edit that profile right there (each stage with its reviewer
// agent or workflow). v-model is the profile the Burn uses ('' = no review).
interface AgentLite { id: string, name: string, enabled?: boolean }
const props = defineProps<{ projectId: string }>()
const model = defineModel<string>({ default: '' })
const { t } = useLang()
const toast = useToast()

const base = computed(() => `/api/projects/${props.projectId}/burn/review-profiles`)
const { data, error, refresh } = useLiveFetch<{ profiles: BurnReviewProfile[] }>(base, { lazy: true })
const { data: agentsData } = useLiveFetch<{ agents: AgentLite[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const { data: wfData } = useLiveFetch<{ workflows: ProjectWorkflow[] }>(() => `/api/projects/${props.projectId}/workflows`, { lazy: true })
const profiles = computed(() => data.value?.profiles ?? [])

// one pick per stage: an agent reviews itself, or a workflow reviews ('a:<id>' / 'w:<key>'; 'a:' is the Burn's own agent)
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
const stageIcons: Record<ReviewStage, string> = { issue: 'i-lucide-circle-help', plan: 'i-lucide-list-checks', result: 'i-lucide-shield-check' }

// the pick: no review, a profile, or a new one being written
const NONE = '__none', NEW = '__new'
const creating = ref(false)
const pick = computed({
  get: () => creating.value ? NEW : (model.value || NONE),
  set: (v: string) => {
    creating.value = v === NEW
    if (v !== NEW) model.value = v === NONE ? '' : v
  }
})
const pickItems = computed(() => [
  { value: NONE, label: t('burn.review.none') },
  ...profiles.value.map(p => ({ value: p.id, label: p.name })),
  { value: NEW, label: t('burn.review.new'), icon: 'i-lucide-plus' }
])
const editing = computed(() => creating.value ? null : profiles.value.find(p => p.id === model.value) ?? null)
const showEditor = computed(() => creating.value || !!editing.value)

// the profile being edited, as a form
interface StageForm { on: boolean, who: string, agent: string }
const blank = (): StageForm => ({ on: false, who: 'a:', agent: '' })
const form = reactive({ name: '', stages: { issue: blank(), plan: blank(), result: blank() } as Record<ReviewStage, StageForm> })
const { errors, check, reset: resetCheck } = useFormCheck(() => ({ name: !form.name.trim() && t('common.required') }))
function load(p: BurnReviewProfile | null) {
  form.name = p?.name ?? ''
  resetCheck()
  for (const k of reviewStageKeys) {
    const st = p?.stages[k]
    form.stages[k] = { on: !!st, who: st?.workflow ? `w:${st.workflow}` : `a:${st?.agent_id ?? ''}`, agent: st?.agent_id ?? '' }
  }
}
watch(() => [editing.value?.id, creating.value], () => load(editing.value), { immediate: true })

const saving = ref(false)
async function save() {
  if (saving.value || !check()) return
  saving.value = true
  const stages: Partial<Record<ReviewStage, BurnReviewStage>> = {}
  for (const k of reviewStageKeys) {
    const st = form.stages[k]
    if (!st.on) continue
    const wf = st.who.startsWith('w:')
    stages[k] = { agent_id: wf ? st.agent : st.who.slice(2), workflow: wf ? st.who.slice(2) : '' }
  }
  try {
    const body = { name: form.name, stages }
    const res = editing.value
      ? await $fetch<{ profile: BurnReviewProfile }>(`/api/burn-review-profiles/${editing.value.id}`, { method: 'PUT', body })
      : await $fetch<{ profile: BurnReviewProfile }>(base.value, { method: 'POST', body })
    await refresh()
    creating.value = false
    model.value = res.profile.id
    toast.add({ title: t('burn.review.profileSaved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
async function remove() {
  const p = editing.value
  if (!p || !confirm(t('burn.review.deleteConfirm', { name: p.name }))) return
  try {
    await $fetch(`/api/burn-review-profiles/${p.id}`, { method: 'DELETE' })
    model.value = ''
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <div class="space-y-3">
    <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    <USkeleton v-else-if="!data" class="h-8 w-full" />
    <template v-else>
      <UFormField :label="t('burn.review.label')" :help="t('burn.review.help')">
        <USelect v-model="pick" :items="pickItems" class="w-full" />
      </UFormField>

      <div v-if="showEditor" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
        <UFormField :label="t('burn.review.name')" required data-field="name" :error="errors.name">
          <UInput v-model="form.name" class="w-full" :placeholder="t('burn.review.namePlaceholder')" />
        </UFormField>
        <div v-for="k in reviewStageKeys" :key="k" class="space-y-2">
          <div class="flex items-start gap-2.5">
            <UIcon :name="stageIcons[k]" class="mt-0.5 size-4 shrink-0" :class="form.stages[k].on ? 'text-(--ui-primary)' : 'text-(--ui-text-dimmed)'" />
            <USwitch v-model="form.stages[k].on" class="flex-1" :label="t('burn.review.stageTitle', { stage: t(`burn.review.${k}`) })" :description="t(`burn.review.${k}Help`)" />
          </div>
          <div v-if="form.stages[k].on" class="flex items-center gap-2 ps-[1.625rem]">
            <USelect v-model="form.stages[k].who" :items="reviewerItems" :icon="reviewerIcon(form.stages[k].who)" class="min-w-0 flex-1" :aria-label="t('burn.review.reviewer')" />
            <InfoTip :text="t('burn.review.reviewerHelp')" icon-class="size-3.5 text-(--ui-text-dimmed)" />
          </div>
        </div>
        <div class="flex items-center justify-end gap-2 border-t border-(--ui-border)/60 pt-3">
          <UBadge v-if="editing?.in_use" :label="t('burn.review.inUse')" color="warning" variant="subtle" size="sm" class="me-auto" />
          <UButton v-if="creating" size="sm" color="neutral" variant="ghost" :label="t('common.cancel')" @click="creating = false" />
          <UButton v-if="editing" size="sm" color="error" variant="ghost" icon="i-lucide-trash-2" :label="t('common.delete')" @click="remove" />
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-save" :label="t('burn.review.saveProfile')" :loading="saving" @click="save" />
        </div>
      </div>
    </template>
  </div>
</template>
