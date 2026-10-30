<script setup lang="ts">
// An agent's AI connections as one list, tried top to bottom: the first is
// saved as provider_id + llm_model, the rest as fallback_provider_ids. An
// entry is a connection id, or "id|model": the same connection may come back
// with another model. "" is the office's default connection, first only.
const props = defineProps<{ providers: Provider[], tier?: ModelTier, disabled?: boolean }>()
const providerId = defineModel<string>('providerId', { required: true })
const llmModel = defineModel<string>('llmModel', { required: true })
const fallbacks = defineModel<string[]>('fallbacks', { required: true })
const { t } = useLang()

const split = (e: string) => { const [id = '', m = ''] = e.split('|'); return { id: id.trim(), model: m.trim() } }
const join = (id: string, m: string) => (m.trim() ? `${id}|${m.trim()}` : id)
const defaultProvider = computed(() => props.providers.find(p => p.is_default))
const providerOf = (id: string) => (id ? props.providers.find(p => p.id === id) : defaultProvider.value)
const mainId = computed(() => providerOf(providerId.value)?.id)
const entries = computed(() => [join(providerId.value, llmModel.value), ...fallbacks.value.filter(e => e !== mainId.value)])
const rows = computed(() => entries.value.map(split))
function set(l: string[]) {
  // the default connection only stands first; lower down it is named
  const [first = '', ...rest] = l
  const main = split(first)
  providerId.value = main.id
  llmModel.value = main.model
  fallbacks.value = rest.map((e) => { const r = split(e); return join(r.id || defaultProvider.value?.id || '', r.model) }).filter(e => e && e !== (providerOf(main.id)?.id ?? ''))
}
const nameOf = (id: string) => (id ? providerOf(id)?.name ?? id : t('org.form.providerDefault', { suffix: defaultProvider.value ? ` (${defaultProvider.value.name})` : '' }))
const options = computed(() => props.providers
  .filter(p => !entries.value.includes(p.id) && !(p.id === mainId.value && !llmModel.value))
  .map(p => ({ label: p.enabled ? p.name : `${p.name} (${t('org.form.fallbackOff')})`, value: p.id })))
// USelect keeps the picked value even with model-value undefined; once that
// connection leaves the options it shows its raw id, so remount it empty.
const addKey = ref(0)
const add = (id: string) => {
  if (id) set([...entries.value, id])
  addKey.value++
}
const setModel = (i: number, m: string) => set(entries.value.map((e, j) => (j === i ? join(split(e).id, m) : e)))
function up(i: number) {
  const l = [...entries.value]
  ;[l[i - 1], l[i]] = [l[i]!, l[i - 1]!]
  set(l)
}
const remove = (i: number) => set(entries.value.filter((_, j) => j !== i))
</script>

<template>
  <UFormField>
    <template #label><InfoLabel :label="t('org.form.connections')" :info="t('org.form.connectionsHelp')" /></template>
    <div class="space-y-1.5">
      <div v-for="(r, i) in rows" :key="i" class="flex items-center gap-2 rounded-md border border-(--ui-border) px-2 py-1 text-sm" :class="{ 'opacity-60': providerOf(r.id)?.enabled === false }">
        <span class="w-4 text-xs tabular-nums text-(--ui-text-muted)">{{ i + 1 }}</span>
        <span class="min-w-0 flex-1 truncate">
          {{ nameOf(r.id) }}
          <UBadge v-if="providerOf(r.id)?.enabled === false" :label="t('org.form.fallbackOff')" size="sm" color="neutral" variant="subtle" />
        </span>
        <ModelInput
          :model-value="r.model" :models="providerOf(r.id)?.models" size="xs" class="w-40" :disabled="disabled"
          :placeholder="i > 0 && providerOf(r.id)?.id === mainId && !r.model ? t('org.form.fallbackModelNeeded') : (tier && providerOf(r.id)?.tier_models[tier]) || t('org.form.fallbackModel')"
          @update:model-value="(v: string) => setModel(i, v)"
        />
        <template v-if="!disabled">
          <UButton v-if="i > 0" size="xs" color="neutral" variant="ghost" icon="i-lucide-arrow-up" :aria-label="t('org.form.fallbackUp')" @click="up(i)" />
          <UButton v-if="rows.length > 1 || r.id" size="xs" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('org.form.fallbackRemove')" @click="remove(i)" />
        </template>
      </div>
      <USelect
        v-if="!disabled && options.length" :key="addKey" :model-value="undefined" :items="options" :placeholder="t('org.form.connectionAdd')"
        icon="i-lucide-plus" size="sm" class="w-full sm:w-72" @update:model-value="(v: string) => add(v)"
      />
    </div>
  </UFormField>
</template>
