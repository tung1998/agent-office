<script setup lang="ts">
// A new org model (template), written with the office assistant: the draft on
// one side, the chat on the other; its ```template blocks replace the draft
// and nothing is saved until Save. ?c= reopens the chat (and its drafts).
interface AgentDraft { key: string, name: string, tier: AgentTier, reports_to?: string[], role?: string, description?: string, model_tier: ModelTier, instructions?: string, permissions?: Record<string, unknown> }
interface TemplateDraft { key: string, name: string, kind: 'solo' | 'team' | 'council' | 'custom', description: string, governance: { mode: string, quorum?: number, veto?: string[], notes?: string }, agents: AgentDraft[] }
const toast = useToast()
const { t } = useLang()
const { isAdmin } = useAuth()
const { data: asst } = await useLiveFetch<{ project_id: string }>('/api/assistant', { key: 'assistant' })

const draft = ref<TemplateDraft>({ key: '', name: '', kind: 'team', description: '', governance: { mode: 'hierarchy' }, agents: [] })
const highlight = ref(false)
function applyDraft(p: Record<string, unknown>) {
  const d = { ...draft.value, ...p } as TemplateDraft
  if (!Array.isArray(d.agents)) d.agents = []
  d.governance = { ...(d.governance ?? {}), mode: d.governance?.mode || 'single' }
  draft.value = d
  highlight.value = true
  setTimeout(() => { highlight.value = false }, 3000)
}
const replay = (blocks: Record<string, unknown>[]) => blocks.forEach(applyDraft)
function onPatch(p: Record<string, unknown>) {
  applyDraft(p)
  toast.add({ title: t('tplNew.filled'), color: 'info' })
}

// checked as it is written: what keeps it from being saved
const problems = ref<string[]>([])
let timer: ReturnType<typeof setTimeout> | undefined
watch(draft, (d) => {
  clearTimeout(timer)
  timer = setTimeout(async () => {
    if (!d.name && !d.agents.length) { problems.value = []; return }
    try {
      problems.value = (await $fetch<{ problems: string[] }>('/api/templates/validate', { method: 'POST', body: { template: d } })).problems
    } catch { /* checked again on Save */ }
  }, 400)
}, { deep: true })

const kinds = computed(() => (['solo', 'team', 'council', 'custom'] as const).map(k => ({ label: kindLabel[k], value: k })))
const setKey = (v: string | number) => { draft.value.key = String(v).toLowerCase().replace(/[^a-z0-9-]+/g, '-').slice(0, 41) }
const removeAgent = (key: string) => { draft.value.agents = draft.value.agents.filter(a => a.key !== key) }
// the whole draft as JSON, for what the form does not show
const jsonOpen = ref(false)
const json = ref('')
watch(jsonOpen, (o) => { if (o) json.value = JSON.stringify(draft.value, null, 2) })
function applyJson() {
  try {
    applyDraft(JSON.parse(json.value))
    jsonOpen.value = false
  } catch (e) {
    toast.add({ title: t('tplNew.badJson'), description: String(e), color: 'error' })
  }
}

const pageContext = () => JSON.stringify({ page: 'template.new', draft: draft.value, problems: problems.value })

