<script setup lang="ts">
// The few figures worth a glance on every page (admin): what waits for a
// person, the agents at work, the project processes (dev, build…), the machine's CPU and memory. Each opens its
// tab of the overview.
const { t } = useLang()
const { data: inc, error: incErr } = useLiveFetch<{ count: number }>('/api/incidents', { key: 'incidents', lazy: true })
const { summary: sum } = useSystemStats('summary') // the Máy tab's look feeds it too: never two

const memPct = computed(() => sum.value?.mem_total ? Math.round(sum.value.mem_used / sum.value.mem_total * 100) : 0)
const hot = (p: number) => p >= 90 ? 'text-(--ui-error)' : p >= 75 ? 'text-(--ui-warning)' : ''
const items = computed(() => {
  const s = sum.value
  const n = inc.value?.count ?? 0
  return [
    { key: 'inc', icon: incErr.value ? 'i-lucide-triangle-alert' : 'i-lucide-siren', value: incErr.value ? '?' : String(n), title: incErr.value ? t('hdr.incidentsError') : t('hdr.incidents', { n }), to: '/', cls: incErr.value ? 'text-(--ui-warning)' : n ? 'text-(--ui-error)' : '', mobile: true },
    { key: 'agents', icon: 'i-lucide-bot', value: s ? String(s.agents) : '–', title: s ? t('hdr.agents', { n: s.agents, p: s.procs }) : '', to: '/?tab=machine', cls: s?.agents ? 'text-primary' : '', mobile: true },
    { key: 'tasks', icon: 'i-lucide-square-terminal', value: s ? String(s.tasks?.length ?? 0) : '–', title: s?.tasks?.length ? t('hdr.tasks', { n: s.tasks.length, names: s.tasks.slice(0, 5).join(', ') + (s.tasks.length > 5 ? '…' : '') }) : t('hdr.noTasks'), to: '/?tab=machine', cls: s?.tasks?.length ? 'text-primary' : '', mobile: true },
    { key: 'cpu', icon: 'i-lucide-cpu', value: s ? `${Math.round(s.cpu_percent)}%` : '–', title: t('hdr.cpu'), to: '/?tab=machine', cls: s ? hot(s.cpu_percent) : '', mobile: false },
    { key: 'ram', icon: 'i-lucide-memory-stick', value: s ? `${memPct.value}%` : '–', title: t('hdr.ram'), to: '/?tab=machine', cls: hot(memPct.value), mobile: false }
  ]
})
</script>

<template>
  <div class="flex items-center gap-0.5">
    <NuxtLink
      v-for="x in items" :key="x.key" :to="x.to" :title="x.title" :aria-label="x.title"
      class="items-center gap-1 rounded-md px-1.5 py-1 text-xs tabular-nums transition hover:bg-(--ui-bg-elevated)"
      :class="[x.mobile ? 'flex' : 'hidden sm:flex', x.cls || 'text-(--ui-text-muted) hover:text-(--ui-text)']"
    >
      <UIcon :name="x.icon" class="size-3.5" />{{ x.value }}
    </NuxtLink>
  </div>
</template>
