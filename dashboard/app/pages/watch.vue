<script setup lang="ts">
// Watch screen (like iTerm): chats of any projects side by side, split right
// or below, each box kept in this browser.
const { t } = useLang()
const { root, count, add, clear } = useWatchLayout()
const { data: proj } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true, server: false })
const projects = computed(() => proj.value?.projects ?? [])
const addMenu = computed(() => [projects.value.length
  ? projects.value.map(p => ({ label: p.name, icon: p.scope === 'machine' ? 'i-lucide-monitor' : 'i-lucide-folder', onSelect: () => add(p.id) }))
  : [{ label: t('watch.noProjects'), disabled: true }]])
function reset() {
  if (confirm(t('watch.clearConfirm'))) clear()
}
</script>

<template>
  <UDashboardPanel :ui="{ body: 'p-2 sm:p-2 min-h-0 overflow-hidden' }">
    <template #header>
      <UDashboardNavbar :title="t('watch.title')" :ui="{ root: 'max-sm:px-3' }">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>
        <template #right>
          <UButton v-if="count" size="sm" color="neutral" variant="ghost" icon="i-lucide-eraser" :label="t('watch.clear')" @click="reset" />
          <UDropdownMenu :items="addMenu" :content="{ align: 'end' }" :ui="{ content: 'max-h-80 w-60' }">
            <UButton size="sm" icon="i-lucide-plus" :label="t('watch.add')" />
          </UDropdownMenu>
        </template>
      </UDashboardNavbar>
    </template>
    <template #body>
      <div v-if="!proj" class="grid h-full grid-cols-2 gap-2" aria-busy="true">
        <USkeleton class="h-full" /><USkeleton class="h-full" />
      </div>
      <WatchTree v-else-if="root" :node="root" :projects="projects" class="h-full" />
      <div v-else class="flex h-full items-start justify-center pt-[8vh]">
        <WatchPicker :projects="projects" :hint="t('watch.empty')" class="max-h-[70vh] w-full max-w-lg" @pick="add" />
      </div>
    </template>
  </UDashboardPanel>
</template>
