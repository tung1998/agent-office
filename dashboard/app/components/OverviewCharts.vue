<script setup lang="ts">
// The office over the last days, on the overview: how much work ran each day
// (done, failed), what it cost each day, and where the money went (projects,
// models). The per-run list is the Job page.
interface UsageRow { key: string, label: string, runs: number, input_tokens: number, output_tokens: number, cost_usd: number, unknown_cost: number }
interface Summary { period: number, days: number, by_day: UsageRow[], by_project: UsageRow[], by_model: UsageRow[] }
interface DayStats { key: string, jobs: number, done: number, failed: number, cost_usd: number }
const { t, dateLocale } = useLang()

const days = ref(30)
const { data: sum } = useLiveFetch<Summary>('/api/usage/summary', { query: { days }, lazy: true })
const { data: work } = useLiveFetch<{ rows: DayStats[] }>('/api/jobs/stats', { query: { by: 'day', days }, lazy: true })

// every day of the range, a day without work too (the bars keep their place)
const dayKeys = computed(() => {
  const out: string[] = []
  const d = new Date()
  for (let i = days.value - 1; i >= 0; i--) {
    const x = new Date(d.getFullYear(), d.getMonth(), d.getDate() - i)
    out.push(`${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, '0')}-${String(x.getDate()).padStart(2, '0')}`)
  }
  return out
})
const workDays = computed(() => {
  const by = new Map((work.value?.rows ?? []).map(r => [r.key, r]))
  return dayKeys.value.map(k => by.get(k) ?? { key: k, jobs: 0, done: 0, failed: 0, cost_usd: 0 })
})
const costDays = computed(() => {
  const by = new Map((sum.value?.by_day ?? []).map(r => [r.key, r]))
  return dayKeys.value.map(k => by.get(k) ?? { key: k, label: '', runs: 0, input_tokens: 0, output_tokens: 0, cost_usd: 0, unknown_cost: 0 })
})
const maxWork = computed(() => Math.max(1, ...workDays.value.map(d => d.jobs)))
const maxCost = computed(() => Math.max(0.0001, ...costDays.value.map(d => d.cost_usd)))
const totals = computed(() => workDays.value.reduce((a, d) => ({ jobs: a.jobs + d.jobs, done: a.done + d.done, failed: a.failed + d.failed }), { jobs: 0, done: 0, failed: 0 }))
const maxProject = computed(() => Math.max(0.0001, ...(sum.value?.by_project.map(r => r.cost_usd) ?? [0])))
const maxModel = computed(() => Math.max(0.0001, ...(sum.value?.by_model.map(r => r.cost_usd) ?? [0])))