const saving = ref(false)
async function save() {
  saving.value = true
  try {
    const res = await $fetch<{ model: OrgModel }>('/api/templates', { method: 'POST', body: { template: draft.value } })
    toast.add({ title: t('tplNew.saved', { name: res.model.name }), color: 'success' })
    await navigateTo(`/templates/${res.model.id}`)
  } catch (e) {
    const d = (e as { data?: { problems?: string[] } }).data
    if (d?.problems) problems.value = d.problems
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <PageShell :title="t('tplNew.title')">
    <UButton to="/templates" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2 mb-2" :label="t('tplNew.back')" />
    <UAlert v-if="!isAdmin" color="warning" variant="subtle" :title="t('tplNew.adminOnly')" />
    <div v-else class="grid gap-4 lg:h-[calc(100vh-9rem)] lg:min-h-[36rem] lg:grid-cols-2">
      <div class="flex flex-col lg:min-h-0">
        <div class="space-y-4 rounded-lg transition lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pe-1" :class="highlight && 'ring-2 ring-primary/60 ring-offset-2 ring-offset-(--ui-bg)'">
          <div class="grid gap-3 sm:grid-cols-2">
            <UFormField :label="t('tplNew.name')" required>
              <UInput v-model="draft.name" class="w-full" :placeholder="t('tplNew.namePlaceholder')" />
            </UFormField>
            <UFormField :label="t('tplNew.key')" required>
              <UInput :model-value="draft.key" class="w-full font-mono" placeholder="review-team" @update:model-value="setKey" />
            </UFormField>
          </div>
          <UFormField :label="t('tplNew.kind')">
            <USelect v-model="draft.kind" :items="kinds" class="w-48" />
          </UFormField>
          <UFormField :label="t('tplNew.description')">
            <UTextarea v-model="draft.description" :rows="2" autoresize class="w-full" />
          </UFormField>

          <div class="space-y-2">
            <div class="flex items-center justify-between">
              <p class="text-sm font-medium">{{ t('tplNew.agents', { n: draft.agents.length }) }}</p>
              <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-braces" :label="t('tplNew.editJson')" @click="jsonOpen = true" />
            </div>
            <p v-if="!draft.agents.length" class="rounded-lg border border-dashed border-(--ui-border) p-4 text-center text-sm text-(--ui-text-muted)">{{ t('tplNew.noAgents') }}</p>
            <div v-for="a in draft.agents" :key="a.key" class="rounded-lg border border-(--ui-border) p-3 text-sm">
              <div class="flex items-center gap-2">
                <AgentAvatar :agent="{ name: a.name }" size="xs" />
                <span class="font-medium">{{ a.name }}</span>
                <span class="font-mono text-xs text-(--ui-text-muted)">{{ a.key }}</span>
                <UBadge :label="tierLabel[a.tier] ?? a.tier" size="sm" variant="subtle" />
                <UBadge :label="modelTierLabel[a.model_tier] ?? a.model_tier" size="sm" color="neutral" variant="soft" />
                <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-x" class="ms-auto" :aria-label="t('tplNew.removeAgent')" @click="removeAgent(a.key)" />
              </div>
              <p v-if="a.role || a.reports_to?.length" class="mt-1 text-xs text-(--ui-text-muted)">
                {{ a.role }}<template v-if="a.reports_to?.length"> · {{ t('tplNew.reportsTo', { to: a.reports_to.join(', ') }) }}</template>
              </p>
              <p v-if="a.instructions" class="mt-1 line-clamp-2 text-xs">{{ a.instructions }}</p>
            </div>
          </div>

          <UAlert v-if="problems.length" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('tplNew.problems')">
            <template #description>
              <ul class="list-disc ps-4 text-xs">
                <li v-for="(p, i) in problems" :key="i">{{ p }}</li>
              </ul>
            </template>
          </UAlert>
        </div>
        <div class="flex justify-end gap-2 border-t border-(--ui-border) pt-3">
          <UButton color="neutral" variant="ghost" :label="t('org.form.close')" to="/templates" />
          <UButton icon="i-lucide-save" :loading="saving" :disabled="!!problems.length || !draft.agents.length" :label="t('auto.save')" @click="save()" />
        </div>
      </div>
      <div class="h-[32rem] lg:h-auto lg:min-h-0">
        <ChatPanel v-if="asst?.project_id" :project-id="asst.project_id" purpose="template" :page-context="pageContext" @template-patch="onPatch" @history="replay" />
      </div>
    </div>

    <UModal v-model:open="jsonOpen" :title="t('tplNew.editJson')" :ui="{ content: 'max-w-3xl' }">
      <template #body>
        <UTextarea v-model="json" :rows="22" class="w-full font-mono text-xs" />
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('org.form.cancel')" @click="jsonOpen = false" />
          <UButton :label="t('tplNew.applyJson')" @click="applyJson" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
