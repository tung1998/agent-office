<script setup lang="ts">
// Compact segmented switch for sub-sections, with an optional count and alert dot.
export interface SegmentItem { value: string, label: string, icon?: string, count?: number, alert?: boolean }
defineProps<{ items: SegmentItem[] }>()
const model = defineModel<string>({ required: true })
const { t } = useLang()
</script>

<template>
  <div class="inline-flex rounded-lg bg-(--ui-bg-elevated) p-0.5" role="tablist">
    <button
      v-for="it in items" :key="it.value" type="button" role="tab" :aria-selected="model === it.value"
      class="relative flex items-center gap-1.5 rounded-md px-2.5 py-1 text-sm transition"
      :class="model === it.value ? 'bg-(--ui-bg) font-medium shadow-sm' : 'text-(--ui-text-muted) hover:text-(--ui-text)'"
      @click="model = it.value"
    >
      <UIcon v-if="it.icon" :name="it.icon" class="size-4" />
      {{ it.label }}
      <span v-if="it.count !== undefined" class="text-xs tabular-nums text-(--ui-text-dimmed)">{{ it.count }}</span>
      <span v-if="it.alert" class="size-1.5 rounded-full bg-(--ui-error)" :aria-label="t('nav.alertLabel')" />
    </button>
  </div>
</template>
