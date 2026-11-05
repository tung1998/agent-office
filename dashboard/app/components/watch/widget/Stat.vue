<script setup lang="ts">
// One figure, big (like Graylog's "Count"): jobs running, waiting, failed in
// 24h, spent in 24h, things needing a person; its days below as a trend.
// The number grows with the box.
interface Row { key: string, jobs: number, done: number, failed: number, cost_usd: number }
interface Stats { rows: Row[], totals: { running: number, pending: number, needs_input: number, failed_24h: number, cost_24h: number } }

const { t } = useLang()
const { projectId, config, size, tiny } = useWatchBox()

const query = computed(() => ({ days: 14, ...(projectId.value ? { project: projectId.value } : {}) }))
const { data, error } = useLiveFetch<Stats>('/api/jobs/stats', { query, lazy: true, server: false })
const { data: inc } = useLiveFetch<{ incidents: { project_id: string }[] }>('/api/incidents', { key: 'incidents', lazy: true })

const metric = computed(() => config.value.metric as string)
const value = computed(() => {
  const x = data.value?.totals
  if (metric.value === 'incidents') return inc.value ? inc.value.incidents.filter(i => !projectId.value || i.project_id === projectId.value).length : null
  if (!x) return null
  return { running: x.running, pending: x.pending, needs_input: x.needs_input, failed_24h: x.failed_24h, cost_24h: x.cost_24h }[metric.value] ?? null
})
const shown = computed(() => {
  const v = value.value
  if (v == null) return ''
  if (metric.value === 'cost_24h') return `$${v < 10 ? v.toFixed(2) : Math.round(v).toLocaleString()}`
  return v >= 10000 ? `${(v / 1000).toFixed(1)}K` : v.toLocaleString()
})
// running/waiting have no history: the days' work is their context
const trend = computed(() => {
  const rows = data.value?.rows ?? []
  if (metric.value === 'cost_24h') return rows.map(r => r.cost_usd)
  if (metric.value === 'failed_24h') return rows.map(r => r.failed)
  return rows.map(r => r.jobs)
})
const trendLabel = computed(() => metric.value === 'cost_24h' ? t('watchW.stat.trendCost') : metric.value === 'failed_24h' ? t('watchW.stat.trendFailed') : t('watchW.stat.trendJobs'))
// a red number when something is wrong, never color alone (the label says what)
const alarm = computed(() => (metric.value === 'failed_24h' || metric.value === 'incidents' || metric.value === 'needs_input') && (value.value ?? 0) > 0)
// failures open grouped by cause (ADR-253)
const failOpen = ref(false)
const fontPx = computed(() => Math.round(Math.max(28, Math.min(size.value.w / Math.max(3, shown.value.length * 0.62), size.value.h / 2.6, 140))))
</script>

<template>
  <div class="flex h-full min-h-0 flex-col p-3">
    <p class="truncate text-xs font-medium text-(--ui-text-muted)">{{ t(`watchW.stat.metric.${metric}` as 'watchW.stat.metric.running') }}</p>
    <div class="flex min-h-0 flex-1 items-center justify-center">
      <USkeleton v-if="value == null && !error" class="h-12 w-24" />
      <p v-else-if="error" class="text-sm text-(--ui-error)">{{ apiError(error) }}</p>
      <button
        v-else-if="metric === 'failed_24h'" type="button" class="font-semibold leading-none hover:underline" :aria-label="t('fail.open')"
        :class="alarm ? 'text-(--ui-error)' : 'text-(--ui-text-highlighted)'" :style="{ fontSize: `${fontPx}px` }" @click="failOpen = true"
      >
        {{ shown }}
      </button>
      <p v-else class="font-semibold leading-none" :class="alarm ? 'text-(--ui-error)' : 'text-(--ui-text-highlighted)'" :style="{ fontSize: `${fontPx}px` }">{{ shown }}</p>
    </div>
    <div v-if="config.trend && !tiny && trend.length > 1 && size.h >= 170">
      <p class="text-[11px] text-(--ui-text-dimmed)">{{ trendLabel }}</p>
      <WatchSparkline :values="trend" :label="trendLabel" />
    </div>
    <FailureDigestModal v-if="metric === 'failed_24h'" v-model:open="failOpen" :project="projectId || undefined" since="24h" />
  </div>
</template>
