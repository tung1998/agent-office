<script setup lang="ts">
// A chat's tags as small chips; removable: an ✕ on each.
defineProps<{ tags: string[], removable?: boolean, size?: 'xs' | 'sm' }>()
const emit = defineEmits<{ remove: [string] }>()
const { t } = useLang()
</script>

<template>
  <UBadge
    v-for="tag in tags" :key="tag" :label="tag" :color="tagColor(tag)" variant="subtle" :size="size ?? 'sm'"
    class="max-w-40" :ui="{ label: 'truncate' }"
  >
    <template v-if="removable" #trailing>
      <button type="button" class="-me-0.5 rounded-sm opacity-60 hover:opacity-100" :aria-label="t('chatTag.remove', { name: tag })" @click.stop="emit('remove', tag)">
        <UIcon name="i-lucide-x" class="size-3" />
      </button>
    </template>
  </UBadge>
</template>
