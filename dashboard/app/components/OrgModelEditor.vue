<script setup lang="ts">
// agentsFirst (a project's Agents page): the agents are the page, the model a line
const props = defineProps<{ modelId: string, agentsFirst?: boolean }>()
const emit = defineEmits<{ changed: [] }>()

const toast = useToast()
const saveError = useSaveError()
const { isAdmin } = useAuth()
const { t } = useLang()

const _f1 = useLiveFetch<{ model: OrgModel }>(() => `/api/org-models/${props.modelId}`)
const { data, refresh } = _f1
const _f2 = useLiveFetch<{ providers: Provider[] }>('/api/providers')
const { data: provData } = _f2
await Promise.all([_f1, _f2]) // started together: one round trip, not 2 (a phone over a VPN)
const model = computed(() => data.value?.model)
const agents = computed(() => model.value?.agents ?? [])
const providers = computed(() => provData.value?.providers ?? [])
const defaultProvider = computed(() => providers.value.find(p => p.is_default))

const rows = computed(() => (['lead', 'manager', 'worker'] as const)
  .map(t => ({ tier: t, agents: agents.value.filter(a => a.tier === t) }))
  .filter(r => r.agents.length))

function providerOf(a: Agent) {
  return providers.value.find(p => p.id === a.provider_id) ?? defaultProvider.value
}
function resolvedModel(a: Agent) {
  return a.llm_model || providerOf(a)?.tier_models[a.model_tier] || '—'
}
function nameOf(key: string) {
  return agents.value.find(a => a.key === key)?.name ?? key
}

const historyOpen = ref(false)
const problems = ref<string[]>([])
function showError(e: unknown) {
  const d = (e as { data?: { error?: string, problems?: string[] } })?.data
  problems.value = d?.problems ?? []
  if (!d?.problems) saveError(e, async () => { await refresh() })
}

// ---- model settings ----
const settingsOpen = ref(false)
const settings = reactive({ name: '', description: '', kind: 'custom', mode: 'hierarchy', quorum: 2, veto: [] as string[], notes: '' })
let settingsFrom = '' // the model as it was opened: a save over someone else's change is refused (409)
function openSettings() {
  const m = model.value!
  settingsFrom = (m as { version?: string }).version ?? ''
  Object.assign(settings, {
    name: m.name, description: m.description, kind: m.kind, mode: m.governance.mode || 'hierarchy',
    quorum: m.governance.quorum || 2, veto: [...(m.governance.veto ?? [])], notes: m.governance.notes ?? ''
  })
  problems.value = []
  settingsOpen.value = true
}
async function saveSettings() {
  try {
    await $fetch(`/api/org-models/${props.modelId}`, {
      method: 'PATCH',
      body: {
        name: settings.name, description: settings.description, kind: settings.kind,
        governance: {
          mode: settings.mode, notes: settings.notes, veto: settings.veto,
          quorum: settings.mode === 'council' ? Number(settings.quorum) : 0
        },
        version: settingsFrom || undefined
      }
    })
    settingsOpen.value = false
    await refresh()
    emit('changed')
    toast.add({ title: t('org.editor.savedModel'), color: 'success' })
  } catch (e) {
    showError(e)
  }
}
const leads = computed(() => agents.value.filter(a => a.tier === 'lead'))

// ---- agent editor ----
const editorOpen = ref(false)
const editing = ref<Agent | null>(null)
const empty = (): Omit<Agent, 'id' | 'org_model_id' | 'sort'> => ({
  key: '', name: '', tier: 'worker', role: '', description: '', reports_to: [], provider_id: '',
  model_tier: 'fast', llm_model: '', instructions: '', permissions: { level: 'read', read_only: true, requires_approval: false }
})
const form = reactive(empty())
const saving = ref(false)

