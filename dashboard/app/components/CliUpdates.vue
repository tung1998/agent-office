<script setup lang="ts">
// The AI CLIs kept up to date (ADR-259): each installed CLI with its version,
// the newest one, the runs using it now and its update; an update waits for
// those runs (or goes now), a version that does not start is put back. The
// mode says what office does on its own: nothing, tell, or update when idle.
interface Upgrade {
  state: 'waiting' | 'updating' | 'checking' | 'rolling_back' | 'done' | 'rolled_back' | 'failed' | 'gave_up' | 'cancelled'
  when: 'idle' | 'now'
  waiting: number
  from?: string
  to?: string
  note?: string
  ended_at?: string
}
interface Tool {
  id: string
  name: string
  installed: boolean
  version?: string
  latest?: string
  outdated?: boolean
  update_command?: string
  in_use: number
  upgrade?: Upgrade
}
type Mode = 'off' | 'notify' | 'auto'
interface List { enabled: boolean, tools: Tool[], auto_update?: { mode: Mode, tools?: Record<string, Mode> } }

const { t } = useLang()
const toast = useToast()
const data = ref<List | null>(null)
const busy = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

const active = (u?: Upgrade) => !!u && ['waiting', 'updating', 'checking', 'rolling_back'].includes(u.state)
const installed = computed(() => (data.value?.tools ?? []).filter(x => x.installed))
const outdated = computed(() => installed.value.filter(x => x.outdated).length)

let unread = 0 // looks while the newest versions are read in the background
async function load() {
  clearTimeout(timer)
  try {
    data.value = await $fetch<List>('/api/cli-tools')
  } catch {
    return
  }
  // an update on its way is followed until it ends; newest versions the
  // server is still reading (first look, or after 6h) are fetched again soon
  if (installed.value.some(x => active(x.upgrade))) timer = setTimeout(load, 3000)
  else if (installed.value.some(x => !x.latest) && unread++ < 5) timer = setTimeout(load, 4000)
}

