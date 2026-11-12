<script setup lang="ts">
// Shipped workflows are immutable code templates; custom library workflows
// remain editable files. Installing either creates a project-owned copy.
const toast = useToast()
const { t } = useLang()
const { isAdmin } = useAuth()
const { data, refresh, pending } = useLiveFetch<{ workflows: LibraryWorkflow[] }>('/api/workflow-library', { lazy: true })
const { data: projectsData } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const projects = computed(() => projectsData.value?.projects ?? [])
const search = ref('')
const category = ref('all')
const featuredWorkflowKeys = new Set(['review-pr', 'bugfix', 'incident-response', 'product-discovery', 'security-assessment', 'launch-readiness', 'write-content'])
const allWorkflows = computed(() => [...(data.value?.workflows ?? [])].sort((a, b) =>
  Number(featuredWorkflowKeys.has(b.def.key)) - Number(featuredWorkflowKeys.has(a.def.key))
  || Number(b.builtin) - Number(a.builtin)
  || a.def.key.localeCompare(b.def.key)))
const categories = computed(() => ['all', ...new Set(allWorkflows.value.map(w => w.def.category?.trim() || t('wf.otherCategory')))])
const categoryItems = computed(() => categories.value.map(value => ({
  label: value === 'all' ? t('wf.allCategories') : value,
  value
})))
const list = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return allWorkflows.value.filter((w) => {
    const workflowCategory = w.def.category?.trim() || t('wf.otherCategory')
    const matchesCategory = category.value === 'all' || workflowCategory === category.value
    const searchable = [w.def.name, w.def.key, w.def.description, workflowCategory, ...(w.def.roles ?? []).flatMap(r => [r.name, r.key])].join(' ').toLocaleLowerCase()
    return matchesCategory && (!query || searchable.includes(query))
  })
})
const hasFilters = computed(() => !!search.value.trim() || category.value !== 'all')

