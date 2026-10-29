<script setup lang="ts">
// A project's agents (ADR-099): who answers when nobody is named (the
// default), each one's switch, more picked from the library (ADR-166), and
// the history of changes. How they work together is a workflow, not set here.
const props = defineProps<{ project: Project }>()
const emit = defineEmits<{ changed: [] }>()

const toast = useToast()
const { isAdmin } = useAuth()
const { t, lang } = useLang()

const { data: provData } = useLiveFetch<{ providers: Provider[] }>('/api/providers', { lazy: true })
const providers = computed(() => provData.value?.providers ?? [])
const defaultProvider = computed(() => providers.value.find(p => p.is_default))
const agents = computed(() => props.project.agents ?? [])
const base = computed(() => `/projects/${props.project.id}`)

function resolvedModel(a: Agent) {
  const p = providers.value.find(x => x.id === a.provider_id) ?? defaultProvider.value
  return a.llm_model || p?.tier_models[a.model_tier] || modelTierLabel[a.model_tier]
}
function changed() {
  emit('changed')
}
const openAgent = (a: Agent) => navigateTo(`${base.value}/agents/${a.id}`)

// ---- default / delete ----
async function makeDefault(a: Agent) {
  try {
    await $fetch(`/api/projects/${props.project.id}/default-agent`, { method: 'PUT', body: { agent_id: a.id } })
    toast.add({ title: t('team.defaultSet', { name: a.name }), color: 'success' })
    changed()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function remove(a: Agent) {
  if (!confirm(t('org.editor.deleteAgentConfirm', { name: a.name }))) return
  try {
    await $fetch(`/api/agents/${a.id}`, { method: 'DELETE' })
    changed()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const menu = (a: Agent) => [[
  { label: t('team.open'), icon: 'i-lucide-square-arrow-out-up-right', to: `${base.value}/agents/${a.id}` },
  ...(isAdmin.value && a.id !== props.project.default_agent_id ? [{ label: t('team.makeDefault'), icon: 'i-lucide-star', onSelect: () => makeDefault(a) }] : [])
], ...(isAdmin.value ? [[{ label: t('org.form.delete'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => remove(a) }]] : [])]

function exportAgents() {
  window.open(`/api/projects/${props.project.id}/agents/export`, '_blank')
}
const historyOpen = ref(false)

// ---- add an agent from the library: pick, adjust the basics here, the rest on its own page ----
const addOpen = ref(false)
const addTab = ref<'library' | 'edit'>('library')
const form = reactive({ name: '', key: '', role: '', model_tier: 'balanced' as ModelTier })
// picked from the library (ADR-153): its description, instructions and permissions go along
const fromLib = ref<LibraryAgent>()
const libPick = ref<LibraryAgent>()
const keyTouched = ref(false)
const problems = ref<string[]>([])
const adding = ref(false)
function openAdd() {
  Object.assign(form, { name: '', key: '', role: '', model_tier: 'balanced' })
  keyTouched.value = false
  fromLib.value = undefined
  libPick.value = undefined
  problems.value = []
  addTab.value = 'library'
  addOpen.value = true
}
// a free key: the library's own, or with -2, -3… when the project has it
function freeKey(k: string) {
  const used = new Set(agents.value.map(a => a.key))
  let n = 1
  let out = k
  while (used.has(out)) out = `${k}-${++n}`
  return out
}
function pickLib(a: LibraryAgent) {
  fromLib.value = { ...a, description: (lang.value === 'vi' && a.description_vi) || a.description }
  Object.assign(form, { name: a.name, role: a.role, model_tier: a.model_tier })
  form.key = freeKey(a.key)
  keyTouched.value = true
  problems.value = []
  addTab.value = 'edit'
}
// the key follows the name until typed by hand (lowercase, digits, dashes)
const slug = (s: string) => s.normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/\u0111/gi, 'd')
  .toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
watch(() => form.name, (n) => { if (!keyTouched.value) form.key = slug(n) })
async function add() {
  if (adding.value || !fromLib.value) return
  adding.value = true
  problems.value = []
  try {
    const res = await $fetch<{ agent: Agent }>(`/api/projects/${props.project.id}/agents`, {
      method: 'POST',
      body: {
        key: form.key, name: form.name, role: form.role, model_tier: form.model_tier,
        description: fromLib.value.description, instructions: fromLib.value.instructions,
        permissions: fromLib.value.permissions, skills: fromLib.value.skills ?? []
      }
    })
    addOpen.value = false
    toast.add({ title: t('org.editor.savedAgent', { name: res.agent.name }), color: 'success' })
    changed()
    await navigateTo({ path: `${base.value}/agents/${res.agent.id}`, query: { tab: 'config' } })
  } catch (e) {
    const d = (e as { data?: { problems?: string[] } })?.data
    problems.value = d?.problems?.length ? d.problems : [apiError(e)]
  } finally {
    adding.value = false
  }
}

// from the agent library page (?add=<set>/<key>): the form opens filled
const route = useRoute()
onMounted(async () => {
  const id = route.query.add
  if (typeof id !== 'string' || !isAdmin.value) return
  navigateTo({ query: { ...route.query, add: undefined } }, { replace: true })
  try {
    const res = await $fetch<{ agent: LibraryAgent }>(`/api/agent-library/${id}`)
    openAdd()
    pickLib(res.agent)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
})

</script>

<template>
  <!-- no agent yet: pick from the library (or let AI pick) -->
  <UCard v-if="!agents.length" class="max-w-3xl" :ui="{ body: 'space-y-4 sm:p-5' }">
    <div class="flex items-start gap-3">
      <UIcon name="i-lucide-bot" class="mt-0.5 size-6 shrink-0 text-(--ui-text-dimmed)" />
      <div class="min-w-0">
        <p class="font-medium">{{ t('team.empty.title') }}</p>
        <p class="text-sm text-(--ui-text-muted)">{{ t('team.empty.desc') }}</p>
      </div>
    </div>
    <div v-if="isAdmin" class="flex flex-wrap justify-end gap-2">
      <UButton :to="`${base}/setup`" icon="i-lucide-sparkles" color="neutral" variant="outline" :label="t('project.setupAi')" />
      <UButton icon="i-lucide-library" :label="t('team.lib.addOne')" @click="openAdd" />
    </div>
  </UCard>

  <div v-else class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="me-auto text-lg font-semibold">{{ t('org.editor.agentsCount', { n: agents.length }) }}</h2>
      <UButton icon="i-lucide-history" :label="t('org.editor.history')" color="neutral" variant="outline" @click="historyOpen = true" />
      <UDropdownMenu
        :content="{ align: 'end' }"
        :items="[[
          { label: t('team.export'), icon: 'i-lucide-download', onSelect: exportAgents },
          ...(isAdmin ? [{ label: t('project.setupAi'), icon: 'i-lucide-sparkles', to: `${base}/setup` }] : [])
        ]]"
      >
        <UButton icon="i-lucide-ellipsis" color="neutral" variant="outline" :aria-label="t('project.actionsAria')" />
      </UDropdownMenu>
      <UButton v-if="isAdmin" icon="i-lucide-user-plus" :label="t('org.editor.addAgent')" @click="openAdd" />
    </div>

    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      <!-- a div, not a link: the switch and the menu sit inside -->
      <div
        v-for="a in agents" :key="a.id" role="link" tabindex="0"
        class="cursor-pointer rounded-lg border bg-(--ui-bg) p-3 text-left transition hover:border-(--ui-primary) hover:shadow-sm"
        :class="a.id === project.default_agent_id ? 'border-(--ui-primary)/50' : 'border-(--ui-border)'"
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
          <div class="flex shrink-0 items-center gap-1.5" @click.stop>
            <UTooltip v-if="a.id === project.default_agent_id" :text="t('team.defaultHint')">
              <UBadge :label="t('team.default')" icon="i-lucide-star" variant="subtle" size="sm" />
            </UTooltip>
            <UBadge v-if="a.enabled === false" :label="t('org.editor.paused')" color="warning" variant="subtle" size="sm" icon="i-lucide-pause" />
            <AgentSwitch :agent="a" @changed="changed" />
            <UDropdownMenu :content="{ align: 'end' }" :items="menu(a)">
              <UButton icon="i-lucide-ellipsis-vertical" size="xs" color="neutral" variant="ghost" :aria-label="t('project.actionsAria')" />
            </UDropdownMenu>
          </div>
        </div>
        <p v-if="a.role" class="mt-2 line-clamp-2 text-sm text-(--ui-text-toned)">{{ a.role }}</p>
        <div class="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-(--ui-text-muted)" :class="a.enabled === false ? 'opacity-50' : ''">
          <UBadge :label="modelTierLabel[a.model_tier]" color="neutral" variant="subtle" size="sm" />
          <span class="font-mono">{{ resolvedModel(a) }}</span>
          <span class="inline-flex items-center gap-1" :class="permRank(agentLevel(a.permissions)) >= 2 ? 'text-(--ui-warning)' : ''" :title="permOf(agentLevel(a.permissions)).description">
            <UIcon :name="a.permissions.caps ? 'i-lucide-sliders-horizontal' : permOf(agentLevel(a.permissions)).icon" class="size-3.5" />{{ a.permissions.caps ? t('org.editor.customCaps', { n: a.permissions.caps.length }) : permOf(agentLevel(a.permissions)).label }}
          </span>
          <UIcon v-if="a.permissions.requires_approval" name="i-lucide-shield-check" class="size-3.5" :title="t('org.editor.requiresApproval')" />
        </div>
      </div>
    </div>

    <RevisionHistory v-model:open="historyOpen" :project-id="project.id" @restored="changed" />
  </div>

  <UModal v-model:open="addOpen" :title="t('org.editor.newAgent')" :ui="{ content: 'max-w-2xl' }">
    <template #body>
      <AgentLibrary v-if="addTab === 'library'" v-model:selected="libPick" @pick="pickLib" />
      <form v-else id="agent-add" class="space-y-4" @submit.prevent="add">
        <div v-if="fromLib" class="flex items-center gap-2 rounded-lg bg-(--ui-bg-elevated) px-3 py-2 text-sm">
          <UIcon name="i-lucide-library" class="size-4 shrink-0 text-primary" />
          <span class="me-auto min-w-0 truncate">{{ t('team.lib.from', { name: fromLib.name }) }}</span>
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-arrow-left" :label="t('team.lib.back')" @click="addTab = 'library'" />
        </div>
        <LibraryPackNotice v-if="fromLib?.requires_pack" :pack="fromLib.requires_pack" />
        <div class="grid gap-3 sm:grid-cols-2">
          <UFormField :label="t('org.form.name')" required>
            <UInput v-model="form.name" class="w-full" autofocus />
          </UFormField>
          <UFormField :label="t('org.form.key')" required>
            <template #hint>
              <InfoTip :text="t('org.form.keyHelp')" icon-class="size-4 text-(--ui-text-muted)" />
            </template>
            <UInput v-model="form.key" class="w-full font-mono" @input="keyTouched = true" />
          </UFormField>
        </div>
        <UFormField :label="t('org.form.role')">
          <UInput v-model="form.role" class="w-full" :placeholder="t('org.form.rolePlaceholder')" />
        </UFormField>
        <p v-if="fromLib?.skills?.length" class="flex flex-wrap items-center gap-1.5 text-sm">
          <span class="text-(--ui-text-muted)">{{ t('team.lib.skills') }}</span>
          <UBadge v-for="sk in fromLib.skills" :key="sk" color="neutral" variant="subtle" class="font-mono" :label="sk" />
          <InfoTip :text="t('team.lib.skillsHelp')" icon-class="size-3.5 text-(--ui-text-muted)" />
        </p>
        <UFormField :label="t('org.form.modelTier')">
          <USelect v-model="form.model_tier" :items="Object.entries(modelTierLabel).map(([value, label]) => ({ label, value }))" class="w-full" />
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
        <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="addOpen = false" />
        <UButton v-if="addTab === 'library'" icon="i-lucide-check" :disabled="!libPick" :label="t('team.lib.use')" @click="libPick && pickLib(libPick)" />
        <UButton v-else type="submit" form="agent-add" :loading="adding" :disabled="!fromLib || !form.name.trim() || !form.key.trim()" :label="t('team.addAndConfigure')" />
      </div>
    </template>
  </UModal>
</template>
