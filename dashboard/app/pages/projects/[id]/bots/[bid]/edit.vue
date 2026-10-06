<script setup lang="ts">
// A bot's setup: the bot and its commands (ADR-049).
const route = useRoute()
const { t } = useLang()
const projectId = computed(() => route.params.id as string)
const botId = computed(() => route.params.bid as string)
</script>

<template>
  <PageShell :title="botId === 'new' ? t('bot.new') : t('bot.title')">
    <UButton :to="{ path: `/projects/${projectId}`, query: { tab: 'automations' } }" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2 mb-2" :label="t('auto.back')" />
    <BotSetup :key="botId" :project-id="projectId" :bot-id="botId" />
  </PageShell>
</template>
