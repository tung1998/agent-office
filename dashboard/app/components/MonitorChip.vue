<script setup lang="ts">
// The monitor of one process or compose service, on its row in Vận hành:
// its status and 24h uptime; opened, turn it on/off, AI analysis when down,
// check now, and its Up/Down events. No monitor yet: an admin turns it on.
export interface MonitorItem {
  id: string
  name: string
  type: 'process' | 'container'
  target: string
  config: { file?: string }
  enabled: boolean
  ai_enabled: boolean
  status: 'pending' | 'up' | 'down' | 'paused'
  last_message: string
  uptime_24h: number | null
  trend: { at: string, ok: boolean, latency_ms: number, message: string }[]
}
interface MonitorEvent { id: string, monitor_id: string, kind: 'up' | 'down', message: string, analysis: string, analysis_status: string, cost_usd: number, at: string }

const props = defineProps<{ projectId: string, type: 'process' | 'container', target: string, file?: string, monitor?: MonitorItem }>()
const emit = defineEmits<{ changed: [] }>()
const { t, when: fmtWhen } = useLang()
const toast = useToast()
const { manages } = useAuth()
const isAdmin = computed(() => manages(props.projectId)) // its project's admin or manager, on any page (ADR-224)

const statusMeta = computed(() => ({
  up: { label: t('monitor.status.up'), color: 'success' as const },
  down: { label: t('monitor.status.down'), color: 'error' as const },
  pending: { label: t('monitor.status.pending'), color: 'neutral' as const },
  paused: { label: t('monitor.status.paused'), color: 'neutral' as const }
}))
const pct = (v: number | null) => v === null ? '' : `${v >= 99.95 ? 100 : v.toFixed(v >= 99 ? 2 : 1)}%`
const when = (d: string) => fmtWhen(d, { seconds: true })

const open = ref(false)
const busy = ref(false)
async function run(fn: () => Promise<unknown>) {
  if (busy.value) return
  busy.value = true
  try {
    await fn()
    emit('changed')
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = false
  }
}
const turnOn = () => run(() => $fetch(`/api/projects/${props.projectId}/monitors`, {
  method: 'POST', body: { type: props.type, target: props.target, config: props.file ? { file: props.file } : {} }
}))
const patch = (body: Record<string, unknown>) => run(() => $fetch(`/api/monitors/${props.monitor!.id}`, { method: 'PATCH', body }))
const checkNow = () => run(() => $fetch(`/api/monitors/${props.monitor!.id}/check`, { method: 'POST' }))
function remove() {
  if (!confirm(t('monitor.confirmOff'))) return
  open.value = false
  run(() => $fetch(`/api/monitors/${props.monitor!.id}`, { method: 'DELETE' }))
}

// its events, read when opened
const { data: evData, status: evStatus, execute: loadEvents } = useLiveFetch<{ events: MonitorEvent[] }>('/api/monitor-events', {
  query: { project: props.projectId, limit: 100 }, lazy: true, immediate: false, watch: false, server: false
})
watch(open, (v) => { if (v) loadEvents() })
const events = computed(() => (evData.value?.events ?? []).filter(e => e.monitor_id === props.monitor?.id).slice(0, 20))
</script>