function openAgent(a: Agent) {
  // a project's agent has its own page; library templates keep the side panel
  if (model.value?.repo_id) return navigateTo(`/projects/${model.value.repo_id}/agents/${a.id}`)
  editing.value = a
  const { id: _id, org_model_id: _org, sort: _sort, ...rest } = JSON.parse(JSON.stringify(a)) as Agent
  Object.assign(form, empty(), rest)
  problems.value = []
  editorOpen.value = true
}
function newAgent(tier: AgentTier) {
  editing.value = null
  Object.assign(form, empty(), { tier, model_tier: tier === 'lead' ? 'strong' : tier === 'manager' ? 'balanced' : 'fast' })
  if (tier !== 'lead' && leads.value[0]) form.reports_to = [leads.value[0].key]
  problems.value = []
  editorOpen.value = true
}

const bossOptions = computed(() => agents.value
  .filter(a => a.tier !== 'worker' && a.key !== form.key)
  .map(a => ({ label: `${a.name} (${a.key})`, value: a.key })))

// Select items cannot use an empty value, so "default provider" is a sentinel.
const DEFAULT_PROVIDER = '__default'
const providerChoice = computed({
  get: () => form.provider_id || DEFAULT_PROVIDER,
  set: (v: string) => { form.provider_id = v === DEFAULT_PROVIDER ? '' : v }
})
const providerOptions = computed(() => [
  { label: t('org.form.providerDefault', { suffix: defaultProvider.value ? ` (${defaultProvider.value.name})` : '' }), value: DEFAULT_PROVIDER },
  ...providers.value.map(p => ({ label: p.name, value: p.id }))
])
const formProvider = computed(() => providers.value.find(p => p.id === form.provider_id) ?? defaultProvider.value)

async function saveAgent() {
  saving.value = true
  problems.value = []
  // Send only editable fields: the API rejects unknown ones such as id.
  const body = {
    key: form.key, name: form.name, tier: form.tier, role: form.role, description: form.description,
    reports_to: form.tier === 'lead' ? [] : form.reports_to, provider_id: form.provider_id,
    model_tier: form.model_tier, llm_model: form.llm_model, instructions: form.instructions, permissions: form.permissions
  }
  try {
    if (editing.value) {
      await $fetch(`/api/agents/${editing.value.id}`, { method: 'PATCH', body: { ...body, version: (editing.value as { version?: string }).version } })
    } else {
      await $fetch(`/api/org-models/${props.modelId}/agents`, { method: 'POST', body })
    }
    editorOpen.value = false
    await refresh()
    emit('changed')
    toast.add({ title: t('org.editor.savedAgent', { name: form.name }), color: 'success' })
  } catch (e) {
    showError(e)
  } finally {
    saving.value = false
  }
}

async function deleteAgent() {
  if (!editing.value || !confirm(t('org.editor.deleteAgentConfirm', { name: editing.value.name }))) return
  try {
    await $fetch(`/api/agents/${editing.value.id}`, { method: 'DELETE' })
    editorOpen.value = false
    await refresh()
    emit('changed')
  } catch (e) {
    showError(e)
  }
}

const tierColor: Record<AgentTier, 'primary' | 'info' | 'neutral'> = { lead: 'primary', manager: 'info', worker: 'neutral' }
</script>

