<script setup lang="ts">
// One job, whatever it ran: how it went and why (the error first), what it
// ran with, what a script printed, and where to open what it belongs to.
const props = defineProps<{ jobId: string | null }>()
const emit = defineEmits<{ close: [], open: [Job] }>()
const { t, dateLocale } = useLang()
const data = ref<{ job: Job, children: Job[] | null } | null>(null)
watch(() => props.jobId, async (id) => {
  data.value = null
  if (id) data.value = await $fetch(`/api/jobs/${id}`)
}, { immediate: true })
const open = computed({ get: () => !!props.jobId, set: (v: boolean) => { if (!v) emit('close') } })
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', second: '2-digit', day: '2-digit', month: '2-digit' })
const secs = (ms: number) => !ms ? '—' : ms >= 60000 ? `${(ms / 60000).toFixed(1)}m` : `${(ms / 1000).toFixed(1)}s`
const kindName = (k: string) => t(`job.kind.${k}` as 'job.kind.script')
// where it belongs: its chat (the assistant's on its own page), its automation
const { data: asst } = useLiveFetch<{ project_id: string }>('/api/assistant', { key: 'assistant', lazy: true })
const link = computed(() => {
  const j = data.value?.job
  if (!j) return null
  if (j.conversation_id && j.project_id === asst.value?.project_id) return { to: `/assistant?c=${j.conversation_id}`, label: t('job.openChat'), icon: 'i-lucide-messages-square' }
  if (j.conversation_id) return { to: `/projects/${j.project_id}?tab=chat&c=${j.conversation_id}${j.message_id ? `&m=${j.message_id}` : ''}`, label: t('job.openChat'), icon: 'i-lucide-messages-square' }
  if (j.origin === 'automation' && j.origin_id) return { to: `/projects/${j.project_id}/automations/${j.origin_id}`, label: t('job.openAutomation'), icon: 'i-lucide-alarm-clock' }
  return null
})
const facts = computed(() => {
  const j = data.value?.job
  if (!j) return []
  return [
    [t('job.factKind'), kindName(j.kind)],
    [t('job.factProject'), j.project_name || '—'],
    [t('job.factWho'), j.automation_name || j.agent_name || j.created_by || '—'],
    [t('job.factTrigger'), j.trigger || '—'],
    [t('job.factStarted'), j.started_at ? when(j.started_at) : when(j.created_at)],
    [t('job.factDuration'), secs(j.duration_ms)],
    [t('job.factTokens'), j.input_tokens || j.output_tokens ? `${j.input_tokens} / ${j.output_tokens}` : '—'],
    [t('job.factCost'), j.cost_usd ? `$${j.cost_usd.toFixed(3)}` : '—']
  ]
})
</script>

<template>
  <UModal v-model:open="open" :title="data?.job.title || t('job.detail')" :ui="{ content: 'max-w-3xl' }">
    <template #body>
      <LoadingRows v-if="!data" :n="3" :icon="false" />
      <div v-else class="space-y-4 text-sm">
        <div class="flex flex-wrap items-center gap-2 text-xs text-(--ui-text-muted)">
          <JobStatusBadge :status="data.job.status" />
          <span v-if="data.job.exit_code !== null">{{ t('job.exit', { n: data.job.exit_code }) }}</span>
          <span>{{ when(data.job.created_at) }}</span>
        </div>
        <!-- why it failed, first -->
        <div v-if="data.job.error" class="rounded-lg border border-(--ui-error)/40 bg-(--ui-error)/5 p-3">
          <p class="flex items-center gap-1.5 text-xs font-medium text-(--ui-error)">
            <UIcon name="i-lucide-circle-x" class="size-4" />{{ t('job.errorTitle') }}<span v-if="data.job.error_code" class="font-mono">· {{ data.job.error_code }}</span>
          </p>
          <pre class="mt-1.5 max-h-60 overflow-auto font-mono text-xs leading-5 whitespace-pre-wrap break-anywhere">{{ data.job.error }}</pre>
        </div>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4">
          <div v-for="[k, v] in facts" :key="k" class="min-w-0">
            <dt class="text-xs text-(--ui-text-muted)">{{ k }}</dt>
            <dd class="truncate">{{ v }}</dd>
          </div>
        </dl>
        <template v-if="data.job.kind === 'script' || data.job.output">
          <p class="text-xs font-medium text-(--ui-text-muted)">{{ t('job.output') }}</p>
          <pre class="max-h-96 overflow-auto rounded bg-(--ui-bg-elevated) p-2 font-mono text-xs leading-5">{{ data.job.output || t('job.noOutput') }}</pre>
        </template>
        <template v-if="data.children?.length">
          <p class="text-xs font-medium text-(--ui-text-muted)">{{ t('job.children') }}</p>
          <button
            v-for="c in data.children" :key="c.id" type="button"
            class="flex w-full items-center gap-2 rounded-md border border-(--ui-border) px-3 py-2 text-left hover:bg-(--ui-bg-elevated)/50"
            @click="emit('open', c)"
          >
            <JobStatusBadge :status="c.status" />
            <span class="min-w-0 flex-1 truncate">{{ c.agent_name || c.title }}</span>
            <UIcon name="i-lucide-external-link" class="size-3.5 text-(--ui-text-muted)" />
          </button>
        </template>
      </div>
    </template>
    <template v-if="link" #footer>
      <div class="flex w-full justify-end">
        <UButton :to="link.to" :icon="link.icon" :label="link.label" color="neutral" variant="outline" @click="emit('close')" />
      </div>
    </template>
  </UModal>
</template>
