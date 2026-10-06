<script setup lang="ts">
// Filters a list by where its items came from ('all' = every source): a row
// of small icons, the name on hover.
const model = defineModel<ChatFilter>({ default: 'all' })
const props = defineProps<{ burn?: boolean }>()
const { t } = useLang()
const items = computed(() => [
  { value: 'all' as const, icon: 'i-lucide-layers', label: t('source.all') },
  ...sources.map(s => ({ value: s as ChatFilter, icon: sourceIcon[s], label: t(`source.${s}`) })),
  ...(props.burn ? [{ value: 'burn' as const, icon: 'i-lucide-flame', label: t('burn.title') }] : [])
])
</script>

<template>
  <div class="flex items-center gap-0.5 rounded-md bg-(--ui-bg-elevated) p-0.5" role="radiogroup" :aria-label="t('source.filter')">
    <UTooltip v-for="i in items" :key="i.value" :text="i.label">
      <button
        type="button" role="radio" :aria-checked="model === i.value" :aria-label="i.label"
        class="flex flex-1 items-center justify-center rounded px-1.5 py-1"
        :class="model === i.value ? 'bg-(--ui-bg) text-(--ui-text) shadow-sm' : 'text-(--ui-text-muted) hover:text-(--ui-text)'"
        @click="model = i.value"
      >
        <UIcon :name="i.icon" class="size-3.5" />
      </button>
    </UTooltip>
  </div>
</template>
