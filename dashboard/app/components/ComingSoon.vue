<script setup lang="ts">
const { t } = useLang()
defineProps<{ title: string, icon: string, milestone: string, description: string }>()
</script>

<template>
  <PageShell :title="title">
    <div class="flex flex-col items-center justify-center gap-3 py-24 text-center">
      <UIcon :name="icon" class="size-10 text-(--ui-text-dimmed)" />
      <h2 class="text-lg font-semibold">{{ title }}</h2>
      <p class="max-w-md text-sm text-(--ui-text-muted)">{{ description }}</p>
      <UBadge :label="t('soon.badge', { milestone })" variant="subtle" color="neutral" />
    </div>
  </PageShell>
</template>
