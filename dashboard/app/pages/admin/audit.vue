<script setup lang="ts">
// Change log of the whole office (ADR-043): 7-day summary, then every change
// with filters.
definePageMeta({ admin: true })

const { t } = useLang()
const _f1 = useLiveFetch<{ projects: { id: string, name: string }[] }>('/api/projects')
const { data: projData } = _f1
const _f2 = useLiveFetch<{ rows: { key: string, count: number, failed: number }[] }>('/api/audit/stats', { query: { by: 'kind', since: '168h' } })
const { data: stats, refresh: refreshStats } = _f2
await Promise.all([_f1, _f2]) // started together: one round trip, not 2 (a phone over a VPN)
const log = ref<{ reload: () => void } | null>(null)

const tiles = computed(() => {
  const rows = stats.value?.rows ?? []
  const of = (k: string) => rows.find(r => r.key === k)?.count ?? 0
  return [
    { label: t('audit.statTotal'), value: rows.reduce((n, r) => n + r.count, 0), icon: 'i-lucide-scroll-text' },
    { label: t('audit.statHuman'), value: of('human'), icon: 'i-lucide-user' },
    { label: t('audit.statAgent'), value: of('agent'), icon: 'i-lucide-bot' },
    { label: t('audit.statFailed'), value: rows.reduce((n, r) => n + r.failed, 0), icon: 'i-lucide-circle-alert' }
  ]
})
function reload() {
  refreshStats()
  log.value?.reload()
}
</script>

<template>
  <PageShell :title="t('audit.title')">
    <template #actions>
      <UButton icon="i-lucide-refresh-cw" color="neutral" variant="ghost" :aria-label="t('audit.title')" @click="reload()" />
    </template>

    <div class="max-w-6xl space-y-4">
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <UCard v-for="s in tiles" :key="s.label" :ui="{ body: 'p-3 sm:p-3' }">
          <div class="flex items-center gap-2 text-xs text-(--ui-text-muted)">
            <UIcon :name="s.icon" class="size-3.5" />{{ s.label }}
          </div>
          <div class="mt-1 text-2xl font-semibold tabular-nums">{{ s.value }}</div>
        </UCard>
      </div>
      <AuditLog ref="log" show-filters :projects="projData?.projects ?? []" />
    </div>
  </PageShell>
</template>