async function update(tool: Tool, when: 'idle' | 'now') {
  busy.value = tool.id
  if (when === 'now') {
    // the runs using it now, not those of the last look
    const inUse = await $fetch<Tool>(`/api/cli-tools/${tool.id}`).then(x => x.in_use, () => tool.in_use)
    if (inUse > 0 && !confirm(t('cliup.confirmNow', { name: tool.name, n: inUse }))) {
      busy.value = ''
      return
    }
  }
  try {
    await $fetch(`/api/cli-tools/${tool.id}/update`, { method: 'POST', body: { when } })
    await load()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

async function cancel(tool: Tool) {
  try {
    await $fetch(`/api/cli-tools/${tool.id}/update/cancel`, { method: 'POST' })
    await load()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

const modeItems = computed(() => [
  { value: 'off', label: t('cliup.modeOff') },
  { value: 'notify', label: t('cliup.modeNotify') },
  { value: 'auto', label: t('cliup.modeAuto') }
])
const DEFAULT = '__default'
const toolModeItems = computed(() => [{ value: DEFAULT, label: t('cliup.modeDefault') }, ...modeItems.value])

async function saveMode(mode: Mode, tools: Record<string, Mode>) {
  try {
    const auto = await $fetch<List['auto_update']>('/api/cli-tools/auto-update', { method: 'PUT', body: { mode, tools } })
    if (data.value) data.value.auto_update = auto
    toast.add({ title: t('cliup.saved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const mode = computed({
  get: () => data.value?.auto_update?.mode ?? 'notify',
  set: (v: Mode) => saveMode(v, { ...data.value?.auto_update?.tools })
})
function toolMode(id: string) {
  return data.value?.auto_update?.tools?.[id] ?? DEFAULT
}
function setToolMode(id: string, v: string) {
  const tools = { ...data.value?.auto_update?.tools }
  if (v === DEFAULT) delete tools[id]
  else tools[id] = v as Mode
  saveMode(mode.value, tools)
}

function upgradeText(u: Upgrade) {
  switch (u.state) {
    case 'waiting': return t('cliup.waiting', { n: u.waiting })
    case 'updating': case 'checking': return t('cliup.updating')
    case 'rolling_back': return t('cliup.rollingBack', { v: u.from ?? '' })
    case 'done': return u.from && u.to && u.from !== u.to ? t('cliup.done', { from: u.from, to: u.to }) : t('cliup.doneSame')
    case 'rolled_back': return t('cliup.rolledBack', { v: u.from ?? '' })
    case 'gave_up': return t('cliup.gaveUp')
    case 'cancelled': return t('cliup.cancelled')
  }
  return t('cliup.failed', { note: u.note ?? '' })
}
function upgradeColor(u: Upgrade) {
  if (active(u)) return 'text-(--ui-text-muted)'
  if (u.state === 'done') return 'text-(--ui-success)'
  if (u.state === 'cancelled') return 'text-(--ui-text-muted)'
  return 'text-(--ui-warning)'
}
// an ended update is shown for a while, then the row is quiet again
const recent = (u?: Upgrade) => !!u && (active(u) || (!!u.ended_at && Date.now() - Date.parse(u.ended_at) < 6 * 3600_000))

onMounted(load)
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <USkeleton v-if="!data" class="h-24 w-full" />
  <UCard v-else-if="data.enabled && installed.length" :ui="{ body: 'space-y-3 sm:p-4' }">
    <div class="flex flex-wrap items-center gap-2">
      <UIcon name="i-lucide-circle-arrow-up" class="size-4 text-(--ui-text-muted)" />
      <p class="font-medium">{{ t('cliup.title') }}</p>
      <InfoTip :text="t('cliup.help')" icon-class="size-4 text-(--ui-text-muted)" />
      <UBadge v-if="outdated" color="warning" variant="subtle" size="sm" :label="t('cliup.outdatedCount', { n: outdated })" />
      <USelect v-model="mode" :items="modeItems" size="sm" class="ms-auto w-44" :aria-label="t('cliup.mode')" />
    </div>
    <ul class="divide-y divide-(--ui-border) rounded-md border border-(--ui-border)">
      <li v-for="x in installed" :key="x.id" class="space-y-1 px-3 py-2">
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <span class="font-medium">{{ x.name }}</span>
          <span class="font-mono text-xs text-(--ui-text-muted)">{{ x.version }}</span>
          <UBadge v-if="x.outdated" color="warning" variant="subtle" size="sm" :label="t('cliup.newer', { v: x.latest ?? '' })" />
          <span v-if="x.in_use" class="text-xs text-(--ui-text-muted)">{{ t('cliup.inUse', { n: x.in_use }) }}</span>
          <div class="ms-auto flex items-center gap-1">
            <USelect
              :model-value="toolMode(x.id)" :items="toolModeItems" size="xs" class="w-36" :aria-label="t('cliup.modeOf', { name: x.name })"
              @update:model-value="(v: string) => setToolMode(x.id, v)"
            />
            <template v-if="x.update_command && !active(x.upgrade)">
              <UButton
                size="xs" color="neutral" variant="ghost" icon="i-lucide-clock" :label="t('cliup.whenIdle')"
                :title="x.update_command" :loading="busy === x.id" @click="update(x, 'idle')"
              />
              <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-zap" :label="t('cliup.now')" :title="x.update_command" @click="update(x, 'now')" />
            </template>
            <UButton
              v-else-if="x.upgrade?.state === 'waiting'" size="xs" color="neutral" variant="ghost" icon="i-lucide-x"
              :label="t('cli.cancel')" @click="cancel(x)"
            />
          </div>
        </div>
        <p v-if="x.upgrade && recent(x.upgrade)" class="flex items-start gap-1.5 text-xs" :class="upgradeColor(x.upgrade)">
          <UIcon v-if="active(x.upgrade)" name="i-lucide-loader-circle" class="mt-0.5 size-3.5 shrink-0 animate-spin" />
          <span class="min-w-0 break-words">{{ upgradeText(x.upgrade) }}</span>
        </p>
      </li>
    </ul>
  </UCard>
</template>
