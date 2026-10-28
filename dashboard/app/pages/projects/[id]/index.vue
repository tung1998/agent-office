<script setup lang="ts">
import type { Attachment } from '~/components/PromptInput.vue'
const route = useRoute()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const id = computed(() => route.params.id as string)

const _f1 = useLiveFetch<{ project: Project }>(() => `/api/projects/${id.value}`, { lazy: true })
const { data, refresh, error: loadError } = _f1
// not awaited: the page shows at once with a skeleton (a phone over a VPN)
const project = computed(() => data.value?.project)
// hidden from this account's menu, still on the Projects page
const menu = useProjectMenu()
const hidingMenu = ref(false)
async function toggleMenu() {
  hidingMenu.value = true
  const hide = !menu.isHidden(id.value)
  try {
    await menu.setHidden(id.value, hide)
    toast.add({ title: hide ? t('project.hiddenFromMenu') : t('project.shownInMenu'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    hidingMenu.value = false
  }
}

// the tab lives in the URL so the sidebar can link to each section
// (older links: tab=config&section=…, tab=tools, tab=channels: bots live in Automations now,
// tab=model: the agents)
type Tab = 'chat' | 'tasks' | 'support' | 'automations' | 'ops' | 'files' | 'agents' | 'workflows' | 'perm' | 'skill' | 'mcp' | 'info' | 'log' | 'burn'
const tabs: Tab[] = ['chat', 'tasks', 'support', 'automations', 'ops', 'files', 'agents', 'workflows', 'perm', 'skill', 'mcp', 'info', 'log', 'burn']
const aliases: Record<string, Tab> = { tools: 'skill', channels: 'automations', model: 'agents' }

// "Hỏi agent" from Vận hành: open Chat with the log attached
// send=true ("Sửa lỗi") starts a new conversation and sends right away
const chatPrefill = useState<{ text: string, files: Attachment[], send?: boolean, agentId?: string, conversationId?: string } | null>('chat-prefill', () => null)
function askAgent(text: string, files: Attachment[], send = false) {
  chatPrefill.value = { text, files, send }
  tab.value = 'chat'
}
const tab = computed<Tab>({
  get: () => {
    const raw = String((route.query.tab === 'config' ? route.query.section ?? 'info' : route.query.tab) ?? '')
    const q = aliases[raw] ?? raw
    const found = tabs.find(t => t === q) ?? 'chat'
    if (found === 'ops' && project.value && !hasFeature(project.value, 'ops')) return 'chat' // Vận hành turned off
    if (found === 'support' && project.value && !hasFeature(project.value, 'support')) return 'chat' // the support pack off
    return (found === 'log' || found === 'files') && !isAdmin.value ? 'chat' : found // the change log and files are for admins
  },
  set: t => navigateTo({ query: { tab: t } }, { replace: true })
})
const { touch } = useProjectUsage()
watch(id, v => touch(v), { immediate: true })

// ---- edit project ----
const editOpen = ref(false)
const form = reactive({ name: '', description: '', git_remote: '' })
function openEdit() {
  Object.assign(form, { name: project.value!.name, description: project.value!.description, git_remote: project.value!.git_remote })
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

</script>

<template>
  <PageShell :title="project?.name ?? t('project.defaultTitle')">
    <template v-if="project?.path && hasFeature(project, 'git')" #subtitle>
      <GitBar :project-id="project.id" />
    </template>

    <!-- Chat fills the page height (the panel body is a bounded flex column) -->
    <div v-if="project" :class="((tab === 'chat' || tab === 'tasks') && project.agent_count) || tab === 'files' || tab === 'support' ? 'flex min-h-0 flex-1 flex-col gap-4' : 'space-y-4'">
      <UAlert v-if="!project.exists" color="error" variant="subtle" icon="i-lucide-folder-x" :title="t('project.notFound')" />


      <!-- a tab whose panel waits for its data (an await in setup) shows its shape
           meanwhile; keyed by the tab, so each switch shows it again -->
      <Suspense :key="tab">
        <div class="contents">
          <AutomationsPanel v-if="tab === 'automations'" :project-id="project.id" />
          <SupportPanel v-else-if="tab === 'support'" :project-id="project.id" />
          <BurnPanel v-else-if="tab === 'burn' && isAdmin" :project-id="project.id" />
          <FilesPanel v-else-if="tab === 'files' && isAdmin && project.path" :project-id="project.id" :git="hasFeature(project, 'git')" />
          <AuditLog v-else-if="tab === 'log' && isAdmin" :key="project.id" class="max-w-6xl" :filter="{ project: project.id }" show-filters />
          <OpsPanel v-else-if="tab === 'ops'" :project-id="project.id" :has-folder="!!project.path" @ask-agent="askAgent" />
          <ProjectWorkflows v-else-if="tab === 'workflows'" :project-id="project.id" />
          <template v-else-if="['info', 'agents', 'perm', 'skill', 'mcp'].includes(tab)">
            <div v-if="tab === 'info'" class="space-y-4">
            <UCard class="max-w-4xl" :ui="{ body: 'space-y-3 sm:p-4' }">
              <div class="grid gap-x-4 gap-y-2 text-sm sm:grid-cols-[8rem_1fr]">
                <span class="text-(--ui-text-muted)">{{ t('project.infoFolder') }}</span>
                <span v-if="project.scope === 'folder'" class="min-w-0 break-all font-mono text-xs">{{ project.path }}</span>
                <span v-else>{{ t('project.machineHelper') }}</span>
                <span class="text-(--ui-text-muted)">{{ t('project.infoRemote') }}</span>
                <span class="min-w-0 break-all font-mono text-xs">{{ project.git_remote || '—' }}</span>
                <span class="text-(--ui-text-muted)">{{ t('project.infoDescription') }}</span>
                <p class="whitespace-pre-line text-(--ui-text-toned)">{{ project.description || '—' }}</p>
              </div>
              <!-- the project's own actions (the page header holds none) -->
              <div class="flex flex-wrap gap-2">
                <template v-if="isAdmin">
                  <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('project.rename')" @click="openEdit" />
                  <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-sparkles" :label="t('project.setupAi')" :to="`/projects/${id}/setup`" />
                </template>
                <UButton
                  size="xs" color="neutral" variant="outline" :loading="hidingMenu"
                  :icon="menu.isHidden(id) ? 'i-lucide-eye' : 'i-lucide-eye-off'" :label="menu.isHidden(id) ? t('project.showInMenu') : t('project.hideFromMenu')"
                  @click="toggleMenu"
                />
                <UButton v-if="isAdmin" size="xs" color="error" variant="ghost" icon="i-lucide-folder-minus" :label="t('project.unmanage')" :loading="removing" class="ms-auto" @click="removeRepo" />
              </div>
            </UCard>
              <ProjectLinks :project-id="project.id" />
              <FeaturePacks :project-id="project.id" @changed="refresh()" />
              <PackSync v-if="isAdmin" :project-id="project.id" @changed="refresh()" />
              <ProjectBudget :project-id="project.id" />
            </div>
            <PolicyPanel v-else-if="tab === 'perm'" :project-id="project.id" />
            <ToolsPanel v-else-if="tab === 'skill' || tab === 'mcp'" :key="tab" :kind="tab" :project-path="project.path" :project-id="project.id" />
            <ProjectAgents v-else :project="project" @changed="refresh()" />
          </template>
          <template v-else-if="project.agent_count">
            <!-- Việc is gone (ADR-057): an old link to it opens the chat -->
            <ChatPanel :project-id="project.id" />
          </template>
          <!-- no agent yet: pick from the library first -->
          <ProjectAgents v-else :project="project" @changed="refresh()" />
        </div>
        <template #fallback>
          <div class="max-w-4xl rounded-lg border border-(--ui-border)" aria-busy="true"><LoadingRows :n="5" /></div>
        </template>
      </Suspense>
    </div>
    <UAlert v-else-if="loadError" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="apiError(loadError)" />
    <!-- the project on its way -->
    <div v-else class="flex min-h-0 flex-1 gap-4" aria-busy="true">
      <div class="hidden w-60 shrink-0 rounded-lg border border-(--ui-border) md:block"><LoadingRows :n="6" /></div>
      <div class="flex-1 space-y-3 rounded-lg border border-(--ui-border) p-4">
        <USkeleton class="h-4 w-1/3" /><USkeleton class="h-4 w-2/3" /><USkeleton class="h-4 w-1/2" />
      </div>
    </div>

    <UModal v-model:open="editOpen" :title="t('project.editTitle')">
      <template #body>
        <form id="project-edit" class="space-y-4" @submit.prevent="saveRepo">
          <UFormField :label="t('projects.name')"><UInput v-model="form.name" class="w-full" /></UFormField>
          <UFormField :label="t('project.description')"><UTextarea v-model="form.description" :rows="3" class="w-full" /></UFormField>
          <UFormField :label="t('project.infoRemote')" :hint="t('project.remoteHint')"><UInput v-model="form.git_remote" placeholder="https://github.com/org/repo.git" class="w-full font-mono" /></UFormField>
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
