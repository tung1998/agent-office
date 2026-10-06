<script setup lang="ts">
import type { Attachment } from '~/components/PromptInput.vue'
const route = useRoute()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const id = computed(() => route.params.id as string)

const _f1 = useLiveFetch<{ project: Project }>(() => `/api/projects/${id.value}`, { lazy: true })
const { data, refresh, error: loadError } = _f1
const _f2 = useLiveFetch<{ templates: OrgModel[] }>('/api/templates', { lazy: true })
const { data: tplData } = _f2
// not awaited: the page shows at once with a skeleton (a phone over a VPN)
const project = computed(() => data.value?.project)
const templates = computed(() => tplData.value?.templates ?? [])

// the tab lives in the URL so the sidebar can link to each section
// (older links: tab=config&section=…, tab=tools, tab=channels: bots live in Automations now)
type Tab = 'chat' | 'tasks' | 'automations' | 'ops' | 'files' | 'model' | 'perm' | 'skill' | 'mcp' | 'info' | 'log' | 'burn'
const tabs: Tab[] = ['chat', 'tasks', 'automations', 'ops', 'files', 'model', 'perm', 'skill', 'mcp', 'info', 'log', 'burn']

// "Hỏi agent" from Vận hành: open Chat with the log attached
// send=true ("Sửa lỗi") starts a new conversation and sends right away
const chatPrefill = useState<{ text: string, files: Attachment[], send?: boolean, agentId?: string, conversationId?: string } | null>('chat-prefill', () => null)
function askAgent(text: string, files: Attachment[], send = false) {
  chatPrefill.value = { text, files, send }
  tab.value = 'chat'
}
const tab = computed<Tab>({
  get: () => {
    const q = route.query.tab === 'config' ? (route.query.section ?? 'info') : route.query.tab === 'tools' ? 'skill' : route.query.tab === 'channels' ? 'automations' : route.query.tab
    const found = tabs.find(t => t === q) ?? 'chat'
    return (found === 'log' || found === 'files') && !isAdmin.value ? 'chat' : found // the change log and files are for admins
  },
  set: t => navigateTo({ query: { tab: t } }, { replace: true })
})
const { touch } = useProjectUsage()
watch(id, v => touch(v), { immediate: true })

