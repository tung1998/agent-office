<script setup lang="ts">
// What failed, grouped by cause (ADR-253): the same error seen many times is
// one row, with how often, who, the last time and its latest full text; a
// sample job opens in place.
interface FailureGroup {
  signature: string, category: string, count: number, jobs: number, calls: number,
  agent_names: string[], providers: string[], project_names: string[],
  first: string, last: string, sample: string, job_ids: string[]
}
const props = defineProps<{ project?: string, since?: string }>()
const open = defineModel<boolean>('open', { default: false })
const { t, when } = useLang()

const since = ref(props.since ?? '24h')
const category = ref('all') // a select item cannot be ''
const data = ref<{ groups: FailureGroup[], total: number, categories: Record<string, number> } | null>(null)
const error = ref<unknown>(null)
let token = 0
async function load() {
  const my = ++token
  data.value = null
  error.value = null
  const q = new URLSearchParams({ since: since.value })
  if (props.project) q.set('project', props.project)
  if (category.value !== 'all') q.set('category', category.value)
  try {
    const res = await $fetch<{ groups: FailureGroup[], total: number, categories: Record<string, number> }>(`/api/jobs/failures?${q}`)
    if (my === token) data.value = res
  } catch (e) {
    if (my === token) error.value = e
  }
}
watch([open, since, category, () => props.project], ([o]) => { if (o) load() }, { immediate: true })

const sinceItems = computed(() => ['24h', '168h', '720h'].map(v => ({ value: v, label: t(`fail.since.${v}` as 'fail.since.24h') })))
const catName = (c: string) => t(`fail.cat.${c}` as 'fail.cat.other')
// every category seen in the range, the biggest first; "all" leads
const catItems = computed(() => {
  const cats = Object.entries(data.value?.categories ?? {}).sort((a, b) => b[1] - a[1])
  return [{ value: 'all', label: t('fail.allCats') }, ...cats.map(([c, n]) => ({ value: c, label: `${catName(c)} (${n})` }))]
})
// faults a person must fix on the machine are red; limits that pass, amber
const catColor = (c: string) => ['cli_broken', 'needs_login', 'config'].includes(c)
  ? 'error' as const
  : ['quota', 'rate_limit', 'budget'].includes(c) ? 'warning' as const : 'neutral' as const
const expanded = ref<string | null>(null)
const detail = ref<string | null>(null)
</script>

<template>
  <UModal v-model:open="open" :title="t('fail.title')" :ui="{ content: 'max-w-3xl' }">
    <template #body>
      <div class="space-y-3 text-sm">
        <div class="flex flex-wrap items-center gap-2">
          <USelect v-model="since" size="sm" :items="sinceItems" class="w-36" />
          <USelect v-model="category" size="sm" :items="catItems" class="w-48" :disabled="!data && category === 'all'" />
          <span v-if="data" class="text-xs text-(--ui-text-muted)">{{ t('fail.total', { n: data.total }) }}</span>
          <InfoTip :text="t('fail.info')" />
        </div>
        <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)">
          <template #actions><UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="load" /></template>
        </UAlert>
        <LoadingRows v-else-if="!data" :n="3" :icon="false" />
        <p v-else-if="!data.groups.length" class="rounded-lg border border-(--ui-border) p-3 text-xs text-(--ui-text-muted)">{{ t('fail.empty') }}</p>
        <div v-else class="overflow-hidden rounded-lg border border-(--ui-border)">
          <div v-for="g in data.groups" :key="g.category + g.signature" class="border-b border-(--ui-border) last:border-0">
            <button
              type="button" class="flex w-full items-start gap-3 px-3 py-2 text-left hover:bg-(--ui-bg-elevated)/50"
              :aria-expanded="expanded === g.category + g.signature"
              @click="expanded = expanded === g.category + g.signature ? null : g.category + g.signature"
            >
              <span class="mt-0.5 w-8 shrink-0 text-right sm:w-10 font-semibold tabular-nums">{{ g.count }}×</span>
              <span class="min-w-0 flex-1">
                <span class="flex flex-wrap items-center gap-1.5">
                  <UBadge :color="catColor(g.category)" variant="subtle" size="sm">{{ catName(g.category) }}</UBadge>
                  <span class="text-xs text-(--ui-text-muted)">{{ t('fail.last', { when: when(g.last) }) }}</span>
                </span>
                <span class="mt-1 block font-mono text-xs break-anywhere">{{ g.signature }}</span>
                <span class="block text-xs text-(--ui-text-muted)">
                  {{ [g.agent_names.join(', '), g.providers.join(', '), project ? '' : g.project_names.join(', '), g.jobs ? t('fail.jobs', { n: g.jobs }) : '', g.calls ? t('fail.calls', { n: g.calls }) : ''].filter(Boolean).join(' · ') }}
                </span>
              </span>
              <UIcon :name="expanded === g.category + g.signature ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" class="mt-1 size-4 shrink-0 text-(--ui-text-dimmed)" />
            </button>
            <div v-if="expanded === g.category + g.signature" class="space-y-2 px-3 pb-3 pl-14 sm:pl-16">
              <p class="text-xs text-(--ui-text-muted)">{{ t('fail.firstLast', { first: when(g.first), last: when(g.last) }) }}</p>
              <pre class="max-h-48 overflow-auto rounded-md bg-(--ui-bg-muted) p-2 font-mono text-xs leading-5 whitespace-pre-wrap break-anywhere">{{ g.sample }}</pre>
              <div v-if="g.job_ids.length" class="flex flex-wrap gap-1.5">
                <UButton
                  v-for="(id, i) in g.job_ids" :key="id" size="xs" color="neutral" variant="outline" icon="i-lucide-square-arrow-out-up-right"
                  :label="t('fail.job', { n: i + 1 })" @click="detail = id"
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    </template>
  </UModal>
  <JobDetailModal :job-id="detail" @close="detail = null" @open="(j: Job) => detail = j.id" />
</template>
