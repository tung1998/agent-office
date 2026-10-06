<script setup lang="ts">
// One filter icon (with how many filters are on); the filters open under it.
defineProps<{ active?: number }>()
const { t } = useLang()
</script>

<template>
  <UPopover :content="{ align: 'end' }">
    <!-- how many filters are on: a badge on the icon -->
    <UChip :text="active" :show="!!active" size="3xl" color="primary" inset class="shrink-0">
      <UButton size="sm" color="neutral" :variant="active ? 'soft' : 'ghost'" icon="i-lucide-list-filter" :aria-label="t('filter.title')" />
    </UChip>
    <template #content>
      <div class="w-64 space-y-2 p-3">
        <p class="text-xs font-medium text-(--ui-text-muted)">{{ t('filter.title') }}</p>
        <slot />
      </div>
    </template>
  </UPopover>
</template>
