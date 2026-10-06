<script setup lang="ts">
import type { WatchPane } from '~/composables/useWatchLayout'

// One box of the watch screen: a project's chat (the reusable ChatPanel), with
// its project, split right/below and close in the chat's own header.
const props = defineProps<{ pane: WatchPane, projects: Project[] }>()
const { t } = useLang()
const { split, close, setPane } = useWatchLayout()
const project = computed(() => props.projects.find(p => p.id === props.pane.projectId))
const projectMenu = computed(() => [props.projects.map(p => ({
  label: p.name,
  icon: p.id === props.pane.projectId ? 'i-lucide-check' : (p.scope === 'machine' ? 'i-lucide-monitor' : 'i-lucide-folder'),
  onSelect: () => { if (p.id !== props.pane.projectId) setPane(props.pane.id, { projectId: p.id, conversationId: undefined }) }
}))])
</script>

<template>
  <div class="flex flex-col">
    <ChatPanel
      v-if="project" :key="pane.projectId" :project-id="pane.projectId" :conversation-id="pane.conversationId" compact pane class="min-h-0 flex-1"
      @current="(id) => setPane(pane.id, { conversationId: id })"
    >
      <template #lead>
        <UDropdownMenu :items="projectMenu" :content="{ align: 'start' }" :ui="{ content: 'max-h-80 w-60' }">
          <UButton size="sm" color="neutral" variant="soft" icon="i-lucide-folder" :label="project.name" :title="t('watch.project')" class="max-w-32" :ui="{ label: 'truncate' }" />
        </UDropdownMenu>
      </template>
      <template #actions>
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-columns-2" :aria-label="t('watch.splitRight')" :title="t('watch.splitRight')" @click="split(pane.id, 'row')" />
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-rows-2" :aria-label="t('watch.splitDown')" :title="t('watch.splitDown')" @click="split(pane.id, 'col')" />
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('watch.close')" :title="t('watch.close')" @click="close(pane.id)" />
      </template>
    </ChatPanel>
    <!-- a fresh split, or its project was removed: pick a project or a recent chat -->
    <WatchPicker
      v-else :projects="projects" :hint="t('watch.pickProject')" class="h-full"
      @pick="(projectId, conversationId) => setPane(pane.id, { projectId, conversationId })"
    >
      <template #actions>
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('watch.close')" :title="t('watch.close')" @click="close(pane.id)" />
      </template>
    </WatchPicker>
  </div>
</template>
