<script setup lang="ts">
// A project's automation runs, shown beside the automation list on desktop.
// Jobs are the source of truth for every schedule, webhook and bot run.
const props = defineProps<{ projectId: string, automations: Automation[] }>()
const { t, when } = useLang()
const ALL = '_'
const pick = ref(ALL)
const limit = ref(20)

const items = computed(() => [
  { label: t('auto.runs.all'), value: ALL },
  ...props.automations.map(a => ({ label: a.name, value: a.id }))
])
const url = computed(() => {
  const id = pick.value === ALL ? '' : `&origin_id=${encodeURIComponent(pick.value)}`
  return `/api/jobs?project=${encodeURIComponent(props.projectId)}&origin=automation&since=720h&limit=${limit.value}${id}`
})
const { data, error, pending, refresh } = useLiveFetch<{ jobs: Job[], next_before: string }>(url, { lazy: true })
const jobs = computed(() => data.value?.jobs ?? [])
const more = computed(() => !!data.value?.next_before)
const detail = ref<string | null>(null)
const chatTo = (j: Job) => j.conversation_id ? `/projects/${j.project_id}?tab=chat&c=${j.conversation_id}${j.message_id ? `&m=${j.message_id}` : ''}` : undefined

watch(pick, () => { limit.value = 20 })
useLive(['jobs', 'runs'], () => refresh())

</script>

<template>
  <div class="space-y-3">
    <div class="flex flex-wrap items-center gap-2">
      <USelect v-model="pick" :items="items" size="sm" class="w-56" :aria-label="t('auto.runs.filter')" />
      <p class="text-sm text-(--ui-text-muted)">{{ t('auto.runs.count', { n: jobs.length }) }}</p>
    </div>
    <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)" />
    <LoadingRows v-else-if="pending && !data" />
    <div v-else-if="!jobs.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
      <UIcon name="i-lucide-history" class="mx-auto size-8 text-(--ui-text-dimmed)" />
      <p class="mt-2 font-medium">{{ t('auto.runs.empty') }}</p>
      <p class="text-sm text-(--ui-text-muted)">{{ t('auto.runs.emptyHint') }}</p>
    </div>
    <ul v-else class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
      <li v-for="j in jobs" :key="j.id" class="relative px-3 py-2.5 hover:bg-(--ui-bg-elevated)/50">
        <NuxtLink v-if="chatTo(j)" :to="chatTo(j) || ''" class="absolute inset-0 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-(--ui-primary)" :aria-label="t('job.openChat')" />
        <button v-else type="button" class="absolute inset-0" :aria-label="t('job.detail')" @click="detail = j.id" />
        <div class="relative flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <JobStatusBadge :status="j.status" />
          <span class="min-w-0 flex-1 truncate font-medium">{{ j.automation_name || j.title || t('auto.title') }}</span>
          <UBadge v-if="j.resumes" color="info" variant="subtle" size="sm" icon="i-lucide-rotate-cw" :label="t('job.resumed', { n: j.resumes })" class="shrink-0" />
          <span class="shrink-0 text-xs text-(--ui-text-muted)">{{ when(j.created_at) }} · ${{ j.cost_usd.toFixed(3) }}</span>
        </div>
        <p v-if="j.title && j.title !== j.automation_name" class="relative mt-1 line-clamp-1 text-xs text-(--ui-text-muted)">{{ j.title }}</p>
        <p v-if="j.error" class="relative mt-0.5 line-clamp-1 text-xs text-(--ui-error)">{{ j.error }}</p>
      </li>
      <li v-if="more" class="p-2 text-center">
        <UButton size="xs" color="neutral" variant="ghost" :loading="pending" :label="t('auto.runs.more')" @click="limit += 20" />
      </li>
    </ul>
    <JobDetailModal :job-id="detail" @close="detail = null" @open="(j: Job) => { detail = j.id }" />
  </div>
</template>
