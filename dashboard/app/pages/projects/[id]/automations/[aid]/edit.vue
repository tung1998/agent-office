<script setup lang="ts">
const route = useRoute()
const { t } = useLang()
const projectId = computed(() => route.params.id as string)
const { data } = await useLiveFetch<{ automation: Automation }>(() => `/api/automations/${route.params.aid}`)
// a bot's command is edited with its bot (ADR-049)
const a = data.value?.automation
if (a && isChannelSource(a.source) && a.config.channel_id) await navigateTo(`/projects/${projectId.value}/bots/${a.config.channel_id}/edit`, { replace: true })
</script>

<template>
  <PageShell :title="data?.automation.name ?? t('auto.edit')">
    <UButton :to="`/projects/${projectId}/automations/${route.params.aid}`" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2 mb-2" :label="data?.automation.name ?? t('auto.back')" />
    <AutomationBuilder v-if="data" :key="data.automation.id" :project-id="projectId" :automation="data.automation" />
  </PageShell>
</template>
