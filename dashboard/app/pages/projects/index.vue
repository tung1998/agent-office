<script setup lang="ts">
const { isOfficeAdmin, canCreateProject, manages, fetchMe } = useAuth()
const { t } = useLang()
const _f1 = useLiveFetch<{ projects: Project[] }>('/api/projects')
const { data, refresh } = _f1
const _f3 = useLiveFetch<{ mode: string, home_dir: string, project_root?: string, clone_root?: string }>('/api/system')
const { data: sys } = _f3
await Promise.all([_f1, _f3]) // started together: one round trip, not 2 (a phone over a VPN)
// the account's order (dragged here; the menu's options are in Settings); the menu's projects get a mark
const { isHidden, setOrder, arrange, menu, max } = useProjectMenu()
const ordered = computed(() => arrange(data.value?.projects ?? []))
const projects = ref<Project[]>([])
watch(ordered, v => { projects.value = [...v] }, { immediate: true })
const inMenu = computed(() => new Set(menu(data.value?.projects ?? []).map(p => p.id)))
const dragging = ref('')
function dragOver(target: Project) {
  const from = projects.value.findIndex(p => p.id === dragging.value)
  const to = projects.value.indexOf(target)
  if (from < 0 || to < 0 || from === to) return
  const list = [...projects.value]
  list.splice(to, 0, ...list.splice(from, 1))
  projects.value = list
}
async function dragEnd() {
  if (!dragging.value) return
  dragging.value = ''
  const ids = projects.value.map(p => p.id)
  if (ids.join() === ordered.value.map(p => p.id).join()) return
  try {
    await setOrder(ids)
  } catch (e) {
    projects.value = [...ordered.value]
    toast.add({ title: apiError(e), color: 'error' })
  }
}

const addOpen = ref(false)
type Scope = 'folder' | 'clone' | 'idea' | 'template' | 'machine'
// clone: a pasted git link, cloned into parent/dir (dir from the link when empty)
// idea: a new, empty repo in parent/dir whose Burn builds it (ADR-139)
// template: a new repo from a pack of office's library (ADR-199); a root pack's head is given the goal
const form = reactive({ scope: 'folder' as Scope, path: '', name: '', url: '', dir: '', parent: '', idea: '', stack: '', goal: '', template: '' })
const { data: packData } = useLiveFetch<{ packs: { ref: string, title: string, description: string, root?: boolean, bundle: string }[] }>('/api/project-packs', { lazy: true })
const packItems = computed(() => (packData.value?.packs ?? []).map(p => ({ label: `${p.title} · ${p.ref}`, value: p.ref })))
const pickedPack = computed(() => packData.value?.packs.find(p => p.ref === form.template))
const error = ref('')
const adding = ref(false)
const urlDir = computed(() => form.url.trim().replace(/\/+$/, '').split(/[/:]/).pop()?.replace(/\.git$/, '') ?? '')
const cloneDest = computed(() => {
  const dir = form.dir.trim() || urlDir.value
  return form.parent && dir ? `${form.parent.replace(/\/+$/, '')}/${dir}` : ''
})
const ideaDest = computed(() => form.parent && form.dir.trim() ? `${form.parent.replace(/\/+$/, '')}/${form.dir.trim()}` : '')
const canAdd = computed(() => ({
  folder: !!form.path,
  clone: !!form.url.trim() && !!form.parent,
  idea: !!form.idea.trim() && !!form.dir.trim() && !!form.parent,
  template: !!form.template && !!form.dir.trim() && !!form.parent,
  machine: !!form.name
})[form.scope])

function openAdd(path = '', name = '', template = '') {
  Object.assign(form, { scope: template ? 'template' : 'folder', path, name, url: '', dir: '', parent: sys.value?.clone_root ?? '', idea: '', stack: '', goal: '', template })
  error.value = ''
  addOpen.value = true
}

