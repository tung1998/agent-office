<script setup lang="ts">
// An agent's fallback connections, in the order they are tried; the main
// connection (mainId) is not one of them.
const props = defineProps<{ providers: Provider[], mainId?: string, disabled?: boolean }>()
const model = defineModel<string[]>({ required: true })
const { t } = useLang()

const name = (id: string) => props.providers.find(p => p.id === id)?.name ?? id
const shown = computed(() => model.value.filter(id => id !== props.mainId))
const options = computed(() => props.providers
  .filter(p => p.id !== props.mainId && !model.value.includes(p.id))
  .map(p => ({ label: p.name, value: p.id })))
const add = (id: string) => { if (id) model.value = [...shown.value, id] }
function up(i: number) {
  const l = [...shown.value]
  ;[l[i - 1], l[i]] = [l[i]!, l[i - 1]!]
  model.value = l
}
const remove = (i: number) => { model.value = shown.value.filter((_, j) => j !== i) }
</script>

<template>
  <UFormField :label="t('org.form.fallbacks')" :help="t('org.form.fallbacksHelp')">
    <div class="space-y-1.5">
      <div v-for="(id, i) in shown" :key="id" class="flex items-center gap-2 rounded-md border border-(--ui-border) px-2 py-1 text-sm">
        <span class="w-4 text-xs tabular-nums text-(--ui-text-muted)">{{ i + 1 }}</span>
        <span class="min-w-0 flex-1 truncate">{{ name(id) }}</span>
        <template v-if="!disabled">
          <UButton v-if="i > 0" size="xs" color="neutral" variant="ghost" icon="i-lucide-arrow-up" :aria-label="t('org.form.fallbackUp')" @click="up(i)" />
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('org.form.fallbackRemove')" @click="remove(i)" />
        </template>
      </div>
      <USelect
        v-if="!disabled && options.length" :model-value="undefined" :items="options" :placeholder="t('org.form.fallbackAdd')"
        icon="i-lucide-plus" size="sm" class="w-full sm:w-72" @update:model-value="(v: string) => add(v)"
      />
    </div>
  </UFormField>
</template>
