<script setup lang="ts">
// How hard the model thinks: low … max (Ultra). "" = the default, named by
// fallback (an agent's: the CLI's own; a chat's: its agent's level).
const props = defineProps<{ fallback?: string, size?: 'xs' | 'sm' | 'md', disabled?: boolean }>()
const model = defineModel<string>({ default: '' })
const { t } = useLang()
// a Select item cannot have "" as its value: the default is a sentinel
const DEFAULT = '__default'
const value = computed({ get: () => model.value || DEFAULT, set: (v: string) => { model.value = v === DEFAULT ? '' : v } })
const items = computed(() => [
  { label: props.fallback ? t('effort.inherit', { level: effortLabel(props.fallback, t) }) : t('effort.default'), value: DEFAULT },
  ...EFFORTS.map(e => ({ label: effortLabel(e, t), value: e }))
])
</script>

<template>
  <USelect v-model="value" :items="items" :size="size" :disabled="disabled" icon="i-lucide-brain" class="min-w-32" />
</template>
