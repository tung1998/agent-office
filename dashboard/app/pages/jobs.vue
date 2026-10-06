<script setup lang="ts">
// Every piece of work in one place (a chat, a task, an automation): what runs, failed, and what it cost.
const route = useRoute()
const { t } = useLang()
const project = computed(() => (route.query.project as string) || '')
const { data: stats, refresh, error: statsError } = useLiveFetch<{ totals: { running: number, pending: number, failed_24h: number, cost_24h: number } }>(
  () => `/api/jobs/stats?since=24h${project.value ? `&project=${project.value}` : ''}`, { lazy: true })
const { data: proj } = useLiveFetch<{ projects: Project[] }>('/api/projects', { lazy: true })
const projects = computed(() => (proj.value?.projects ?? []).map(p => ({ id: p.id, name: p.name })))
const tiles = computed(() => {
  const x = stats.value?.totals
  return [
    { label: t('job.running'), value: String(x?.running ?? 0), icon: 'i-lucide-loader' },
    { label: t('job.pending'), value: String(x?.pending ?? 0), icon: 'i-lucide-clock' },
    { label: t('job.failed24'), value: String(x?.failed_24h ?? 0), icon: 'i-lucide-circle-x' },
    { label: t('job.cost24'), value: `$${(x?.cost_24h ?? 0).toFixed(2)}`, icon: 'i-lucide-wallet' }
  ]
})
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { timer = setInterval(() => refresh(), 10000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <PageShell :title="t('job.title')">
    <div class="space-y-4">
      <div class="grid grid-cols-2 gap-2 sm:grid-cols-4 sm:gap-3">
        <UCard v-for="x in tiles" :key="x.label" :ui="{ body: 'p-3 sm:p-4' }">
          <p class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)"><UIcon :name="x.icon" class="size-3.5" />{{ x.label }}</p>
          <USkeleton v-if="!stats && !statsError" class="mt-1 h-7 w-14" />
          <p v-else-if="statsError" class="mt-1 text-xl font-semibold text-(--ui-error) sm:text-2xl" :title="t('common.loadError')">!</p>
          <p v-else class="mt-1 text-xl font-semibold tabular-nums sm:text-2xl">{{ x.value }}</p>
        </UCard>
      </div>
      <JobGroups :key="project" :project="project" :projects="projects" />
    </div>
  </PageShell>
</template>
