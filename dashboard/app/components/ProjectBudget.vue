<script setup lang="ts">
// A project's daily AI budget (its runs stop for the day once reached), what
// it spent today, and the office-wide limit above it.
const props = defineProps<{ projectId: string }>()
const { t } = useLang()
const toast = useToast()
const { isAdmin } = useAuth()
const { data, refresh } = useLiveFetch<{ daily_limit_usd: number, today_usd: number, office_limit_usd: number }>(() => `/api/projects/${props.projectId}/budget`, { lazy: true })
const limit = ref(0)
const { stale, reset: resync } = useDraft(() => data.value && { daily_limit_usd: data.value.daily_limit_usd }, limit, (d) => { limit.value = d.daily_limit_usd ?? 0 }) // never over what is being typed
const saving = ref(false)
async function save() {
  saving.value = true
  try {
    await $fetch(`/api/projects/${props.projectId}/budget`, { method: 'PUT', body: { daily_limit_usd: Number(limit.value) || 0 } })
    await refresh()
    resync() // saved: take what the server has now
    toast.add({ title: t('costs.saved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
const usd = (v: number) => v >= 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(3)}`
const ratio = computed(() => data.value?.daily_limit_usd ? data.value.today_usd / data.value.daily_limit_usd : 0)
</script>

<template>
  <UCard class="max-w-4xl" :ui="{ body: 'space-y-3 sm:p-4' }">
    <p class="flex items-center gap-2 font-medium"><UIcon name="i-lucide-wallet" class="size-4 text-(--ui-text-muted)" />{{ t('budget.title') }}</p>
    <StaleNotice :show="stale" @reload="resync" />
    <USkeleton v-if="!data" class="h-10 w-full" />
    <template v-else>
      <p class="text-sm">
        {{ t('budget.today', { v: usd(data.today_usd) }) }}<template v-if="data.daily_limit_usd"> / {{ usd(data.daily_limit_usd) }}</template>
      </p>
      <UProgress v-if="data.daily_limit_usd" :model-value="Math.min(100, ratio * 100)" :color="ratio >= 1 ? 'error' : ratio >= 0.8 ? 'warning' : 'primary'" size="sm" />
      <UFormField v-if="isAdmin" :label="t('budget.limit')" :help="t('budget.limitHelp')">
        <div class="flex items-center gap-2">
          <UInputNumber v-model="limit" :min="0" :step="0.5" :placeholder="t('costs.noLimitPlaceholder')" class="w-40" />
          <UButton icon="i-lucide-save" :label="t('common.save')" :loading="saving" @click="save" />
        </div>
      </UFormField>
      <p v-if="data.office_limit_usd" class="text-xs text-(--ui-text-muted)">{{ t('budget.officeLimit', { v: usd(data.office_limit_usd) }) }}</p>
    </template>
  </UCard>
</template>
