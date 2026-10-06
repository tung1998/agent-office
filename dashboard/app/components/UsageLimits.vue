<script setup lang="ts">
// Usage windows of the AI connections (Claude Code subscriptions): how much of
// each window is used and when it resets. compact = the default one, 2 bars.
import type { MessageKey } from '~/locales/vi'

const props = defineProps<{ compact?: boolean, providerId?: string }>()
const { t, dateLocale } = useLang()
const { limits, refresh } = useLimits()
onMounted(() => refresh())

const shown = computed(() => {
  let list = limits.value
  if (props.providerId) list = list.filter(l => l.provider_id === props.providerId)
  else if (props.compact) {
    const one = list.find(l => l.is_default) ?? list[0]
    list = one ? [one] : []
  }
  return list
})
function label(key: string) {
  const k = `limits.window.${key}` as MessageKey
  const s = t(k)
  if (s !== k) return s
  const model = key.startsWith('seven_day_') ? key.slice(10) : ''
  return model ? t('limits.weeklyModel', { model: model.charAt(0).toUpperCase() + model.slice(1) }) : key
}
function resets(at: string) {
  const d = new Date(at)
  const ms = d.getTime() - Date.now()
  if (ms <= 0) return t('limits.resetDone')
  if (ms < 24 * 3600e3) {
    const h = Math.floor(ms / 3600e3)
    const m = Math.round((ms % 3600e3) / 60e3)
    return t('limits.resetsIn', { time: h ? `${h}h ${m}m` : `${m}m` })
  }
  return t('limits.resetsAt', { time: d.toLocaleString(dateLocale.value, { weekday: 'short', hour: '2-digit', minute: '2-digit' }) })
}
const pct = (u: number) => Math.round(u * 100)
const color = (u: number) => u >= 0.9 ? 'bg-(--ui-error)' : u >= 0.7 ? 'bg-(--ui-warning)' : 'bg-primary'
const windows = (w: Record<string, LimitWindow>) => props.compact ? orderedWindows(w).filter(([k]) => k === 'five_hour' || k === 'seven_day') : orderedWindows(w)
</script>

<template>
  <div v-if="shown.length" class="space-y-3">
    <div v-for="l in shown" :key="l.provider_id" class="space-y-2">
      <p v-if="!compact && !providerId" class="text-xs text-(--ui-text-muted)">{{ t('limits.title', { name: l.provider_name }) }}</p>
      <div v-for="[k, w] in windows(l.windows)" :key="k" :title="compact ? `${label(k)} · ${resets(w.resets_at)}` : undefined">
        <div class="flex items-baseline justify-between gap-2" :class="compact ? 'text-[11px]' : 'text-xs'">
          <span class="truncate">{{ label(k) }}</span>
          <span class="shrink-0 tabular-nums text-(--ui-text-muted)">
            <template v-if="!compact">{{ resets(w.resets_at) }} · </template>{{ pct(w.utilization) }}%
          </span>
        </div>
        <div class="mt-1 overflow-hidden rounded-full bg-(--ui-bg-accented)" :class="compact ? 'h-1' : 'h-1.5'">
          <div class="h-full rounded-full transition-all" :class="color(w.utilization)" :style="{ width: `${Math.min(100, pct(w.utilization))}%` }" />
        </div>
      </div>
    </div>
  </div>
</template>