// folders Claude Code has opened on this machine that office does not manage yet
// stop managing one (its folder stays): it comes back under "found on the machine"
const toast = useToast()
const removing = reactive(new Set<string>())
async function remove(p: Project) {
  if (removing.has(p.id) || !confirm(t('project.confirmUnmanage', { name: p.name }))) return
  removing.add(p.id)
  try {
    await $fetch(`/api/projects/${p.id}`, { method: 'DELETE' })
    await refresh()
    scanMachine()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    removing.delete(p.id)
  }
}
const found = ref<MachineProject[]>([])
const scanning = ref(false)
async function scanMachine() {
  scanning.value = true
  try {
    const inv = await $fetch<Inventory>('/api/automation/scan')
    const known = new Set(projects.value.map(p => p.path))
    found.value = inv.projects.filter(p => p.exists && !p.project_id && !known.has(p.path))
  } catch {
    found.value = []
  } finally {
    scanning.value = false
  }
}
onMounted(() => { if (isOfficeAdmin.value) scanMachine() })
// the Library's project templates open the form with theirs picked (ADR-199)
const route = useRoute()
onMounted(() => {
  const ref = typeof route.query.template === 'string' ? route.query.template : ''
  if (ref && canCreateProject.value) {
    openAdd('', '', ref)
    navigateTo({ query: {} }, { replace: true })
  }
})

async function add() {
  adding.value = true
  error.value = ''
  try {
    const res = form.scope === 'clone'
      ? await $fetch<{ project: Project }>('/api/projects/clone', {
          method: 'POST',
          body: { url: form.url.trim(), parent: form.parent, dir: form.dir.trim(), name: form.name }
        })
      : form.scope === 'idea' || form.scope === 'template'
        ? await $fetch<{ project: Project }>('/api/projects/new', {
            method: 'POST',
            body: form.scope === 'template'
              ? { template: form.template, goal: pickedPack.value?.root ? form.goal.trim() : '', parent: form.parent, dir: form.dir.trim(), name: form.name }
              : { idea: form.idea.trim(), stack: form.stack.trim(), parent: form.parent, dir: form.dir.trim(), name: form.name }
          })
        : await $fetch<{ project: Project }>('/api/projects', {
          method: 'POST',
            body: { path: form.scope === 'folder' ? form.path : '', name: form.name }
          })
    addOpen.value = false
    await fetchMe() // a manager now has this one too (ADR-224)
    // a template comes set up (its pack); any other has the starter agent: the setup picks what else it needs (ADR-166)
    await navigateTo(form.scope === 'template' ? `/projects/${res.project.id}` : `/projects/${res.project.id}/setup`)
  } catch (e) {
    error.value = apiError(e)
  } finally {
    adding.value = false
  }
  await refresh()
}
</script>

