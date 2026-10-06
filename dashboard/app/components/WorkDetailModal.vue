<script setup lang="ts">
// A piece of work at a glance (a chat, an automation, a failure on the
// overview): what it is, how its runs went, each run's error, and more below
// (logs, a monitor's checks) — a run opens in full, without leaving the page.
const props = defineProps<{ title: string, subtitle?: string, query: string, summary?: { label: string, value: string, bad?: boolean }[] }>()
const open = defineModel<boolean>('open', { default: false })
const { t, dateLocale } = useLang()

const jobs = ref<Job[] | null>(null)
watch([open, () => props.query], async ([o]) => {
  if (!o || !props.query) return
  jobs.value = null
  try {
    jobs.value = (await $fetch<{ jobs: Job[] }>(`/api/jobs?${props.query}`)).jobs
  } catch { jobs.value = [] }
}, { immediate: true })
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
const detail = ref<string | null>(null)
</script>

<template>
  <UModal v-model:open="open" :title="title" :description="subtitle" :ui="{ content: 'max-w-3xl', description: 'line-clamp-2' }">
    <template #body>
      <div class="space-y-4 text-sm">
        <div v-if="summary?.length" class="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <div v-for="s in summary" :key="s.label" class="rounded-lg border border-(--ui-border) px-3 py-2">
            <p class="text-xs text-(--ui-text-muted)">{{ s.label }}</p>
            <p class="font-semibold tabular-nums" :class="s.bad && 'text-(--ui-error)'">{{ s.value }}</p>
          </div>
        </div>
        <slot />
        <div v-if="query">
          <p class="mb-1.5 text-xs font-medium text-(--ui-text-muted)">{{ t('job.runs') }}</p>
          <div class="overflow-hidden rounded-lg border border-(--ui-border)">
            <LoadingRows v-if="!jobs" :n="3" :icon="false" />
            <p v-else-if="!jobs.length" class="p-3 text-xs text-(--ui-text-muted)">{{ t('job.empty') }}</p>
            <button
              v-for="j in jobs ?? []" :key="j.id" type="button"
              class="flex w-full items-start gap-3 border-b border-(--ui-border) px-3 py-2 text-left last:border-0 hover:bg-(--ui-bg-elevated)/50"
              @click="detail = j.id"
            >
              <JobStatusBadge :status="j.status" class="mt-0.5 shrink-0" />
              <span class="min-w-0 flex-1">
                <span class="block truncate">{{ j.title || j.automation_name || j.kind }}</span>
                <span v-if="j.error" class="line-clamp-2 text-xs text-(--ui-error) break-anywhere">{{ j.error }}</span>
                <span class="block text-xs text-(--ui-text-muted)">{{ when(j.created_at) }}<template v-if="j.agent_name"> · {{ j.agent_name }}</template><template v-if="j.trigger"> · {{ j.trigger }}</template><template v-if="j.cost_usd"> · ${{ j.cost_usd.toFixed(3) }}</template></span>
              </span>
              <UIcon name="i-lucide-chevron-right" class="mt-1 size-4 shrink-0 text-(--ui-text-dimmed)" />
            </button>
          </div>
        </div>
      </div>
    </template>
    <template v-if="$slots.actions" #footer>
      <div class="flex w-full flex-wrap justify-end gap-2"><slot name="actions" /></div>
    </template>
  </UModal>
  <JobDetailModal :job-id="detail" @close="detail = null" @open="(j: Job) => detail = j.id" />
</template>
