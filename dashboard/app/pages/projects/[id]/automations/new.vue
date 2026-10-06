<script setup lang="ts">
const route = useRoute()
const { t } = useLang()
const projectId = computed(() => route.params.id as string)
// ?preset=pr: the Review Pull Request template
const preset = computed(() => route.query.preset === 'pr' ? prReviewDraft(t) : undefined)
</script>

<template>
  <PageShell :title="t('auto.new')">
    <UButton :to="{ path: `/projects/${projectId}`, query: { tab: 'automations' } }" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2 mb-2" :label="t('auto.back')" />
    <div class="mb-3 flex flex-wrap items-center gap-2 text-sm">
      <span class="text-(--ui-text-muted)">{{ t('auto.templates') }}</span>
      <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-git-pull-request" :label="t('auto.prName')" :to="{ query: { preset: 'pr' } }" />
    </div>
    <AutomationBuilder :key="String(route.query.preset ?? '')" :project-id="projectId" :preset="preset" />
  </PageShell>
</template>