const usd = (v: number) => v >= 100 ? `$${v.toFixed(0)}` : v >= 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(3)}`
const tokens = (v: number) => v >= 1e6 ? `${(v / 1e6).toFixed(1)}M` : v >= 1e3 ? `${(v / 1e3).toFixed(1)}K` : `${v}`
const dayLabel = (key: string) => new Date(key + 'T00:00:00').toLocaleDateString(dateLocale.value, { day: '2-digit', month: '2-digit' })
const tickEvery = computed(() => Math.max(1, Math.ceil(days.value / 8)))
const hover = ref<number | null>(null)
// the cost line: one point at the middle of each day's bar, 0–100 up
const costLine = computed(() => costDays.value.map((d, i) => `${i + 0.5},${100 - (d.cost_usd / maxCost.value) * 100}`).join(' '))
const dim = (i: number) => hover.value !== null && hover.value !== i ? 'opacity-40' : ''
</script>

<template>
  <div class="space-y-3">
    <div class="flex items-center justify-between gap-2">
      <p class="font-semibold">{{ t('home.chartsTitle') }}</p>
      <USelect
        v-model="days" size="sm" class="w-28"
        :items="[{ label: t('costs.range7d'), value: 7 }, { label: t('costs.range30d'), value: 30 }, { label: t('costs.range90d'), value: 90 }]"
      />
    </div>

    <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
      <!-- work and cost on one time axis: two panels, one hover, so a day's
           runs and its money read together (no second y-scale) -->
      <UCard class="lg:col-span-2">
        <template #header>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <p class="font-medium">{{ t('home.workChart') }}</p>
            <div class="flex flex-wrap items-center gap-3 text-xs text-(--ui-text-muted)">
              <span class="flex items-center gap-1"><span class="size-2 rounded-sm bg-(--ui-primary)/70" />{{ t('home.workDone', { n: totals.done }) }}</span>
              <span class="flex items-center gap-1"><span class="size-2 rounded-sm bg-(--ui-error)" />{{ t('home.workFailed', { n: totals.failed }) }}</span>
              <span class="flex items-center gap-1"><span class="size-2 rounded-sm bg-(--ui-text-dimmed)/60" />{{ t('home.workOther', { n: totals.jobs - totals.done - totals.failed }) }}</span>
              <span v-if="sum" class="flex items-center gap-1"><span class="h-0.5 w-3 rounded-full bg-(--ui-warning)" />{{ usd(sum.period) }} · {{ t('costs.avgPerDay', { v: usd(sum.period / Math.max(sum.days, 1)) }) }}</span>
            </div>
          </div>
        </template>
        <USkeleton v-if="!work || !sum" class="h-56 w-full" />
        <div v-else class="relative" @mouseleave="hover = null">
          <div class="flex">
            <p class="w-10 shrink-0 text-[10px] text-(--ui-text-muted)">{{ maxWork }}</p>
            <!-- runs as bars, cost as a line over them (its scale on the right) -->
            <div class="relative h-48 flex-1 border-b border-(--ui-border)">
              <div class="flex h-full items-end gap-0.5 pb-px">
                <div v-for="(d, i) in workDays" :key="d.key" class="flex h-full flex-1 flex-col justify-end" @mouseenter="hover = i">
                  <div class="flex w-full flex-col-reverse overflow-hidden rounded-t transition-opacity" :class="dim(i)" :style="{ height: d.jobs ? `max(2px, ${(d.jobs / maxWork) * 100}%)` : '0' }">
                    <div class="w-full bg-(--ui-primary)/70" :style="{ height: `${(d.done / Math.max(d.jobs, 1)) * 100}%` }" />
                    <div class="w-full bg-(--ui-text-dimmed)/60" :style="{ height: `${((d.jobs - d.done - d.failed) / Math.max(d.jobs, 1)) * 100}%` }" />
                    <div class="w-full border-b-2 border-(--ui-bg) bg-(--ui-error)" :style="{ height: `${(d.failed / Math.max(d.jobs, 1)) * 100}%` }" />
                  </div>
                </div>
              </div>
              <svg class="pointer-events-none absolute inset-0 size-full overflow-visible" :viewBox="`0 0 ${costDays.length} 100`" preserveAspectRatio="none">
                <polyline :points="costLine" fill="none" stroke="var(--ui-warning)" stroke-width="2" stroke-linejoin="round" vector-effect="non-scaling-stroke" />
              </svg>
              <span
                v-if="hover !== null" class="pointer-events-none absolute size-2 -translate-x-1/2 translate-y-1/2 rounded-full bg-(--ui-warning) ring-2 ring-(--ui-bg)"
                :style="{ left: `${((hover + 0.5) / costDays.length) * 100}%`, bottom: `${(costDays[hover]!.cost_usd / maxCost) * 100}%` }"
              />
            </div>
            <p class="w-12 shrink-0 text-right text-[10px] text-(--ui-warning)">{{ usd(maxCost) }}</p>
          </div>
          <div class="ms-10 me-12 mt-1 flex gap-0.5 text-[10px] text-(--ui-text-muted)">
            <span v-for="(d, i) in workDays" :key="d.key" class="flex-1 text-center whitespace-nowrap">{{ i % tickEvery === 0 ? dayLabel(d.key) : '' }}</span>
          </div>
          <div v-if="hover !== null" class="pointer-events-none absolute right-0 top-0 rounded-md border border-(--ui-border) bg-(--ui-bg) px-3 py-2 text-xs shadow-sm">
            <p class="font-medium">{{ new Date(workDays[hover]!.key + 'T00:00:00').toLocaleDateString(dateLocale) }}</p>
            <p class="tabular-nums">{{ t('home.workRuns', { n: workDays[hover]!.jobs }) }} · {{ t('home.workDone', { n: workDays[hover]!.done }) }} · {{ t('home.workFailed', { n: workDays[hover]!.failed }) }}</p>
            <p class="tabular-nums">{{ usd(costDays[hover]!.cost_usd) }} · {{ tokens(costDays[hover]!.input_tokens) }} in / {{ tokens(costDays[hover]!.output_tokens) }} out</p>
          </div>
        </div>
      </UCard>

      <!-- where the money went -->
      <UCard
        v-for="block in [
          { title: t('costs.byProject'), rows: sum?.by_project, max: maxProject, isModel: false },
          { title: t('costs.byModel'), rows: sum?.by_model, max: maxModel, isModel: true }
        ]" :key="block.title"
      >
        <template #header><p class="font-medium">{{ block.title }}</p></template>
        <LoadingRows v-if="!sum" :n="3" :icon="false" />
        <p v-else-if="!block.rows?.length" class="text-sm text-(--ui-text-muted)">{{ t('costs.noRuns') }}</p>
        <div v-else class="space-y-2.5">
          <div v-for="r in block.rows.slice(0, 6)" :key="r.key" class="text-sm">
            <div class="flex items-baseline justify-between gap-2">
              <span class="truncate">{{ block.isModel ? (r.key || '—') : (r.label || t('costs.noProject')) }}</span>
              <span class="shrink-0 tabular-nums">{{ usd(r.cost_usd) }}</span>
            </div>
            <div class="mt-1 h-1.5 rounded-full bg-(--ui-bg-accented)">
              <div class="h-full rounded-full bg-(--ui-primary)" :style="{ width: `${Math.max(1, (r.cost_usd / block.max) * 100)}%` }" />
            </div>
            <p class="mt-0.5 text-xs text-(--ui-text-muted)">{{ t('costs.runsAndTokens', { runs: r.runs, in: tokens(r.input_tokens), out: tokens(r.output_tokens) }) }}</p>
          </div>
        </div>
      </UCard>
    </div>
  </div>
</template>
