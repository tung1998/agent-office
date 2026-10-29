<script setup lang="ts">
// The agent library (ADR-153, ADR-154): the list holds names only; an agent's
// instructions are read when it is opened. Picking one fills the new agent's
// form (the parent's button, or a double click); it can also just be read as
// a model for writing one's own.
// tall: a page of its own (more rows than in a dialog)
const props = defineProps<{ tall?: boolean }>()
const emit = defineEmits<{ pick: [a: LibraryAgent] }>()
const selected = defineModel<LibraryAgent>('selected')
const { t } = useLang()
const toast = useToast()

const { data, error, refresh } = useLiveFetch<{ agents: LibraryEntry[] }>('/api/agent-library', { lazy: true })
// featured: the ones written for office
const filter = ref<LibraryCategory | 'featured' | ''>('featured')
const query = ref('')

const filters = computed(() => [
  { value: 'featured' as const, label: t('team.lib.featured') },
  { value: '' as const, label: t('team.lib.all') },
  ...(['code', 'design', 'plan', 'customer', 'content'] as const).map(c => ({ value: c, label: t(`team.lib.cat.${c}`) }))
])
const fold = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()
const shown = computed(() => {
  const q = fold(query.value.trim())
  const f = q ? '' : filter.value // a search looks everywhere
  return (data.value?.agents ?? []).filter(a => (!f || (f === 'featured' ? a.featured : a.category === f))
    && (!q || fold(`${a.name} ${a.role} ${a.description} ${a.key}`).includes(q)))
})
const icon = (c: LibraryCategory) => ({ code: 'i-lucide-code', design: 'i-lucide-palette', plan: 'i-lucide-list-checks', customer: 'i-lucide-headset', content: 'i-lucide-pen-line' })[c]

// the instructions are read on open; the last click wins
const opening = ref('')
async function open(e: LibraryEntry, use = false) {
  if (selected.value?.id === e.id) { // the open one: a click closes it, a double click uses it
    if (use) emit('pick', selected.value)
    else selected.value = undefined
    return
  }
  opening.value = e.id
  try {
    const res = await $fetch<{ agent: LibraryAgent }>(`/api/agent-library/${e.id}`)
    if (opening.value !== e.id) return
    selected.value = res.agent
    if (use) emit('pick', res.agent)
  } catch (err) {
    toast.add({ title: apiError(err), color: 'error' })
  } finally {
    if (opening.value === e.id) opening.value = ''
  }
}
</script>

<template>
  <div class="space-y-3">
    <div class="flex flex-wrap items-center gap-2">
      <div class="flex flex-wrap gap-1">
        <UButton
          v-for="c in filters" :key="c.value" size="xs" :label="c.label"
          :color="filter === c.value && !query ? 'primary' : 'neutral'" :variant="filter === c.value && !query ? 'subtle' : 'ghost'"
          @click="filter = c.value; query = ''"
        />
      </div>
      <UInput v-model="query" icon="i-lucide-search" size="sm" class="ms-auto w-full sm:w-48" :placeholder="t('team.lib.search')" />
    </div>

    <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)">
      <template #actions>
        <UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="() => refresh()" />
      </template>
    </UAlert>
    <LoadingRows v-else-if="!data" :n="4" />
    <p v-else-if="!shown.length" class="py-4 text-center text-sm text-(--ui-text-muted)">{{ t('team.lib.none') }}</p>

    <template v-else>
      <p class="text-xs text-(--ui-text-muted)">{{ t('team.lib.count', { n: shown.length }) }}</p>
      <div class="grid gap-2 overflow-y-auto sm:grid-cols-2" :class="selected ? (props.tall ? 'max-h-72' : 'max-h-48') : (props.tall ? 'max-h-[65vh]' : 'max-h-96')">
        <button
          v-for="a in shown" :key="a.id" type="button"
          class="flex items-start gap-2.5 rounded-lg border p-2.5 text-left transition"
          :class="selected?.id === a.id ? 'border-(--ui-primary) bg-(--ui-primary)/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
          @click="open(a)" @dblclick="open(a, true)"
        >
          <UIcon v-if="opening === a.id" name="i-lucide-loader-circle" class="mt-0.5 size-4 shrink-0 animate-spin text-primary" />
          <span v-else-if="a.emoji" class="mt-px w-4 shrink-0 text-center text-sm leading-5">{{ a.emoji }}</span>
          <UIcon v-else :name="icon(a.category)" class="mt-0.5 size-4 shrink-0 text-primary" />
          <div class="min-w-0">
            <p class="flex items-center gap-1 text-sm font-medium">
              <UTooltip v-if="a.featured" :text="t('team.lib.featured')">
                <UIcon name="i-lucide-star" class="size-3.5 shrink-0 text-(--ui-warning)" />
              </UTooltip>
              <span class="break-words">{{ a.name }}</span>
            </p>
            <p class="line-clamp-2 text-xs text-(--ui-text-muted)">{{ a.role || a.description }}</p>
          </div>
        </button>
      </div>
    </template>

    <!-- the opened one, in full: to use or to read -->
    <div v-if="selected" class="space-y-2 rounded-lg border border-(--ui-border) p-3">
      <div class="flex flex-wrap items-center gap-1.5">
        <p class="me-auto font-medium">{{ selected.emoji }} {{ selected.name }}</p>
        <UBadge :label="modelTierLabel[selected.model_tier]" color="neutral" variant="subtle" size="sm" />
        <UBadge :label="permOf(agentLevel(selected.permissions)).label" :icon="permOf(agentLevel(selected.permissions)).icon" color="neutral" variant="subtle" size="sm" />
        <UTooltip :text="t('common.close')">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('common.close')" @click="selected = undefined" />
        </UTooltip>
      </div>
      <p class="text-sm text-(--ui-text-toned)">{{ selected.description }}</p>
      <pre class="max-h-56 overflow-y-auto whitespace-pre-wrap rounded bg-(--ui-bg-elevated) p-2 text-xs">{{ selected.instructions }}</pre>
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-(--ui-text-muted)">
        <span>{{ t('team.lib.words', { n: selected.words }) }}</span>
        <span v-if="selected.set !== 'office'" class="inline-flex items-center gap-1 text-(--ui-warning)">
          <UIcon name="i-lucide-info" class="size-3.5" />{{ t('team.lib.agencyNote') }}
        </span>
        <ULink v-if="selected.source" :to="selected.source" target="_blank" class="inline-flex items-center gap-1">
          <UIcon name="i-lucide-external-link" class="size-3.5" />{{ t('team.lib.source') }}
        </ULink>
      </div>
      <slot name="actions" :agent="selected" />
    </div>
  </div>
</template>
