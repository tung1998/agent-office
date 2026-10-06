<script setup lang="ts">
// The office-wide daily AI limit and the model prices costs are counted
// with (each project's own budget is set on the project).
interface Price { input: number, output: number, cache_read?: number, cache_write?: number }
interface Settings { daily_limit_usd: number, project_limits?: Record<string, number>, prices?: Record<string, Price>, warn_ratio?: number }
const { t } = useLang()
const toast = useToast()
const open = ref(false)
const { data: sum, refresh } = useLiveFetch<{ today: number, daily_limit: number, settings: Settings, default_prices: Record<string, Price> }>('/api/usage/summary', { query: { days: 1 }, lazy: true })
const form = reactive({ daily: 0, prices: [] as { model: string, input: number, output: number }[] })
function edit() {
  const s = sum.value?.settings
  if (!s) return
  form.daily = s.daily_limit_usd || 0
  form.prices = Object.entries(s.prices ?? {}).map(([model, p]) => ({ model, input: p.input, output: p.output }))
  open.value = true
}
async function save() {
  const s = sum.value?.settings
  if (!s) return
  try {
    const prices: Record<string, Price> = {}
    for (const p of form.prices) {
      if (p.model.trim()) prices[p.model.trim()] = { input: Number(p.input) || 0, output: Number(p.output) || 0 }
    }
    // the projects' own budgets stay as they are
    await $fetch('/api/usage/settings', { method: 'PUT', body: { daily_limit_usd: Number(form.daily) || 0, project_limits: s.project_limits ?? {}, prices } })
    open.value = false
    await refresh()
    toast.add({ title: t('costs.saved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const usd = (v: number) => v >= 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(3)}`
</script>

<template>
  <UCard :ui="{ body: 'flex flex-wrap items-center gap-3 sm:p-4' }">
    <UIcon name="i-lucide-wallet" class="size-5 text-(--ui-text-muted)" />
    <div class="min-w-0 flex-1">
      <p class="font-medium">{{ t('budget.officeTitle') }}</p>
      <p class="text-xs text-(--ui-text-muted)">
        <USkeleton v-if="!sum" class="h-3 w-40" />
        <template v-else>{{ t('budget.today', { v: usd(sum.today) }) }} · {{ sum.daily_limit ? t('budget.officeLimit', { v: usd(sum.daily_limit) }) : t('costs.budgetNone') }}</template>
      </p>
    </div>
    <UButton icon="i-lucide-settings-2" :label="t('costs.budget')" color="neutral" variant="outline" :disabled="!sum" @click="edit" />
  </UCard>
  <UModal v-model:open="open" :title="t('budget.officeTitle')" :ui="{ content: 'max-w-xl' }">
    <template #body>
      <form id="office-budget" class="space-y-5" @submit.prevent="save">
        <UFormField :label="t('costs.dailyLimit')" :help="t('costs.dailyLimitHelp')">
          <UInputNumber v-model="form.daily" :min="0" :step="0.5" :format-options="{ minimumFractionDigits: 0, maximumFractionDigits: 2 }" />
        </UFormField>
        <p class="text-xs text-(--ui-text-muted)">{{ t('budget.perProjectHint') }}</p>
        <div>
          <p class="text-sm font-medium">{{ t('costs.modelPrices') }}</p>
          <p class="text-xs text-(--ui-text-muted)">{{ t('costs.modelPricesDesc') }}</p>
          <div class="mt-2 space-y-2">
            <div v-for="(p, i) in form.prices" :key="i" class="flex items-center gap-2">
              <UInput v-model="p.model" placeholder="gpt-5" size="sm" class="flex-1 font-mono" />
              <UInputNumber v-model="p.input" :min="0" :step="0.1" size="sm" class="w-28" placeholder="input" />
              <UInputNumber v-model="p.output" :min="0" :step="0.1" size="sm" class="w-28" placeholder="output" />
              <UButton icon="i-lucide-x" size="xs" color="neutral" variant="ghost" @click="form.prices.splice(i, 1)" />
            </div>
            <UButton icon="i-lucide-plus" :label="t('costs.addPrice')" size="xs" color="neutral" variant="outline" @click="form.prices.push({ model: '', input: 0, output: 0 })" />
          </div>
          <details class="mt-2 text-xs text-(--ui-text-muted)">
            <summary class="cursor-pointer">{{ t('costs.defaultPrices') }}</summary>
            <ul class="mt-1 space-y-0.5 font-mono">
              <li v-for="(p, m) in sum?.default_prices" :key="m">{{ m }}: ${{ p.input }} / ${{ p.output }}</li>
            </ul>
          </details>
        </div>
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="open = false" />
        <UButton type="submit" form="office-budget" :label="t('common.save')" />
      </div>
    </template>
  </UModal>
</template>
