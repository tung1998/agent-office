<script setup lang="ts">
const { isAdmin } = useAuth()
// tabs, the one open in the URL: the work, how it went, the machine and the
// data (admin); each loads its own data only once it is opened
const route = useRoute()
type HomeTab = 'work' | 'stats' | 'machine' | 'data'
const tab = computed<HomeTab>({
  get: () => {
    const q = route.query.tab
    return q === 'stats' || ((q === 'machine' || q === 'data') && isAdmin.value) ? q : 'work'
  },
  set: v => navigateTo({ query: { ...route.query, tab: v } }, { replace: true })
})
const { t } = useLang()
// its count on the work tab: the layout's fetch, shared
const { data: incData } = useNuxtData<{ count: number }>('incidents')
</script>

<template>
  <PageShell :title="t('nav.overview')">
    <UTabs
      v-model="tab" :content="false" variant="link" class="mb-4"
      :items="[
        { value: 'work', label: t('home.tabWork'), icon: 'i-lucide-list-checks', badge: incData?.count ? { label: String(incData.count), color: 'error', variant: 'subtle' } : undefined },
        { value: 'stats', label: t('home.tabStats'), icon: 'i-lucide-chart-column' },
        ...(isAdmin ? [
          { value: 'machine', label: t('home.tabMachine'), icon: 'i-lucide-cpu' },
          { value: 'data', label: t('nav.data'), icon: 'i-lucide-database' }
        ] : [])
      ]"
    />
    <SystemPanel v-if="tab === 'machine'" />
    <DataPanel v-else-if="tab === 'data'" />
    <div v-else-if="tab === 'stats'" class="space-y-6">
      <!-- how the projects worked: boards, agents, changes, schedules (ADR-210) -->
      <ActivityOverview />
      <OverviewCharts />
    </div>
    <HomeWork v-else />
  </PageShell>
</template>
