<script setup lang="ts">
// How full the conversation's context is (a small ring by the send button);
// click for the numbers and the connection's usage windows.
const props = defineProps<{ tokens?: number, window?: number }>()
const { t } = useLang()
const { refresh } = useLimits()

const ratio = computed(() => props.tokens && props.window ? Math.min(1, props.tokens / props.window) : 0)
const pct = computed(() => Math.round(ratio.value * 100))
const k = (n?: number) => !n ? '0' : n >= 1e6 ? `${(n / 1e6).toFixed(n % 1e6 ? 1 : 0)}M` : n >= 1000 ? `${Math.round(n / 1000)}k` : String(n)
const C = 2 * Math.PI * 7
const stroke = computed(() => ratio.value >= 0.9 ? 'var(--ui-error)' : ratio.value >= 0.7 ? 'var(--ui-warning)' : 'var(--ui-primary)')
</script>

<template>
  <UPopover :content="{ side: 'top', align: 'end' }" @update:open="(o: boolean) => o && refresh(true)">
    <button type="button" class="flex size-8 items-center justify-center rounded-md hover:bg-(--ui-bg-elevated)" :title="t('limits.contextTitle', { pct: pct })" :aria-label="t('limits.contextTitle', { pct: pct })">
      <svg viewBox="0 0 18 18" class="size-4 -rotate-90">
        <circle cx="9" cy="9" r="7" fill="none" stroke="var(--ui-border-accented)" stroke-width="2.5" />
        <circle cx="9" cy="9" r="7" fill="none" :stroke="stroke" stroke-width="2.5" stroke-linecap="round" :stroke-dasharray="`${C * ratio} ${C}`" />
      </svg>
    </button>
    <template #content>
      <div class="w-72 space-y-3 p-3">
        <div>
          <div class="flex items-baseline justify-between text-xs">
            <span class="font-medium">{{ t('limits.context') }}</span>
            <span class="tabular-nums text-(--ui-text-muted)">{{ window ? `${k(tokens)} / ${k(window)} · ${pct}%` : t('limits.contextUnknown') }}</span>
          </div>
          <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-(--ui-bg-accented)">
            <div class="h-full rounded-full" :style="{ width: `${pct}%`, background: stroke }" />
          </div>
        </div>
        <div class="border-t border-(--ui-border) pt-3">
          <UsageLimits />
          <p class="mt-2 text-[11px] text-(--ui-text-dimmed)">{{ t('limits.fromLastRun') }}</p>
        </div>
      </div>
    </template>
  </UPopover>
</template>
