<script setup lang="ts">
// Skills and MCP servers kept by office, plus what is installed machine-wide.
// Installing into a project is done from the project's "Skills & MCP" tab.
const { t } = useLang()
const kind = ref<ItemKind>(useRoute().query.kind === 'mcp' ? 'mcp' : 'skill') // ?kind=mcp: the MCP tab (office's gateway)
const tabs = computed(() => [
  { label: t('tools.skillsTab'), value: 'skill', icon: 'i-lucide-sparkles' },
  { label: t('tools.mcpTab'), value: 'mcp', icon: 'i-lucide-plug-zap' }
])
</script>

<template>
  <PageShell :title="t('tools.libraryPageTitle')">
    <div class="space-y-4">
      <UTabs
        v-model="kind" :content="false" class="w-fit"
        :items="tabs"
      />
      <ToolsPanel :key="kind" :kind="kind" />
    </div>
  </PageShell>
</template>