<template>
  <template v-if="monitor">
    <UButton
      size="xs" :color="statusMeta[monitor.status].color" variant="soft" icon="i-lucide-heart-pulse"
      :label="monitor.uptime_24h !== null && monitor.enabled ? pct(monitor.uptime_24h) : statusMeta[monitor.status].label"
      :title="`${t('monitor.title')}: ${statusMeta[monitor.status].label}${monitor.last_message ? ' · ' + monitor.last_message : ''}`"
      @click.stop="open = true"
    />
    <UModal v-model:open="open" :title="t('monitor.titleOf', { name: monitor.name })" :ui="{ body: 'space-y-4' }">
      <template #body>
        <div class="flex items-center gap-2">
          <UBadge :color="statusMeta[monitor.status].color" variant="subtle" :label="statusMeta[monitor.status].label" />
          <span v-if="monitor.uptime_24h !== null" class="text-sm text-(--ui-text-muted)">{{ t('monitor.uptime24h', { v: pct(monitor.uptime_24h) }) }}</span>
          <span class="min-w-0 flex-1 truncate text-xs text-(--ui-text-muted)">{{ monitor.last_message }}</span>
        </div>
        <div class="flex h-5 items-end gap-px">
          <span v-for="i in Math.max(0, 30 - monitor.trend.length)" :key="'e' + i" class="h-full flex-1 rounded-sm bg-(--ui-bg-elevated)" />
          <span
            v-for="(p, i) in monitor.trend" :key="i" class="h-full flex-1 rounded-sm" :class="p.ok ? 'bg-(--ui-success)' : 'bg-(--ui-error)'"
            :title="`${when(p.at)} · ${p.message}`"
          />
        </div>
        <div class="space-y-2">
          <div class="flex items-center justify-between gap-3">
            <span class="text-sm">{{ t('monitor.enabled') }}</span>
            <USwitch :model-value="monitor.enabled" :disabled="!isAdmin || busy" :aria-label="t('monitor.enabled')" @update:model-value="v => patch({ enabled: v })" />
          </div>
          <div class="flex items-center justify-between gap-3">
            <span class="flex items-center gap-1.5 text-sm">{{ t('monitor.aiOnDown') }} <InfoTip :text="t('monitor.aiTooltip')" icon-class="size-3.5 text-(--ui-text-dimmed)" /></span>
            <USwitch :model-value="monitor.ai_enabled" :disabled="!isAdmin || busy" :aria-label="t('monitor.aiOnDown')" @update:model-value="v => patch({ ai_enabled: v })" />
          </div>
        </div>
        <div>
          <p class="mb-1 text-xs font-semibold tracking-wide text-(--ui-text-muted) uppercase">{{ t('monitor.recentEvents') }}</p>
          <LoadingRows v-if="!evData && evStatus !== 'error'" :n="3" />
          <p v-else-if="!events.length" class="text-sm text-(--ui-text-muted)">{{ t('monitor.emptyEvents') }}</p>
          <div v-else class="max-h-72 divide-y divide-(--ui-border) overflow-auto rounded-md border border-(--ui-border)">
            <div v-for="e in events" :key="e.id" class="space-y-1 px-3 py-2">
              <div class="flex items-center gap-2">
                <UBadge :color="e.kind === 'up' ? 'success' : 'error'" variant="subtle" size="sm" :label="e.kind === 'up' ? t('monitor.status.up') : t('monitor.status.down')" />
                <span class="min-w-0 flex-1 truncate text-xs text-(--ui-text-muted)">{{ e.message }}</span>
                <span class="shrink-0 text-xs text-(--ui-text-dimmed)">{{ when(e.at) }}</span>
              </div>
              <div v-if="e.analysis_status === 'running'" class="flex items-center gap-2 text-xs text-(--ui-text-muted)">
                <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-primary" /> {{ t('monitor.aiAnalyzing') }}
              </div>
              <details v-else-if="e.analysis_status === 'done' || e.analysis_status === 'failed'" class="rounded-md bg-(--ui-bg-elevated) p-2 text-xs">
                <summary class="cursor-pointer font-medium">
                  <UIcon name="i-lucide-bot" class="align-middle" /> {{ t('monitor.aiAnalysis') }}<span v-if="e.cost_usd" class="font-normal text-(--ui-text-muted)"> · ${{ e.cost_usd.toFixed(3) }}</span>
                </summary>
                <!-- eslint-disable-next-line vue/no-v-html -->
                <div class="markdown mt-2" v-html="renderMarkdown(e.analysis, false)" />
              </details>
              <p v-else-if="e.analysis_status === 'skipped'" class="text-xs text-(--ui-text-dimmed)">{{ e.analysis }}</p>
            </div>
          </div>
        </div>
      </template>
      <template v-if="isAdmin" #footer>
        <div class="flex w-full items-center gap-2">
          <UButton size="sm" color="error" variant="ghost" icon="i-lucide-heart-off" :label="t('monitor.turnOff')" :disabled="busy" @click="remove" />
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-refresh-cw" :label="t('monitor.menu.checkNow')" :loading="busy" class="ms-auto" @click="checkNow" />
        </div>
      </template>
    </UModal>
  </template>
  <UButton
    v-else-if="isAdmin" size="xs" color="neutral" variant="ghost" icon="i-lucide-heart-pulse"
    :aria-label="t('monitor.turnOn')" :title="t('monitor.turnOn')" :loading="busy" @click.stop="turnOn"
  />
</template>