// ---- apply / change model ----
const applyOpen = ref(false)
const templateId = ref('')
const applying = ref(false)
function openApply() {
  templateId.value = templates.value.find(t => t.id === project.value?.model?.source_template_id)?.id ?? templates.value[0]?.id ?? ''
  applyOpen.value = true
}
async function apply() {
  applying.value = true
  try {
    await $fetch(`/api/projects/${id.value}/model`, { method: 'POST', body: { template_id: templateId.value, replace: !!project.value?.model } })
    applyOpen.value = false
    await refresh()
    toast.add({ title: t('project.applyDone'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    applying.value = false
  }
}

// ---- edit project ----
const editOpen = ref(false)
const form = reactive({ name: '', description: '' })
function openEdit() {
  Object.assign(form, { name: project.value!.name, description: project.value!.description })
  editOpen.value = true
}
const saving = ref(false)
async function saveRepo() {
  if (saving.value) return
  saving.value = true
  try {
    await $fetch(`/api/projects/${id.value}`, { method: 'PATCH', body: { ...form } })
    editOpen.value = false
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
const removing = ref(false)
async function removeRepo() {
  if (removing.value || !confirm(t('project.confirmUnmanage', { name: project.value?.name ?? '' }))) return
  removing.value = true
  try {
    await $fetch(`/api/projects/${id.value}`, { method: 'DELETE' })
    await navigateTo('/projects')
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
    removing.value = false
  }
}

function exportModel() {
  if (project.value?.model) window.open(`/api/org-models/${project.value.model.id}/export`, '_blank')
}

// Save the project's customised model as a reusable template.
async function saveAsTemplate() {
  const m = project.value?.model
  if (!m) return
  const key = prompt(t('project.templateKeyPrompt'), `${project.value!.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')}-${m.key}`)
  if (!key) return
  try {
    const res = await $fetch<{ model: OrgModel }>('/api/templates', { method: 'POST', body: { source_id: m.id, key, name: `${m.name} (${project.value!.name})` } })
    toast.add({ title: t('project.templateSaved'), color: 'success', actions: [{ label: t('project.templateSavedOpen'), onClick: () => { navigateTo(`/templates/${res.model.id}`) } }] })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <PageShell :title="project?.name ?? t('project.defaultTitle')">
    <template v-if="project?.path" #subtitle>
      <GitBar :project-id="project.id" />
    </template>
    <template #actions>
      <template v-if="project && isAdmin">
        <UButton v-if="!project.model" :to="`/projects/${id}/setup`" size="sm" icon="i-lucide-sparkles" :label="t('project.setupAi')" />
        <UDropdownMenu
          :content="{ align: 'end' }"
          :items="[[
            { label: t('project.rename'), icon: 'i-lucide-pencil', onSelect: openEdit },
            { label: t('project.setupAi'), icon: 'i-lucide-sparkles', to: `/projects/${id}/setup` },
            { label: project.model ? t('project.changeModel') : t('project.chooseModel'), icon: 'i-lucide-network', onSelect: openApply }
          ], [
            { label: t('project.downloadModel'), icon: 'i-lucide-download', disabled: !project.model, onSelect: exportModel },
            { label: t('project.saveModel'), icon: 'i-lucide-bookmark-plus', disabled: !project.model, onSelect: saveAsTemplate }
          ], [
            { label: t('project.unmanage'), icon: 'i-lucide-folder-minus', color: 'error', disabled: removing, onSelect: removeRepo }
          ]]"
        >
          <UButton icon="i-lucide-ellipsis" color="neutral" variant="ghost" :aria-label="t('project.actionsAria')" />
        </UDropdownMenu>
      </template>
    </template>

    <!-- Chat fills the page height (the panel body is a bounded flex column) -->
    <div v-if="project" :class="((tab === 'chat' || tab === 'tasks') && project.model) || tab === 'files' ? 'flex min-h-0 flex-1 flex-col gap-4' : 'space-y-4'">
      <UAlert v-if="!project.exists" color="error" variant="subtle" icon="i-lucide-folder-x" :title="t('project.notFound')" />


      <AutomationsPanel v-if="tab === 'automations'" :project-id="project.id" />
      <BurnPanel v-else-if="tab === 'burn' && isAdmin" :project-id="project.id" />
      <FilesPanel v-else-if="tab === 'files' && isAdmin && project.path" :project-id="project.id" />
      <AuditLog v-else-if="tab === 'log' && isAdmin" :key="project.id" class="max-w-6xl" :filter="{ project: project.id }" show-filters />
      <OpsPanel v-else-if="tab === 'ops'" :project-id="project.id" :has-folder="!!project.path" @ask-agent="askAgent" />
      <template v-else-if="['info', 'model', 'perm', 'skill', 'mcp'].includes(tab)">
        <div v-if="tab === 'info'" class="space-y-4">
        <UCard class="max-w-4xl" :ui="{ body: 'space-y-3 sm:p-4' }">
          <div class="grid gap-x-4 gap-y-2 text-sm sm:grid-cols-[8rem_1fr]">
            <span class="text-(--ui-text-muted)">{{ t('project.infoFolder') }}</span>
            <span v-if="project.scope === 'folder'" class="min-w-0 break-all font-mono text-xs">{{ project.path }}</span>
            <span v-else>{{ t('project.machineHelper') }}</span>
            <template v-if="project.git_remote">
              <span class="text-(--ui-text-muted)">{{ t('project.infoRemote') }}</span>
              <span class="min-w-0 break-all font-mono text-xs">{{ project.git_remote }}</span>
            </template>
            <span class="text-(--ui-text-muted)">{{ t('project.infoDescription') }}</span>
            <p class="whitespace-pre-line text-(--ui-text-toned)">{{ project.description || '—' }}</p>
          </div>
          <UButton v-if="isAdmin" size="xs" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('project.rename')" @click="openEdit" />
        </UCard>
          <ProjectBudget :project-id="project.id" />
        </div>
        <PolicyPanel v-else-if="tab === 'perm'" :project-id="project.id" />
        <ToolsPanel v-else-if="tab === 'skill' || tab === 'mcp'" :key="tab" :kind="tab" :project-path="project.path" :project-id="project.id" />
        <OrgModelEditor v-else-if="project.model" :key="project.model.id" :model-id="project.model.id" agents-first @changed="refresh()" />
        <NoModel v-else :project-id="id" :admin="isAdmin" @choose="openApply" />
      </template>
      <template v-else-if="project.model">
        <!-- Việc is gone (ADR-057): an old link to it opens the chat -->
        <ChatPanel :project-id="project.id" />
      </template>
      <NoModel v-else :project-id="id" :admin="isAdmin" @choose="openApply" />
    </div>
    <UAlert v-else-if="loadError" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="apiError(loadError)" />
    <!-- the project on its way -->
    <div v-else class="flex min-h-0 flex-1 gap-4" aria-busy="true">
      <div class="hidden w-60 shrink-0 rounded-lg border border-(--ui-border) md:block"><LoadingRows :n="6" /></div>
      <div class="flex-1 space-y-3 rounded-lg border border-(--ui-border) p-4">
        <USkeleton class="h-4 w-1/3" /><USkeleton class="h-4 w-2/3" /><USkeleton class="h-4 w-1/2" />
      </div>
    </div>

    <UModal v-model:open="applyOpen" :title="project?.model ? t('project.changeModel') : t('project.chooseModel')" :ui="{ content: 'max-w-xl' }">
      <template #body>
        <div class="space-y-4">
          <UAlert
            v-if="project?.model" color="warning" variant="subtle" icon="i-lucide-triangle-alert"
            :description="t('project.changeModelWarn')"
          />
          <TemplatePicker v-model="templateId" :templates="templates" />
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="applyOpen = false" />
          <UButton :loading="applying" :disabled="!templateId" :label="t('project.applyBtn')" @click="apply" />
        </div>
      </template>
    </UModal>

    <UModal v-model:open="editOpen" :title="t('project.editTitle')">
      <template #body>
        <form id="project-edit" class="space-y-4" @submit.prevent="saveRepo">
          <UFormField :label="t('projects.name')"><UInput v-model="form.name" class="w-full" /></UFormField>
          <UFormField :label="t('project.description')"><UTextarea v-model="form.description" :rows="3" class="w-full" /></UFormField>
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="editOpen = false" />
          <UButton type="submit" form="project-edit" :label="t('common.save')" :loading="saving" :disabled="saving" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
