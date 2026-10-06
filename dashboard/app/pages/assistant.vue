<script setup lang="ts">
// The office assistant (ADR-046): one chat across projects — figures,
// reports, settings through approval cards, work handed to a project's chat.
const { t } = useLang()
const { isAdmin } = useAuth()
const { data, error } = await useLiveFetch<{ project_id: string }>('/api/assistant')
</script>

<template>
  <PageShell :title="t('assistant.title')">
    <template v-if="isAdmin && data?.project_id" #actions>
      <AssistantSettings :project-id="data.project_id" />
    </template>
    <div class="flex min-h-0 flex-1 flex-col gap-3">
      <p class="text-sm text-(--ui-text-muted)">{{ t('assistant.intro') }}</p>
      <ChatPanel v-if="data?.project_id" :project-id="data.project_id" />
      <UAlert v-else-if="error" color="warning" variant="subtle" :title="t('assistant.missing')" />
    </div>
  </PageShell>
</template>
