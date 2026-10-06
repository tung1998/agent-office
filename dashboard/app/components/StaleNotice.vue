<script setup lang="ts">
// A draft whose data changed on the server while it was being edited (ADR-072).
defineProps<{ show: boolean }>()
const emit = defineEmits<{ reload: [] }>()
const { t } = useLang()
</script>

<template>
  <div v-if="show" class="flex items-center gap-2 rounded-lg border border-(--ui-warning)/40 bg-(--ui-warning)/5 px-3 py-2 text-sm">
    <UIcon name="i-lucide-refresh-ccw-dot" class="size-4 shrink-0 text-(--ui-warning)" />
    <span class="min-w-0 flex-1">{{ t('draft.stale') }}</span>
    <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-rotate-ccw" :label="t('draft.reload')" @click="emit('reload')" />
  </div>
</template>
