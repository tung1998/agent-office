<script setup lang="ts">
// Data management (ADR-095): what each project keeps (chats, Burns' chats,
// finished tasks, worktrees, attachments) and cleaning it — by hand, with a
// preview first, or on its own after so many days.
type Kind = 'chat' | 'burn' | 'task'
type Level = 'delete' | 'content' | 'summary'
interface Usage { project_id: string, kind: Kind, items: number, cleaned: number, messages: number, bytes: number }
interface ProjectFiles { worktrees: number, attachments: number, junk: number, junk_items: number }
interface Item { kind: Kind, id: string, project_id: string, title: string, cleaned: string, updated_at: string, messages: number, bytes: number, reason?: string }
interface Status { running: boolean, level: Level, total: number, done: number, started_at: string | null, finished_at: string | null, cleaned: number, bytes: number, failed: Item[] }
interface Auto { enabled: boolean, days: number, level: Level, kinds: Kind[], last_run?: string, last_msg?: string }
interface Data { usage: Usage[], projects: { id: string, name: string }[], files: { projects: Record<string, ProjectFiles>, db_bytes: number, junk: number }, auto: Auto, status: Status }

definePageMeta({ admin: true })
const toast = useToast()
const { t, dateLocale } = useLang()
const { data, refresh } = useFetch<Data>('/api/data')

