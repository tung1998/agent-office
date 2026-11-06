<script setup lang="ts">
// An advisor's second opinion for an agent stuck on the same failure, or
// its review of such a run's answer (ADR-262): folded, opened on a click.
export interface Advice {
  id: string
  message_id?: string
  trigger: 'repeat' | 'done'
  agent_name: string
  advisor_name?: string
  provider?: string
  model?: string
  command?: string
  error?: string
  answer?: string
  objection?: boolean
  status: 'answered' | 'failed' | 'skipped'
  reason?: string
  cost_usd?: number
  created_at: string
}
const props = defineProps<{ advice: Advice, projectId: string }>()
const { t } = useLang()
const tone = computed(() => props.advice.status !== 'answered' ? 'neutral' : props.advice.objection ? 'warning' : 'info')
const title = computed(() => {
  const a = props.advice
  if (a.status !== 'answered') return t('advice.skipped', { agent: a.agent_name })
  if (a.trigger === 'done') return a.objection ? t('advice.objected', { advisor: a.advisor_name ?? '' }) : t('advice.reviewed', { advisor: a.advisor_name ?? '' })
  return t('advice.title', { advisor: a.advisor_name ?? '', agent: a.agent_name })
})
const meta = computed(() => [props.advice.model || props.advice.provider, props.advice.cost_usd ? `$${props.advice.cost_usd.toFixed(3)}` : ''].filter(Boolean).join(' · '))
</script>

<template>
  <details class="group rounded-lg border px-3 py-2 text-xs" :class="{ 'border-(--ui-info)/40 bg-(--ui-info)/5': tone === 'info', 'border-(--ui-warning)/40 bg-(--ui-warning)/5': tone === 'warning', 'border-(--ui-border) bg-(--ui-bg-elevated)/40': tone === 'neutral' }">
    <summary class="flex cursor-pointer list-none items-center gap-2">
      <UIcon :name="advice.trigger === 'done' ? 'i-lucide-shield-check' : 'i-lucide-lightbulb'" class="size-4 shrink-0" :class="{ 'text-(--ui-info)': tone === 'info', 'text-(--ui-warning)': tone === 'warning', 'text-(--ui-text-muted)': tone === 'neutral' }" />
      <span class="min-w-0 flex-1 truncate font-medium">{{ title }}</span>
      <span v-if="meta" class="shrink-0 text-(--ui-text-dimmed)">{{ meta }}</span>
      <UIcon name="i-lucide-chevron-down" class="size-3.5 shrink-0 transition-transform group-open:rotate-180" />
    </summary>
    <div class="mt-2 space-y-2">
      <p v-if="advice.command" class="truncate font-mono text-(--ui-text-muted)" :title="advice.command">
        {{ advice.command }}
      </p>
      <!-- eslint-disable-next-line vue/no-v-html -->
      <div v-if="advice.answer" class="markdown text-sm" v-html="renderMarkdown(advice.answer, true, projectId)" />
      <p v-else-if="advice.reason" class="text-(--ui-text-muted)">{{ advice.reason }}</p>
    </div>
  </details>
</template>
