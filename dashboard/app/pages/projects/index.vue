<script setup lang="ts">
const { isAdmin } = useAuth()
const { t } = useLang()
const _f1 = useLiveFetch<{ projects: Project[] }>('/api/projects')
const { data, refresh } = _f1
const _f2 = useLiveFetch<{ templates: OrgModel[] }>('/api/templates')
const { data: tplData } = _f2
const _f3 = useLiveFetch<{ mode: string, home_dir: string, project_root?: string, clone_root?: string }>('/api/system')
const { data: sys } = _f3
await Promise.all([_f1, _f2, _f3]) // started together: one round trip, not 3 (a phone over a VPN)
const projects = computed(() => data.value?.projects ?? [])
const templates = computed(() => tplData.value?.templates ?? [])

const addOpen = ref(false)
type Scope = 'folder' | 'clone' | 'machine'
// clone: a pasted git link, cloned into parent/dir (dir from the link when empty)
const form = reactive({ scope: 'folder' as Scope, path: '', name: '', template_id: '', url: '', dir: '', parent: '' })
const error = ref('')
const adding = ref(false)
const urlDir = computed(() => form.url.trim().replace(/\/+$/, '').split(/[/:]/).pop()?.replace(/\.git$/, '') ?? '')
const cloneDest = computed(() => {
  const dir = form.dir.trim() || urlDir.value
  return form.parent && dir ? `${form.parent.replace(/\/+$/, '')}/${dir}` : ''
})
const canAdd = computed(() => form.scope === 'folder' ? !!form.path : form.scope === 'clone' ? !!form.url.trim() && !!form.parent : !!form.name)

function openAdd(path = '', name = '') {
  Object.assign(form, { scope: 'folder', path, name, template_id: '__ai', url: '', dir: '', parent: sys.value?.clone_root ?? '' })
  error.value = ''
  addOpen.value = true
}

// folders Claude Code has opened on this machine that office does not manage yet
// stop managing one (its folder stays): it comes back under "found on the machine"
const toast = useToast()
async function remove(p: Project) {
  if (!confirm(t('project.confirmUnmanage', { name: p.name }))) return
  try {
    await $fetch(`/api/projects/${p.id}`, { method: 'DELETE' })
    await refresh()
    scanMachine()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
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
onMounted(() => { if (isAdmin.value) scanMachine() })

async function add() {
  adding.value = true
  error.value = ''
  try {
    const template_id = form.template_id === '__ai' ? '' : form.template_id
    const res = form.scope === 'clone'
      ? await $fetch<{ project: Project }>('/api/projects/clone', {
          method: 'POST',
          body: { url: form.url.trim(), parent: form.parent, dir: form.dir.trim(), name: form.name, template_id }
        })
      : await $fetch<{ project: Project }>('/api/projects', {
          method: 'POST',
          body: { path: form.scope === 'folder' ? form.path : '', name: form.name, template_id }
        })
    addOpen.value = false
    await navigateTo(form.template_id === '__ai' ? `/projects/${res.project.id}/setup` : `/projects/${res.project.id}`)
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
      <UButton v-if="isAdmin" icon="i-lucide-folder-plus" :label="t('projects.add')" @click="openAdd()" />
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
        <UButton v-if="isAdmin" class="mt-4" icon="i-lucide-folder-plus" :label="t('projects.add')" @click="openAdd()" />
      </div>

      <div class="grid gap-3">
        <NuxtLink
          v-for="p in projects" :key="p.id" :to="`/projects/${p.id}`"
          class="flex flex-wrap items-center gap-4 rounded-lg border border-(--ui-border) p-4 transition hover:border-(--ui-primary)"
        >
          <UIcon :name="p.scope === 'machine' ? 'i-lucide-monitor' : 'i-lucide-folder-git-2'" class="size-6 text-primary" />
          <div class="min-w-0 flex-1">
            <p class="font-semibold">{{ p.name }}</p>
            <p v-if="p.scope === 'folder'" class="truncate font-mono text-xs text-(--ui-text-muted)">{{ p.path }}</p>
            <p v-else class="text-xs text-(--ui-text-muted)">{{ t('projects.machineHelper') }}</p>
          </div>
          <UBadge v-if="!p.exists" :label="t('projects.notFound')" color="error" variant="subtle" />
          <div v-if="p.model" class="flex items-center gap-2 text-sm">
            <UIcon :name="kindIcon[p.model.kind]" class="size-4" />
            {{ p.model.name }} · {{ t('projects.agentCount', { n: p.model.agent_count }) }}
          </div>
          <UBadge v-else :label="t('projects.noModel')" color="warning" variant="subtle" />
          <UButton
            v-if="isAdmin" size="xs" color="neutral" variant="ghost" icon="i-lucide-trash-2" class="hover:text-(--ui-error)"
            :aria-label="t('project.unmanage')" :title="t('project.unmanage')" @click.prevent.stop="remove(p)"
          />
          <UIcon name="i-lucide-chevron-right" class="size-4 text-(--ui-text-dimmed)" />
        </NuxtLink>
      </div>

      <section v-if="isAdmin && (found.length || scanning)" class="space-y-2">
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
            <div class="grid gap-2 sm:grid-cols-3">
              <button
                v-for="opt in [
                  { value: 'folder', icon: 'i-lucide-folder-git-2', title: t('projects.scopeFolderTitle'), text: t('projects.scopeFolderText') },
                  { value: 'clone', icon: 'i-lucide-git-branch-plus', title: t('projects.scopeCloneTitle'), text: t('projects.scopeCloneText') },
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
              <UFormField :label="t('projects.cloneUrl')" :help="t('projects.cloneUrlHelp')" required>
                <UInput v-model="form.url" class="w-full font-mono" icon="i-lucide-link" placeholder="https://github.com/org/repo.git" autofocus />
              </UFormField>
              <UFormField :label="t('projects.cloneDir')">
                <UInput v-model="form.dir" class="w-full font-mono" :placeholder="urlDir" />
              </UFormField>
            </div>
            <UFormField :label="t('projects.cloneParent')" :help="cloneDest ? t('projects.cloneInto', { path: cloneDest }) : undefined" required>
              <FolderTree v-model="form.parent" />
            </UFormField>
          </template>

          <UFormField
            :label="t('projects.name')" :required="form.scope === 'machine'"
            :help="form.scope !== 'machine' ? t('projects.nameHelp') : undefined"
          >
            <UInput v-model="form.name" class="w-full" :placeholder="form.scope === 'machine' ? t('projects.namePlaceholder') : ''" />
          </UFormField>

          <UFormField :label="t('projects.orgModel')">
            <TemplatePicker v-model="form.template_id" :templates="templates" allow-ai />
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
