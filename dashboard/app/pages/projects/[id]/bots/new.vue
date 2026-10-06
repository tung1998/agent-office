<script setup lang="ts">
// Connecting a new bot (ADR-049).
const route = useRoute()
const { t } = useLang()
const projectId = computed(() => route.params.id as string)
</script>

<template>
  <PageShell :title="t('bot.new')">
    <UButton :to="{ path: `/projects/${projectId}`, query: { tab: 'automations' } }" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2 mb-2" :label="t('auto.back')" />
    <BotSetup :project-id="projectId" bot-id="new" />
  </PageShell>
</template>