const editTo = (key?: string) => ({ path: '/workflows/edit', query: key ? { key } : {} })
const installOpen = ref(false)
const installWorkflow = ref<LibraryWorkflow | null>(null)
const installProject = ref('')
const installing = ref(false)
const projectItems = computed(() => projects.value.map(p => ({ label: p.name, value: p.id })))
function chooseProject(w: LibraryWorkflow) {
  installWorkflow.value = w
  installProject.value = ''
  installOpen.value = true
}
async function install() {
  if (!installWorkflow.value || !installProject.value) return
  installing.value = true
  try {
    await $fetch(`/api/projects/${installProject.value}/workflows`, { method: 'POST', body: { key: installWorkflow.value.def.key } })
    toast.add({ title: t('wf.installed'), color: 'success' })
    installOpen.value = false
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    installing.value = false
  }
}
async function remove(w: LibraryWorkflow) {
  if (!confirm(t('wf.deleteConfirm', { name: w.def.name || w.def.key }))) return
  try {
    await $fetch(`/api/workflow-library/${w.def.key}`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-3 rounded-xl border border-(--ui-border) p-4">
      <div class="me-auto min-w-48">
        <p class="flex items-center gap-2 text-sm text-(--ui-text-muted)">
          {{ t(hasFilters ? 'wf.countFiltered' : 'wf.count', hasFilters ? { shown: list.length, total: allWorkflows.length } : { n: allWorkflows.length }) }}
          <InfoTip :text="t('wf.libraryInfo')" icon-class="size-4 text-(--ui-text-muted)" />
        </p>
      </div>
      <UInput v-model="search" icon="i-lucide-search" :placeholder="t('wf.search')" class="w-full sm:w-60" />
      <USelect v-model="category" :items="categoryItems" class="w-full sm:w-44" :aria-label="t('wf.allCategories')" />
      <UButton class="sm:ms-auto" icon="i-lucide-plus" :label="t('wf.new')" :to="editTo()" />
    </div>
    <LoadingRows v-if="pending && !data" />
    <div v-else-if="!list.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
      <UIcon name="i-lucide-workflow" class="mx-auto size-8 text-(--ui-text-dimmed)" />
      <p class="mt-2 font-medium">{{ allWorkflows.length ? t('wf.searchEmpty') : t('wf.libraryEmpty') }}</p>
      <UButton v-if="hasFilters" class="mt-3" size="sm" color="neutral" variant="ghost" :label="t('wf.clearSearch')" @click="search = ''; category = 'all'" />
    </div>
    <div v-else class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <div v-for="w in list" :key="w.def.key" class="flex min-w-0 flex-col rounded-xl border border-(--ui-border) p-4">
        <div class="flex items-start gap-3">
          <UIcon name="i-lucide-workflow" class="mt-0.5 size-5 shrink-0 text-primary" />
          <div class="min-w-0 flex-1">
            <p class="flex flex-wrap items-center gap-1.5 font-medium">
              <UIcon v-if="featuredWorkflowKeys.has(w.def.key)" name="i-lucide-star" class="size-3.5 text-(--ui-warning)" :title="t('wf.featured')" />
              <NuxtLink v-if="!w.builtin" :to="editTo(w.def.key)" class="hover:text-primary">{{ w.def.name || w.def.key }}</NuxtLink>
              <span v-else>{{ w.def.name || w.def.key }}</span>
            </p>
            <p class="mt-1 truncate font-mono text-xs text-(--ui-text-muted)">/{{ w.def.key }}</p>
            <p class="mt-1 text-xs text-(--ui-text-muted)">{{ w.def.category?.trim() || t('wf.otherCategory') }}</p>
          </div>
        </div>
        <p v-if="w.error" class="mt-3 line-clamp-2 text-sm text-(--ui-error)">{{ w.error }}</p>
        <p v-else class="mt-3 line-clamp-3 flex-1 text-sm text-(--ui-text-muted)">{{ w.def.description }}</p>
        <div class="mt-4 flex flex-wrap items-center gap-2">
          <UBadge v-if="w.builtin" color="neutral" variant="outline" size="sm" :label="t('wf.builtin')" />
          <UBadge v-if="w.error" color="error" variant="subtle" size="sm" icon="i-lucide-circle-alert" :label="t('wf.invalid')" :title="w.error" />
          <span v-if="w.def.roles?.length" class="ms-auto flex items-center gap-1 text-xs text-(--ui-text-muted)" :title="w.def.roles.map(r => r.name).join(', ')">
            <UIcon v-for="r in w.def.roles" :key="r.key" :name="accessIcon[accessKey(r.access)]" class="size-3.5" />
          </span>
        </div>
        <div class="mt-4 flex flex-wrap items-center gap-2 border-t border-(--ui-border) pt-3">
          <UButton v-if="isAdmin" size="sm" icon="i-lucide-plus" :label="t('wf.addToProject')" :disabled="!!w.error" @click="chooseProject(w)" />
          <UButton v-if="!w.builtin" size="sm" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('wf.edit')" :to="editTo(w.def.key)" />
          <UButton v-if="isAdmin && !w.builtin" size="sm" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('wf.delete')" :title="t('wf.delete')" @click="remove(w)" />
        </div>
      </div>
    </div>
  </div>
  <UModal v-model:open="installOpen" :title="t('wf.addToProjectTitle')" :description="installWorkflow?.def.name || installWorkflow?.def.key">
    <template #body>
      <UFormField :label="t('wf.pickProject')" required>
        <USelectMenu v-model="installProject" :items="projectItems" value-key="value" :placeholder="t('wf.pickProject')" :aria-label="t('wf.pickProject')" class="w-full" />
      </UFormField>
      <p v-if="!projects.length" class="mt-3 text-sm text-(--ui-text-muted)">{{ t('wf.noProjects') }}</p>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="installOpen = false" />
        <UButton color="primary" :label="t('wf.addToProjectAction')" :loading="installing" :disabled="!installProject" @click="install" />
      </div>
    </template>
  </UModal>
</template>
