<script setup lang="ts">
// How each model/effort did per kind of work × difficulty (ADR-267): real
// jobs blended with public numbers until there are enough; ★ = optimal.
const props = defineProps<{ projectId: string }>()
const { t, dateLocale } = useLang()
const toast = useToast()
const { isAdmin } = useAuth()

interface Cell { agent_id: string, provider_id: string, model: string, effort: string, kind: string, difficulty: string, n: number, q: number, cost_usd: number, duration_ms: number, from_prior: boolean }
interface Score { job_id: string, agent_id: string, model: string, effort: string, kind: string, difficulty: string, label_manual: boolean, status: string, q: number | null, feedback: number, cost_usd: number, finished_at: string }
interface Conn { provider_id: string, name: string, over: boolean, max_util: number, subscription: boolean }
interface Reply { days: number, table: { cells: Cell[], best: Cell[] }, quota: Conn[], recent: Score[], agents: Record<string, string>, kinds: string[], difficulties: string[] }

const days = ref(30)
const { data, error, refresh } = useLiveFetch<Reply>(() => `/api/projects/${props.projectId}/model-stats?days=${days.value}`, { lazy: true })
const dayItems = computed(() => [7, 30, 90].map(n => ({ label: t('scoring.days', { n }), value: n })))

const key = (c: { agent_id: string, model: string, effort: string, kind: string, difficulty: string }) => [c.agent_id, c.model, c.effort, c.kind, c.difficulty].join('|')
const bestKeys = computed(() => new Set((data.value?.table.best ?? []).map(key)))
// groups that have real jobs, in the order of kinds × difficulties
const groups = computed(() => {
  const d = data.value
  if (!d) return []
  const out: { kind: string, difficulty: string, cells: Cell[] }[] = []
  for (const k of d.kinds) {
    for (const diff of d.difficulties) {
      const cells = d.table.cells.filter(c => c.kind === k && c.difficulty === diff).sort((a, b) => b.q - a.q)
      if (cells.length) out.push({ kind: k, difficulty: diff, cells })
    }
  }
  return out
})
const agentName = (id: string) => data.value?.agents[id] ?? id
const effortName = (e: string) => e ? effortLabel(e, t) : t('scoring.effortDefault')
const secs = (ms: number) => ms >= 60000 ? `${Math.round(ms / 60000)}m` : `${Math.round(ms / 1000)}s`