const KINDS: Kind[] = ['chat', 'burn', 'task']
const LEVELS: Level[] = ['content', 'summary', 'delete']
const kindLabel = (k: Kind) => ({ chat: t('data.kind.chat'), burn: t('data.kind.burn'), task: t('data.kind.task') })[k]
const levelLabel = (l: Level) => ({ delete: t('data.level.delete'), content: t('data.level.content'), summary: t('data.level.summary') })[l]
const levelHint = (l: Level) => ({ delete: t('data.level.deleteHint'), content: t('data.level.contentHint'), summary: t('data.level.summaryHint') })[l]
const when = (iso?: string | null) => iso ? new Date(iso).toLocaleString(dateLocale.value, { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—'

// ---- what each project keeps ----
const projectName = (id: string) => data.value?.projects.find(p => p.id === id)?.name ?? id
const rows = computed(() => {
  const by = new Map<string, { id: string, kinds: Partial<Record<Kind, Usage>>, files?: ProjectFiles, total: number }>()
  const row = (id: string) => {
    if (!by.has(id)) by.set(id, { id, kinds: {}, total: 0 })
    return by.get(id)!
  }
  for (const u of data.value?.usage ?? []) {
    const r = row(u.project_id)
    r.kinds[u.kind] = u
    r.total += u.bytes
  }
  for (const [id, f] of Object.entries(data.value?.files.projects ?? {})) {
    if (!id) continue
    const r = row(id)
    r.files = f
    r.total += f.worktrees + f.attachments
  }
  return [...by.values()].sort((a, b) => b.total - a.total)
})
const totals = computed(() => {
  const k = (kind: Kind) => (data.value?.usage ?? []).filter(u => u.kind === kind).reduce((n, u) => n + u.bytes, 0)
  return { chat: k('chat'), burn: k('burn'), task: k('task'), db: data.value?.files.db_bytes ?? 0, junk: data.value?.files.junk ?? 0 }
})

// ---- the junk: worktrees nothing uses, attachments nothing points to ----
const sweeping = ref(false)
async function sweep() {
  sweeping.value = true
  try {
    const res = await $fetch<{ bytes: number }>('/api/data/sweep', { method: 'POST' })
    toast.add({ title: t('data.swept', { size: fmtBytes(res.bytes) }), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    sweeping.value = false
  }
}

// ---- cleaning by hand: a preview, then the cleanup ----
const ALL = '__all'
const form = reactive({ project: ALL, kinds: [...KINDS] as Kind[], days: 30, level: 'content' as Level })
const projectItems = computed(() => [{ label: t('data.allProjects'), value: ALL }, ...(data.value?.projects ?? []).map(p => ({ label: p.name, value: p.id }))])
const request = computed(() => ({ project_id: form.project === ALL ? '' : form.project, kinds: form.kinds, older_than_days: form.days, level: form.level }))
const plan = ref<{ count: number, items: Item[], skipped: Item[], bytes: number, messages: number } | null>(null)
watch(form, () => { plan.value = null }, { deep: true })
const planning = ref(false)
async function preview() {
  planning.value = true
  try {
    plan.value = await $fetch('/api/data/plan', { method: 'POST', body: request.value })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    planning.value = false
  }
}
function cleanProject(id: string) {
  form.project = id
  nextTick(() => document.getElementById('data-clean')?.scrollIntoView({ behavior: 'smooth' }))
}
const confirmOpen = ref(false)
async function clean() {
  confirmOpen.value = false
  try {
    await $fetch('/api/data/clean', { method: 'POST', body: request.value })
    plan.value = null
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
// while it runs: follow it
const status = computed(() => data.value?.status)
let timer: ReturnType<typeof setInterval> | undefined
watch(() => status.value?.running, (on, was) => {
  clearInterval(timer)
  if (on) timer = setInterval(() => refresh(), 2000)
  else if (was) toast.add({ title: t('data.cleaned', { n: status.value?.cleaned ?? 0, size: fmtBytes(status.value?.bytes ?? 0) }), color: 'success' })
}, { immediate: true })
onBeforeUnmount(() => clearInterval(timer))

// ---- cleaning on its own ----
const auto = reactive<Auto>({ enabled: false, days: 30, level: 'content', kinds: [...KINDS] })
watch(() => data.value?.auto, (a) => { if (a) Object.assign(auto, JSON.parse(JSON.stringify(a))) }, { immediate: true })
const savingAuto = ref(false)
async function saveAuto() {
  savingAuto.value = true
  try {
    await $fetch('/api/data/auto', { method: 'PUT', body: { enabled: auto.enabled, days: auto.days, level: auto.level, kinds: auto.kinds } })
    toast.add({ title: t('data.autoSaved'), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    savingAuto.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl space-y-4 p-4 sm:p-6">
    <div class="flex items-center gap-2">
      <UIcon name="i-lucide-database" class="size-5" />
      <h1 class="text-lg font-semibold">{{ t('nav.data') }}</h1>
    </div>

    <!-- what is kept, at a glance -->
    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
      <UCard v-for="c in [
        { label: t('data.kind.chat'), size: totals.chat, icon: 'i-lucide-messages-square' },
        { label: t('data.kind.burn'), size: totals.burn, icon: 'i-lucide-flame' },
        { label: t('data.kind.task'), size: totals.task, icon: 'i-lucide-list-checks' },
        { label: t('data.dbFile'), size: totals.db, icon: 'i-lucide-hard-drive' }
      ]" :key="c.label" :ui="{ body: 'sm:p-4' }">
        <p class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)"><UIcon :name="c.icon" class="size-4" />{{ c.label }}</p>
        <p class="mt-1 text-xl font-semibold tabular-nums">{{ fmtBytes(c.size) }}</p>
      </UCard>
      <UCard :ui="{ body: 'sm:p-4' }">
        <p class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)">
          <UIcon name="i-lucide-trash" class="size-4" />{{ t('data.junk') }}
          <UTooltip :text="t('data.junkHint')"><UIcon name="i-lucide-info" class="size-3.5" /></UTooltip>
        </p>
        <div class="mt-1 flex items-center justify-between gap-2">
          <span class="text-xl font-semibold tabular-nums">{{ fmtBytes(totals.junk) }}</span>
          <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-brush-cleaning" :label="t('data.sweep')" :loading="sweeping" :disabled="!totals.junk" @click="sweep" />
        </div>
      </UCard>
    </div>

    <!-- by project -->
    <UCard :ui="{ body: 'p-0 sm:p-0' }">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="text-xs text-(--ui-text-muted)">
            <tr class="border-b border-(--ui-border)">
              <th class="px-4 py-2 text-left font-medium">{{ t('data.project') }}</th>
              <th v-for="k in KINDS" :key="k" class="px-3 py-2 text-right font-medium">{{ kindLabel(k) }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('data.worktrees') }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('data.attachments') }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('data.junk') }}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.id" class="border-b border-(--ui-border) last:border-0">
              <td class="px-4 py-2 font-medium">{{ projectName(r.id) }}</td>
              <td v-for="k in KINDS" :key="k" class="px-3 py-2 text-right tabular-nums">
                <template v-if="r.kinds[k]">
                  {{ fmtBytes(r.kinds[k]!.bytes) }}
                  <span class="block text-xs text-(--ui-text-dimmed)">{{ t('data.items', { n: r.kinds[k]!.items, cleaned: r.kinds[k]!.cleaned }) }}</span>
                </template>
                <span v-else class="text-(--ui-text-dimmed)">—</span>
              </td>
              <td class="px-3 py-2 text-right tabular-nums">{{ fmtBytes(r.files?.worktrees ?? 0) }}</td>
              <td class="px-3 py-2 text-right tabular-nums">{{ fmtBytes(r.files?.attachments ?? 0) }}</td>
              <td class="px-3 py-2 text-right tabular-nums">{{ r.files?.junk ? fmtBytes(r.files.junk) : '—' }}</td>
              <td class="px-3 py-2 text-right"><UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-eraser" :label="t('data.cleanThis')" @click="cleanProject(r.id)" /></td>
            </tr>
            <tr v-if="!rows.length"><td colspan="8" class="px-4 py-6 text-center text-(--ui-text-muted)">{{ t('data.empty') }}</td></tr>
          </tbody>
        </table>
      </div>
    </UCard>

    <!-- cleaning by hand -->
    <UCard id="data-clean" :ui="{ body: 'space-y-4 sm:p-4' }">
      <p class="flex items-center gap-2 text-sm font-semibold"><UIcon name="i-lucide-eraser" class="size-4" />{{ t('data.cleanTitle') }}</p>
      <div class="grid gap-3 sm:grid-cols-3">
        <UFormField :label="t('data.project')"><USelect v-model="form.project" :items="projectItems" class="w-full" /></UFormField>
        <UFormField :label="t('data.olderThan')" :hint="t('data.olderThanHint')">
          <div class="flex items-center gap-2"><UInputNumber v-model="form.days" :min="0" class="w-32" /><span class="text-sm text-(--ui-text-muted)">{{ t('data.days') }}</span></div>
        </UFormField>
        <UFormField :label="t('data.kinds')">
          <div class="flex flex-wrap gap-3 pt-1.5">
            <UCheckbox v-for="k in KINDS" :key="k" :model-value="form.kinds.includes(k)" :label="kindLabel(k)"
              @update:model-value="(v: boolean | 'indeterminate') => { form.kinds = v === true ? [...form.kinds, k] : form.kinds.filter(x => x !== k) }" />
          </div>
        </UFormField>
      </div>
      <div class="grid gap-2 sm:grid-cols-3">
        <button
          v-for="l in LEVELS" :key="l" type="button" class="rounded-lg border p-3 text-left transition"
          :class="form.level === l ? (l === 'delete' ? 'border-(--ui-error) bg-(--ui-error)/5' : 'border-primary bg-primary/5') : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
          @click="form.level = l"
        >
          <span class="block text-sm font-medium">{{ levelLabel(l) }}</span>
          <span class="block text-xs text-(--ui-text-muted)">{{ levelHint(l) }}</span>
        </button>
      </div>

      <div v-if="status?.running" class="space-y-1.5">
        <p class="text-sm">{{ t('data.running', { done: status.done, total: status.total, level: levelLabel(status.level) }) }}</p>
        <UProgress :model-value="status.total ? status.done / status.total * 100 : 0" />
      </div>
      <div v-else class="flex flex-wrap items-center gap-2">
        <UButton icon="i-lucide-search" :label="t('data.preview')" color="neutral" variant="outline" :loading="planning" :disabled="!form.kinds.length" @click="preview" />
        <UButton v-if="plan?.count" icon="i-lucide-eraser" :color="form.level === 'delete' ? 'error' : 'primary'" :label="t('data.cleanN', { n: plan.count })" @click="confirmOpen = true" />
      </div>

      <div v-if="plan" class="space-y-2 rounded-lg bg-(--ui-bg-elevated)/50 p-3 text-sm">
        <p>{{ plan.count ? t('data.planSummary', { n: plan.count, size: fmtBytes(plan.bytes), m: plan.messages }) : t('data.planNone') }}</p>
        <p v-if="form.level === 'summary' && plan.count" class="flex items-start gap-1.5 text-xs text-(--ui-warning)"><UIcon name="i-lucide-info" class="mt-0.5 size-4 shrink-0" />{{ t('data.summaryCost', { n: plan.count }) }}</p>
        <ul class="max-h-56 space-y-0.5 overflow-auto text-xs">
          <li v-for="it in plan.items" :key="it.id" class="flex gap-2">
            <UBadge :label="kindLabel(it.kind)" color="neutral" variant="subtle" size="sm" />
            <span class="min-w-0 flex-1 truncate">{{ it.title || t('chat.newThreadTitle') }}<span class="text-(--ui-text-dimmed)"> · {{ projectName(it.project_id) }}</span></span>
            <span class="shrink-0 tabular-nums text-(--ui-text-muted)">{{ fmtBytes(it.bytes) }} · {{ when(it.updated_at) }}</span>
          </li>
        </ul>
        <details v-if="plan.skipped.length">
          <summary class="cursor-pointer text-xs text-(--ui-text-muted)">{{ t('data.skipped', { n: plan.skipped.length }) }}</summary>
          <ul class="mt-1 space-y-0.5 text-xs">
            <li v-for="it in plan.skipped" :key="it.id">{{ it.title || t('chat.newThreadTitle') }} — <span class="text-(--ui-text-muted)">{{ it.reason }}</span></li>
          </ul>
        </details>
      </div>

      <div v-if="!status?.running && status?.finished_at" class="text-xs text-(--ui-text-muted)">
        {{ t('data.lastRun', { at: when(status.finished_at), n: status.cleaned, size: fmtBytes(status.bytes) }) }}
        <details v-if="status.failed.length" class="mt-1">
          <summary class="cursor-pointer">{{ t('data.skipped', { n: status.failed.length }) }}</summary>
          <ul class="mt-1 space-y-0.5">
            <li v-for="it in status.failed" :key="it.id">{{ it.title || t('chat.newThreadTitle') }} — {{ it.reason }}</li>
          </ul>
        </details>
      </div>
    </UCard>

    <!-- cleaning on its own -->
    <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
      <div class="flex items-center gap-2">
        <p class="flex flex-1 items-center gap-2 text-sm font-semibold"><UIcon name="i-lucide-calendar-clock" class="size-4" />{{ t('data.autoTitle') }}</p>
        <USwitch v-model="auto.enabled" />
      </div>
      <div class="grid gap-3 sm:grid-cols-3">
        <UFormField :label="t('data.olderThan')">
          <div class="flex items-center gap-2"><UInputNumber v-model="auto.days" :min="1" class="w-32" /><span class="text-sm text-(--ui-text-muted)">{{ t('data.days') }}</span></div>
        </UFormField>
        <UFormField :label="t('data.levelLabel')">
          <USelect v-model="auto.level" :items="LEVELS.map(l => ({ label: levelLabel(l), value: l }))" class="w-full" />
        </UFormField>
        <UFormField :label="t('data.kinds')">
          <div class="flex flex-wrap gap-3 pt-1.5">
            <UCheckbox v-for="k in KINDS" :key="k" :model-value="auto.kinds.includes(k)" :label="kindLabel(k)"
              @update:model-value="(v: boolean | 'indeterminate') => { auto.kinds = v === true ? [...auto.kinds, k] : auto.kinds.filter(x => x !== k) }" />
          </div>
        </UFormField>
      </div>
      <p v-if="auto.enabled && auto.level === 'summary'" class="flex items-start gap-1.5 text-xs text-(--ui-warning)"><UIcon name="i-lucide-info" class="mt-0.5 size-4 shrink-0" />{{ t('data.autoSummaryCost') }}</p>
      <div class="flex flex-wrap items-center gap-3">
        <UButton size="sm" icon="i-lucide-save" :label="t('common.save')" :loading="savingAuto" :disabled="!auto.kinds.length" @click="saveAuto" />
        <span v-if="data?.auto.last_run" class="text-xs text-(--ui-text-muted)">{{ t('data.autoLast', { at: when(data.auto.last_run), msg: data.auto.last_msg ?? '' }) }}</span>
      </div>
    </UCard>

    <UModal v-model:open="confirmOpen" :title="t('data.confirmTitle')">
      <template #body>
        <p class="text-sm">{{ t('data.confirmBody', { n: plan?.count ?? 0, level: levelLabel(form.level), size: fmtBytes(plan?.bytes ?? 0) }) }}</p>
        <p v-if="form.level === 'delete'" class="mt-2 text-sm text-(--ui-error)">{{ t('data.confirmDelete') }}</p>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="confirmOpen = false" />
          <UButton :color="form.level === 'delete' ? 'error' : 'primary'" icon="i-lucide-eraser" :label="t('data.cleanN', { n: plan?.count ?? 0 })" @click="clean" />
        </div>
      </template>
    </UModal>
  </div>
</template>
