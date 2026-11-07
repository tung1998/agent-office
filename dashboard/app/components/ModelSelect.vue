<script setup lang="ts">
// A chat's own model for its agent (ADR-267): "" = the agent's own, named by
// fallback; options are the models the agent's connection offers.
const props = defineProps<{ fallback?: string, size?: 'xs' | 'sm' | 'md', disabled?: boolean, options: readonly string[] }>()
const model = defineModel<string>({ default: '' })
const { t } = useLang()
const DEFAULT = '__default'
const value = computed({ get: () => model.value || DEFAULT, set: (v: string) => { model.value = v === DEFAULT ? '' : v } })
const items = computed(() => [
  { label: props.fallback ? t('scoring.modelDefault', { model: props.fallback }) : t('scoring.modelDefaultNone'), value: DEFAULT },
  ...props.options.map(m => ({ label: m, value: m }))
])
</script>

<template>
  <USelect v-model="value" :items="items" :size="size" :disabled="disabled" icon="i-lucide-cpu" class="min-w-32" />
</template>
