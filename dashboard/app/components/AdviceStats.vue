<script setup lang="ts">
// The overview's line on advisors (ADR-262): how often a stuck agent got a
// second opinion these 7 days, how many of those runs ended done, the cost.
interface Stats { asked: number, skipped: number, objected: number, runs_done: number, runs: number, cost_usd: number }
const { t } = useLang()
const { data, error, refresh } = useLiveFetch<{ days: number, stats: Stats, mode: 'off' | 'repeat' | 'repeat_done', daily_usd: number }>('/api/advice/stats', { lazy: true })
const mode = computed(() => data.value ? t(`advice.mode.${data.value.mode}` as 'advice.mode.off') : '')
</script>

<template>
  <UCard :ui="{ body: 'flex flex-wrap items-center gap-x-4 gap-y-1 p-3 sm:p-4' }">
    <UIcon name="i-lucide-lightbulb" class="size-5 shrink-0 text-(--ui-info)" />
    <div class="min-w-0 flex-1 basis-48">
      <p class="flex items-center gap-1 text-sm font-medium">
        <InfoLabel :label="t('advice.statsTitle')" :info="t('advice.statsHelp')" />
      </p>
      <p class="text-xs text-(--ui-text-muted)">
        <USkeleton v-if="!data && !error" class="h-3 w-48" />
        <template v-else-if="data">{{ mode }}</template>
      </p>
    </div>
    <UAlert v-if="error" color="error" variant="subtle" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => { refresh() } }]" class="basis-full" />
    <template v-else-if="data">
      <div class="text-center">
        <p class="text-lg font-semibold tabular-nums">{{ data.stats.asked }}</p>
        <p class="text-[11px] text-(--ui-text-muted)">{{ t('advice.asked') }}</p>
      </div>
      <div class="text-center">
        <p class="text-lg font-semibold tabular-nums">{{ data.stats.runs_done }}/{{ data.stats.runs }}</p>
        <p class="text-[11px] text-(--ui-text-muted)">{{ t('advice.runsDone') }}</p>
      </div>
      <div class="text-center">
        <p class="text-lg font-semibold tabular-nums">${{ data.stats.cost_usd.toFixed(2) }}</p>
        <p class="text-[11px] text-(--ui-text-muted)">{{ t('advice.cost') }}</p>
      </div>
    </template>
    <div v-else class="flex gap-4">
      <USkeleton v-for="i in 3" :key="i" class="h-9 w-12" />
    </div>
  </UCard>
</template>