<template>
  <PageShell :title="t('projects.title')">
    <template #actions>
      <UButton v-if="canCreateProject" icon="i-lucide-folder-plus" :label="t('projects.add')" @click="openAdd()" />
    </template>

    <div class="space-y-4">
      <UAlert
        v-if="sys" color="neutral" variant="subtle" icon="i-lucide-hard-drive"
        :title="sys.mode === 'local' ? t('projects.modeLocal') : t('projects.modeMachine')"
        :description="sys.mode === 'local'
          ? t('projects.modeLocalDesc', { root: sys.project_root ?? '', home: sys.home_dir })
          : t('projects.modeMachineDesc', { home: sys.home_dir })"
      />

      <div v-if="!projects.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
        <UIcon name="i-lucide-folder-git-2" class="mx-auto size-8 text-(--ui-text-dimmed)" />
        <p class="mt-2 font-medium">{{ t('projects.empty') }}</p>
        <p class="text-sm text-(--ui-text-muted)">
          {{ t('projects.emptyDesc', { cmd: 'office init' }) }}
        </p>
        <UButton v-if="canCreateProject" class="mt-4" icon="i-lucide-folder-plus" :label="t('projects.add')" @click="openAdd()" />
      </div>

      <p v-if="projects.length > 1" class="text-xs text-(--ui-text-muted)">{{ t('projects.dragHint', { n: max }) }}</p>
      <div class="grid gap-3">
        <NuxtLink
          v-for="p in projects" :key="p.id" :to="`/projects/${p.id}`" draggable="true"
          class="flex flex-wrap items-center gap-4 rounded-lg border border-(--ui-border) p-4 transition hover:border-(--ui-primary)"
          :class="dragging === p.id ? 'opacity-50' : ''"
          @dragstart="(e: DragEvent) => { dragging = p.id; e.dataTransfer?.setData('text/plain', p.id) }"
          @dragover.prevent="dragOver(p)" @drop.prevent @dragend="dragEnd"
        >
          <UIcon name="i-lucide-grip-vertical" class="size-4 shrink-0 cursor-grab text-(--ui-text-dimmed)" />
          <UIcon :name="p.scope === 'machine' ? 'i-lucide-monitor' : 'i-lucide-folder-git-2'" class="size-6 text-primary" />
          <div class="min-w-0 flex-1">
            <p class="font-semibold">{{ p.name }}</p>
            <p v-if="p.scope === 'folder'" class="truncate font-mono text-xs text-(--ui-text-muted)">{{ p.path }}</p>
            <p v-else class="text-xs text-(--ui-text-muted)">{{ t('projects.machineHelper') }}</p>
          </div>
          <UBadge v-if="isHidden(p.id)" icon="i-lucide-eye-off" :label="t('projects.hiddenFromMenu')" color="neutral" variant="subtle" />
          <UBadge v-else-if="inMenu.has(p.id)" icon="i-lucide-panel-left" :label="t('projects.inMenu')" color="primary" variant="subtle" />
          <UBadge v-if="!p.exists" :label="t('projects.notFound')" color="error" variant="subtle" />
          <div v-if="p.agent_count" class="flex items-center gap-2 text-sm">
            <UIcon name="i-lucide-bot" class="size-4" />
            {{ t('projects.agentCount', { n: p.agent_count }) }}
          </div>
          <UBadge v-else :label="t('team.noAgents')" color="warning" variant="subtle" />
          <UButton
            v-if="manages(p.id)" size="xs" color="neutral" variant="ghost" icon="i-lucide-trash-2" class="hover:text-(--ui-error)"
            :aria-label="t('project.unmanage')" :title="t('project.unmanage')" :loading="removing.has(p.id)" :disabled="removing.has(p.id)"
            @click.prevent.stop="remove(p)"
          />
          <UIcon name="i-lucide-chevron-right" class="size-4 text-(--ui-text-dimmed)" />
        </NuxtLink>
      </div>

      <section v-if="isOfficeAdmin && (found.length || scanning)" class="space-y-2">
        <div class="flex items-center gap-2">
          <h3 class="text-sm font-semibold">{{ t('projects.foundOnMachine') }}</h3>
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :loading="scanning" :aria-label="t('projects.rescan')" class="ms-auto" @click="scanMachine" />
        </div>
        <div class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
          <div v-for="f in found" :key="f.path" class="flex flex-wrap items-center gap-3 px-4 py-2.5">
            <UIcon name="i-lucide-folder" class="size-4 text-(--ui-text-muted)" />
            <div class="min-w-0 flex-1">
              <p class="font-medium">{{ f.name }}</p>
              <p class="truncate font-mono text-xs text-(--ui-text-muted)">{{ f.path }}</p>
            </div>
            <div class="flex flex-wrap gap-1">
              <UBadge v-if="f.skills" color="neutral" variant="subtle" size="sm" icon="i-lucide-sparkles" :label="t('projects.skillCount', { n: f.skills })" />
              <UBadge v-if="f.agents" color="neutral" variant="subtle" size="sm" icon="i-lucide-bot" :label="t('projects.agentCount', { n: f.agents })" />
              <UBadge v-if="f.mcp" color="neutral" variant="subtle" size="sm" icon="i-lucide-plug-zap" :label="t('projects.mcpCount', { n: f.mcp })" />
              <UBadge v-if="f.claude_md" color="neutral" variant="subtle" size="sm" label="CLAUDE.md" />
              <UBadge v-if="f.agents_md" color="neutral" variant="subtle" size="sm" label="AGENTS.md" />
            </div>
            <UButton size="sm" variant="outline" icon="i-lucide-plus" :label="t('projects.addAction')" @click="openAdd(f.path, f.name)" />
          </div>
        </div>
      </section>
    </div>

    <UModal v-model:open="addOpen" :title="t('projects.addModalTitle')" :ui="{ content: 'max-w-2xl' }">
      <template #body>
        <form id="project-form" class="space-y-4" @submit.prevent="add">
          <UFormField :label="t('projects.scope')">
            <div class="grid gap-2 sm:grid-cols-2">
              <button
                v-for="opt in [
                  { value: 'folder', icon: 'i-lucide-folder-git-2', title: t('projects.scopeFolderTitle'), text: t('projects.scopeFolderText') },
                  { value: 'clone', icon: 'i-lucide-git-branch-plus', title: t('projects.scopeCloneTitle'), text: t('projects.scopeCloneText') },
                  { value: 'idea', icon: 'i-lucide-sparkles', title: t('projects.scopeIdeaTitle'), text: t('projects.scopeIdeaText') },
                  { value: 'template', icon: 'i-lucide-building-2', title: t('projects.scopeTemplateTitle'), text: t('projects.scopeTemplateText') },
                  { value: 'machine', icon: 'i-lucide-monitor', title: t('projects.scopeMachineTitle'), text: t('projects.scopeMachineText') }
                ]" :key="opt.value" type="button"
                class="flex items-start gap-3 rounded-lg border p-3 text-left transition"
                :class="form.scope === opt.value ? 'border-(--ui-primary) bg-(--ui-primary)/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
                @click="form.scope = opt.value as Scope"
              >
                <UIcon :name="opt.icon" class="mt-0.5 size-5 shrink-0 text-primary" />
                <div>
                  <p class="font-medium">{{ opt.title }}</p>
                  <p class="text-xs text-(--ui-text-muted)">{{ opt.text }}</p>
                </div>
              </button>
            </div>
          </UFormField>

          <UFormField v-if="form.scope === 'folder'" :label="t('projects.folder')" required>
            <FolderTree v-model="form.path" />
          </UFormField>

          <template v-if="form.scope === 'clone'">
            <div class="grid gap-3 sm:grid-cols-[1fr_12rem]">
              <UFormField required>
                <template #label><InfoLabel :label="t('projects.cloneUrl')" :info="t('projects.cloneUrlHelp')" /></template>
                <UInput v-model="form.url" class="w-full font-mono" icon="i-lucide-link" :placeholder="t('projects.cloneUrlPlaceholder')" autofocus />
              </UFormField>
              <UFormField :label="t('projects.cloneDir')">
                <UInput v-model="form.dir" class="w-full font-mono" :placeholder="urlDir" />
              </UFormField>
            </div>
            <UFormField :label="t('projects.cloneParent')" :help="cloneDest ? t('projects.cloneInto', { path: cloneDest }) : undefined" required>
              <FolderTree v-model="form.parent" />
            </UFormField>
          </template>

          <template v-if="form.scope === 'idea'">
            <UFormField :label="t('projects.idea')" required>
              <UTextarea v-model="form.idea" :rows="3" autoresize class="w-full" :placeholder="t('projects.ideaPlaceholder')" autofocus />
            </UFormField>
            <div class="grid gap-3 sm:grid-cols-2">
              <UFormField :label="t('projects.stack')">
                <UInput v-model="form.stack" class="w-full" :placeholder="t('projects.stackPlaceholder')" />
              </UFormField>
              <UFormField :label="t('projects.cloneDir')" required>
                <UInput v-model="form.dir" class="w-full font-mono" placeholder="my-app" />
              </UFormField>
            </div>
            <UFormField :label="t('projects.cloneParent')" :help="ideaDest ? t('projects.ideaInto', { path: ideaDest }) : undefined" required>
              <FolderTree v-model="form.parent" />
            </UFormField>
          </template>

          <template v-if="form.scope === 'template'">
            <UFormField :label="t('projects.templatePick')" required>
              <USelect v-model="form.template" :items="packItems" :loading="!packData" class="w-full" />
              <template v-if="pickedPack" #help>{{ pickedPack.description }}</template>
            </UFormField>
            <UFormField v-if="pickedPack?.root" :label="t('projects.companyGoal')">
              <UTextarea v-model="form.goal" :rows="2" autoresize class="w-full" :placeholder="t('projects.companyGoalPlaceholder')" autofocus />
            </UFormField>
            <UFormField :label="t('projects.cloneDir')" required>
              <UInput v-model="form.dir" class="w-full font-mono" placeholder="acme" />
            </UFormField>
            <UFormField :label="t('projects.cloneParent')" :help="ideaDest ? t('projects.ideaInto', { path: ideaDest }) : undefined" required>
              <FolderTree v-model="form.parent" />
            </UFormField>
          </template>

          <UFormField :required="form.scope === 'machine'">
            <template #label><InfoLabel :label="t('projects.name')" :info="form.scope !== 'machine' ? t('projects.nameHelp') : undefined" /></template>
            <UInput v-model="form.name" class="w-full" :placeholder="form.scope === 'machine' ? t('projects.namePlaceholder') : ''" />
          </UFormField>

          <UAlert v-if="error" color="error" variant="subtle" :description="error" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="addOpen = false" />
          <UButton
            type="submit" form="project-form" :loading="adding"
            :label="form.scope !== 'clone' ? t('projects.add') : adding ? t('projects.cloning') : t('projects.cloneAction')"
            :disabled="!canAdd"
          />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