<template>
  <div v-if="model" class="space-y-6">
    <!-- summary -->
    <UCard>
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div v-if="agentsFirst" class="min-w-0 space-y-0.5">
          <h2 class="text-lg font-semibold">{{ t('org.editor.agentsCount', { n: agents.length }) }}</h2>
          <p class="flex flex-wrap items-center gap-x-1.5 text-xs text-(--ui-text-muted)" :title="model.description">
            <UIcon :name="kindIcon[model.kind]" class="size-3.5" />
            {{ t('org.editor.modelLine', { name: model.name, kind: kindLabel[model.kind] ?? model.kind }) }}
            · {{ t('org.editor.decision') }} {{ governanceLabel[model.governance.mode] ?? model.governance.mode }}
          </p>
        </div>
        <div v-else class="min-w-0 space-y-1">
          <div class="flex items-center gap-2">
            <UIcon :name="kindIcon[model.kind]" class="size-5 text-primary" />
            <h2 class="text-lg font-semibold">{{ model.name }}</h2>
            <UBadge :label="kindLabel[model.kind]" variant="subtle" />
            <UBadge v-if="model.builtin" :label="t('org.editor.builtin')" color="neutral" variant="outline" size="sm" />
          </div>
          <p class="max-w-3xl text-sm text-(--ui-text-muted)">{{ model.description }}</p>
          <p class="text-sm">
            <span class="text-(--ui-text-muted)">{{ t('org.editor.decision') }}</span>
            {{ governanceLabel[model.governance.mode] ?? model.governance.mode }}
            <template v-if="model.governance.mode === 'council'"> {{ t('org.editor.quorum', { n: model.governance.quorum ?? 0, total: leads.length }) }}</template>
            <template v-if="model.governance.veto?.length"> {{ t('org.editor.veto', { names: model.governance.veto.map(nameOf).join(', ') }) }}</template>
          </p>
          <p v-if="model.governance.notes" class="text-xs text-(--ui-text-muted)">{{ model.governance.notes }}</p>
        </div>
        <!-- one row of actions, same size and style -->
        <div class="flex flex-wrap items-center gap-2 sm:shrink-0">
          <UButton icon="i-lucide-history" :label="t('org.editor.history')" color="neutral" variant="outline" @click="historyOpen = true" />
          <template v-if="isAdmin">
          <UButton icon="i-lucide-settings-2" :label="t('org.editor.settings')" color="neutral" variant="outline" @click="openSettings" />
          <UDropdownMenu :items="[[
            { label: t('org.editor.addLead'), icon: 'i-lucide-crown', onSelect: () => newAgent('lead') },
            { label: t('org.editor.addManager'), icon: 'i-lucide-briefcase', onSelect: () => newAgent('manager') },
            { label: t('org.editor.addWorker'), icon: 'i-lucide-wrench', onSelect: () => newAgent('worker') }
          ]]">
            <UButton icon="i-lucide-user-plus" :label="t('org.editor.addAgent')" />
          </UDropdownMenu>
          </template>
        </div>
      </div>
    </UCard>

    <UAlert
      v-if="problems.length && !editorOpen && !settingsOpen" color="error" variant="subtle" :title="t('org.editor.invalidModel')"
      :description="problems.join(' · ')"
    />

    <!-- org chart -->
    <div class="space-y-3">
      <template v-for="(row, i) in rows" :key="row.tier">
        <div class="flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-(--ui-text-muted)">
          <span>{{ tierLabel[row.tier] }}</span>
          <span class="h-px flex-1 bg-(--ui-border)" />
        </div>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <!-- a div, not a button: the on/off switch sits inside -->
          <div
            v-for="a in row.agents" :key="a.id" role="button" tabindex="0"
            class="cursor-pointer rounded-lg border border-(--ui-border) bg-(--ui-bg) p-3 text-left transition hover:border-(--ui-primary) hover:shadow-sm"
            @click="openAgent(a)" @keydown.enter.self="openAgent(a)"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="flex min-w-0 items-center gap-2.5" :class="a.enabled === false ? 'opacity-50' : ''">
                <AgentAvatar :agent="a" size="md" />
                <div class="min-w-0">
                  <p class="truncate font-medium">{{ a.name }}</p>
                  <p class="truncate font-mono text-xs text-(--ui-text-muted)">{{ a.key }}</p>
                </div>
              </div>
              <div class="flex shrink-0 items-center gap-1.5">
                <UBadge v-if="a.enabled === false" :label="t('org.editor.paused')" color="warning" variant="subtle" size="sm" icon="i-lucide-pause" />
                <UBadge v-else :label="modelTierLabel[a.model_tier]" :color="tierColor[a.tier]" variant="subtle" size="sm" />
                <AgentSwitch v-if="model.repo_id" :agent="a" @changed="refresh(); emit('changed')" />
              </div>
            </div>
            <p v-if="a.role" class="mt-2 line-clamp-2 text-sm text-(--ui-text-toned)">{{ a.role }}</p>
            <div class="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-(--ui-text-muted)" :class="a.enabled === false ? 'opacity-50' : ''">
              <span class="font-mono">{{ resolvedModel(a) }}</span>
              <span class="inline-flex items-center gap-1" :class="permRank(agentLevel(a.permissions)) >= 2 ? 'text-(--ui-warning)' : ''" :title="permOf(agentLevel(a.permissions)).description">
                <UIcon :name="a.permissions.caps ? 'i-lucide-sliders-horizontal' : permOf(agentLevel(a.permissions)).icon" class="size-3.5" />{{ a.permissions.caps ? t('org.editor.customCaps', { n: a.permissions.caps.length }) : permOf(agentLevel(a.permissions)).label }}
              </span>
              <UIcon v-if="a.permissions.requires_approval" name="i-lucide-shield-check" class="size-3.5" :title="t('org.editor.requiresApproval')" />
            </div>
            <div v-if="a.reports_to.length" class="mt-2 flex flex-wrap gap-1">
              <UBadge v-for="r in a.reports_to" :key="r" :label="t('org.editor.reportsTo', { name: nameOf(r) })" color="neutral" variant="outline" size="sm" />
            </div>
          </div>
        </div>
        <div v-if="i < rows.length - 1" class="flex justify-center">
          <UIcon name="i-lucide-chevrons-down" class="size-4 text-(--ui-text-dimmed)" />
        </div>
      </template>
    </div>

    <RevisionHistory v-model:open="historyOpen" :model-id="modelId" @restored="refresh(); emit('changed')" />

    <!-- model settings -->
    <UModal v-model:open="settingsOpen" :title="t('org.editor.settings')" :ui="{ content: 'max-w-lg' }">
      <template #body>
        <form id="model-settings" class="space-y-4" @submit.prevent="saveSettings">
          <UFormField :label="t('org.settings.name')" required>
            <UInput v-model="settings.name" class="w-full" />
          </UFormField>
          <UFormField :label="t('org.settings.description')">
            <UTextarea v-model="settings.description" :rows="3" class="w-full" />
          </UFormField>
          <UFormField :label="t('org.settings.kind')">
            <USelect v-model="settings.kind" :items="Object.entries(kindLabel).map(([value, label]) => ({ label, value }))" class="w-full" />
          </UFormField>
          <UFormField :label="t('org.settings.decisionMode')">
            <USelect v-model="settings.mode" :items="Object.entries(governanceLabel).map(([value, label]) => ({ label, value }))" class="w-full" />
          </UFormField>
          <UFormField v-if="settings.mode === 'council'" :label="t('org.settings.quorumNeeded', { n: leads.length })">
            <UInputNumber v-model="settings.quorum" :min="1" :max="leads.length" />
          </UFormField>
          <UFormField :label="t('org.settings.veto')" :help="t('org.settings.vetoHelp')">
            <USelectMenu v-model="settings.veto" multiple value-key="value" :items="leads.map(a => ({ label: a.name, value: a.key }))" class="w-full" />
          </UFormField>
          <UFormField :label="t('org.settings.notes')">
            <UTextarea v-model="settings.notes" :rows="2" class="w-full" />
          </UFormField>
          <UAlert v-if="problems.length" color="error" variant="subtle" :title="t('org.form.invalid')">
            <template #description>
              <ul class="list-disc ps-4">
                <li v-for="p in problems" :key="p">{{ p }}</li>
              </ul>
            </template>
          </UAlert>
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('org.form.cancel')" @click="settingsOpen = false" />
          <UButton type="submit" form="model-settings" :label="t('org.form.save')" />
        </div>
      </template>
    </UModal>

    <!-- agent editor -->
    <USlideover v-model:open="editorOpen" :title="editing ? editing.name : t('org.editor.newAgent')" :ui="{ content: 'max-w-xl' }">
      <template #body>
        <form id="agent-form" class="space-y-4" @submit.prevent="saveAgent">
          <UAlert v-if="problems.length" color="error" variant="subtle" :title="t('org.form.invalid')">
            <template #description>
              <ul class="list-disc ps-4">
                <li v-for="p in problems" :key="p">{{ p }}</li>
              </ul>
            </template>
          </UAlert>
          <fieldset :disabled="!isAdmin" class="space-y-4">
            <div class="grid gap-3 sm:grid-cols-2">
              <UFormField :label="t('org.form.name')" required>
                <UInput v-model="form.name" class="w-full" />
              </UFormField>
              <UFormField :label="t('org.form.key')" required :help="t('org.form.keyHelp')">
                <UInput v-model="form.key" class="w-full font-mono" />
              </UFormField>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <UFormField :label="t('org.form.tier')">
                <USelect v-model="form.tier" :items="Object.entries(tierLabel).map(([value, label]) => ({ label, value }))" class="w-full" />
              </UFormField>
              <UFormField v-if="form.tier !== 'lead'" :label="t('org.form.reportsTo')">
                <USelectMenu v-model="form.reports_to" multiple value-key="value" :items="bossOptions" class="w-full" />
              </UFormField>
            </div>
            <UFormField :label="t('org.form.role')">
              <UInput v-model="form.role" class="w-full" :placeholder="t('org.form.rolePlaceholder')" />
            </UFormField>
            <UFormField :label="t('org.settings.description')">
              <UTextarea v-model="form.description" :rows="2" class="w-full" />
            </UFormField>

            <USeparator :label="t('org.form.modelSection')" />
            <div class="grid gap-3 sm:grid-cols-2">
              <UFormField :label="t('org.form.provider')">
                <USelect v-model="providerChoice" :items="providerOptions" class="w-full" />
              </UFormField>
              <UFormField :label="t('org.form.modelTier')">
                <USelect v-model="form.model_tier" :items="Object.entries(modelTierLabel).map(([value, label]) => ({ label, value }))" class="w-full" />
              </UFormField>
            </div>
            <UFormField
              :label="t('org.form.specificModel')"
              :help="t('org.form.specificModelHelp', { model: formProvider?.tier_models[form.model_tier] || t('org.form.specificModelFallback') })"
            >
              <UInput v-model="form.llm_model" list="agent-models" class="w-full font-mono" />
              <datalist id="agent-models">
                <option v-for="m in formProvider?.models ?? []" :key="m" :value="m" />
              </datalist>
            </UFormField>

            <USeparator :label="t('org.form.instructionsSection')" />
            <UFormField :label="t('org.form.instructions')">
              <UTextarea v-model="form.instructions" :rows="6" class="w-full" autoresize />
            </UFormField>
            <UFormField :label="t('org.form.permissions')" :help="t('org.form.permissionsHelp')">
              <AgentPermEditor v-model="form.permissions" :project-id="model?.repo_id || undefined" />
            </UFormField>
          </fieldset>

        </form>
      </template>
      <template #footer>
        <div class="flex w-full items-center justify-between gap-2">
          <UButton v-if="isAdmin && editing" color="error" variant="ghost" icon="i-lucide-trash" :label="t('org.form.delete')" @click="deleteAgent" />
          <span v-else />
          <div class="flex gap-2">
            <UButton color="neutral" variant="ghost" :label="t('org.form.close')" @click="editorOpen = false" />
            <UButton v-if="isAdmin" type="submit" form="agent-form" :loading="saving" :label="t('org.form.save')" />
          </div>
        </div>
      </template>
    </USlideover>
  </div>
</template>