const kindItems = computed(() => (data.value?.kinds ?? []).map(k => ({ label: t(`scoring.kind.${k}` as 'scoring.kind.ask'), value: k })))
const diffItems = computed(() => (data.value?.difficulties ?? []).map(d => ({ label: t(`scoring.diff.${d}` as 'scoring.diff.light'), value: d })))
async function relabel(s: Score, kind: string, difficulty: string) {
  try {
    await $fetch(`/api/projects/${props.projectId}/job-scores/${s.job_id}/labels`, { method: 'PUT', body: { kind, difficulty } })
    toast.add({ title: t('scoring.labelSaved'), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <UCard :ui="{ body: 'space-y-4 p-3 sm:p-4' }">
    <div class="flex flex-wrap items-center gap-2">
      <UIcon name="i-lucide-gauge" class="size-5 shrink-0 text-(--ui-primary)" />
      <div class="min-w-0 flex-1">
        <p class="text-sm font-medium">{{ t('scoring.title') }}</p>
        <p class="text-xs text-(--ui-text-muted)">{{ t('scoring.subtitle') }}</p>
      </div>
      <USelect v-model="days" :items="dayItems" size="sm" class="w-28" />
    </div>

    <UAlert v-if="error" color="error" variant="subtle" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => { refresh() } }]" />
    <LoadingRows v-else-if="!data" :n="4" />
    <template v-else>
      <div v-if="data.quota.length" class="flex flex-wrap gap-1.5">
        <span class="text-xs text-(--ui-text-muted)">{{ t('scoring.quota') }}:</span>
        <UBadge v-for="c in data.quota" :key="c.provider_id" :color="c.over ? 'error' : c.max_util >= 0.8 ? 'warning' : 'neutral'" variant="subtle" size="sm">
          {{ c.name }} · {{ c.over ? t('scoring.quotaOver') : t('scoring.quotaUsed', { pct: Math.round(c.max_util * 100) }) }}<template v-if="c.subscription"> · {{ t('scoring.subscription') }}</template>
        </UBadge>
      </div>

      <p v-if="!groups.length" class="text-sm text-(--ui-text-muted)">{{ t('scoring.empty') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full min-w-[36rem] text-sm">
          <thead class="text-left text-xs text-(--ui-text-muted)">
            <tr>
              <th class="py-1 pe-2 font-normal">{{ t('scoring.colConfig') }}</th>
              <th class="py-1 pe-2 text-right font-normal">{{ t('scoring.colQ') }}</th>
              <th class="py-1 pe-2 text-right font-normal">{{ t('scoring.colCost') }}</th>
              <th class="py-1 pe-2 text-right font-normal">{{ t('scoring.colTime') }}</th>
              <th class="py-1 text-right font-normal">{{ t('scoring.colN') }}</th>
            </tr>
          </thead>
          <tbody v-for="g in groups" :key="`${g.kind}-${g.difficulty}`">
            <tr>
              <td colspan="5" class="pt-3 pb-1 text-xs font-medium text-(--ui-text-highlighted)">{{ t(`scoring.kind.${g.kind}` as 'scoring.kind.ask') }} · {{ t(`scoring.diff.${g.difficulty}` as 'scoring.diff.light') }}</td>
            </tr>
            <tr v-for="c in g.cells" :key="key(c)" class="border-t border-(--ui-border)" :class="bestKeys.has(key(c)) && 'bg-(--ui-primary)/5'">
              <td class="py-1 pe-2">
                <span v-if="bestKeys.has(key(c))" class="me-1 text-(--ui-primary)" :title="t('scoring.best')">★</span>
                {{ agentName(c.agent_id) }} · <span class="font-mono text-xs">{{ c.model || '—' }}</span> · {{ effortName(c.effort) }}
                <UBadge v-if="c.from_prior" color="neutral" variant="outline" size="sm" class="ms-1">{{ t('scoring.prior') }}</UBadge>
              </td>
              <td class="py-1 pe-2 text-right tabular-nums" :class="c.q >= 80 ? 'text-(--ui-success)' : 'text-(--ui-text-muted)'">{{ Math.round(c.q) }}</td>
              <td class="py-1 pe-2 text-right tabular-nums">${{ c.cost_usd.toFixed(2) }}</td>
              <td class="py-1 pe-2 text-right tabular-nums">{{ secs(c.duration_ms) }}</td>
              <td class="py-1 text-right tabular-nums">{{ c.n }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <details v-if="data.recent.length" class="text-sm">
        <summary class="cursor-pointer text-xs text-(--ui-text-muted)">{{ t('scoring.recent') }} ({{ data.recent.length }})</summary>
        <ul class="mt-2 space-y-1.5">
          <li v-for="s in data.recent" :key="s.job_id" class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
            <span class="text-(--ui-text-muted) tabular-nums">{{ new Date(s.finished_at).toLocaleString(dateLocale, { dateStyle: 'short', timeStyle: 'short' }) }}</span>
            <span>{{ agentName(s.agent_id) }} · <span class="font-mono">{{ s.model || '—' }}</span> · {{ effortName(s.effort) }}</span>
            <span class="tabular-nums" :class="(s.q ?? 0) >= 80 ? 'text-(--ui-success)' : 'text-(--ui-text-muted)'">Q {{ s.q ?? '—' }}</span>
            <span v-if="s.feedback" :class="s.feedback > 0 ? 'text-(--ui-success)' : 'text-(--ui-error)'">{{ s.feedback > 0 ? '👍' : '👎' }}</span>
            <template v-if="isAdmin">
              <USelect :model-value="s.kind" :items="kindItems" size="xs" class="w-28" @update:model-value="(v: string) => relabel(s, v, s.difficulty)" />
              <USelect :model-value="s.difficulty" :items="diffItems" size="xs" class="w-20" @update:model-value="(v: string) => relabel(s, s.kind, v)" />
            </template>
            <span v-else class="text-(--ui-text-muted)">{{ t(`scoring.kind.${s.kind}` as 'scoring.kind.ask') }} · {{ t(`scoring.diff.${s.difficulty}` as 'scoring.diff.light') }}</span>
          </li>
        </ul>
      </details>
    </template>
  </UCard>
</template>
