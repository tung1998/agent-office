<script setup lang="ts">
// The AI CLIs kept up to date (ADR-259): each installed CLI with its version,
// the newest one, the runs using it now and its update; an update waits for
// those runs (or goes now), a version that does not start is put back. The
// mode says what office does on its own: nothing, tell, or update when idle.
interface Upgrade {
  state: 'waiting' | 'updating' | 'checking' | 'rolling_back' | 'done' | 'rolled_back' | 'failed' | 'gave_up' | 'cancelled'
  when: 'idle' | 'now'
  waiting: number
  draining?: boolean // new runs held back while those running end
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
  latest_checking?: boolean // the server reads the newest version again: looked at soon
  update_command?: string
  in_use: number
  upgrade?: Upgrade
  used?: boolean // an AI connection runs it (ADR-279)
  // the copies the connections run, when not only the one first on PATH
  installs?: { path: string, version?: string, outdated?: boolean }[]
}
type Mode = 'off' | 'notify' | 'auto'
interface List {
  enabled: boolean
  tools: Tool[]
  // bad: per CLI, a version put back (it did not start): not updated to on its own
  auto_update?: { mode: Mode, tools?: Record<string, Mode>, bad?: Record<string, string> }
  alert_chat?: boolean // a chat is picked for alerts: new versions and broken installs can be told
}

const { t } = useLang()
const toast = useToast()
const data = ref<List | null>(null)
const loadError = ref(false)
const busy = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

const active = (u?: Upgrade) => !!u && ['waiting', 'updating', 'checking', 'rolling_back'].includes(u.state)
// only the CLIs an AI connection runs (ADR-279)
const installed = computed(() => (data.value?.tools ?? []).filter(x => x.installed && x.used !== false))
const outdated = computed(() => installed.value.filter(x => x.outdated).length)
// a CLI office tells about (notify or auto) with no chat to tell it in
const untold = computed(() => data.value?.alert_chat === false
  && installed.value.some(x => (auto.value.tools[x.id] ?? auto.value.mode) !== 'off'))
const badOf = (id: string) => data.value?.auto_update?.bad?.[id]

let unread = 0 // looks while the newest versions are read in the background
async function load() {
  clearTimeout(timer)
  try {
    data.value = await $fetch<List>('/api/cli-tools')
    // sync() itself ignores this while an edit is queued or in flight: a read
    // that started before that edit must not land after it and clobber it
    // back to what the server had before the edit was even sent
    autoQueue.sync({ mode: data.value.auto_update?.mode ?? 'notify', tools: { ...data.value.auto_update?.tools } })
    loadError.value = false
  } catch {
    loadError.value = true
    return
  }
  // an update on its way is followed until it ends; newest versions the
  // server is still reading (first look, or after 6h) are fetched again soon
  if (installed.value.some(x => active(x.upgrade))) timer = setTimeout(load, 3000)
  else if (installed.value.some(x => !x.latest || x.latest_checking) && unread++ < 5) timer = setTimeout(load, 4000)
  else unread = 0
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

// `auto` is the one reactive source of truth the mode selects read, so Vue
// re-renders them right away; AutoSaveQueue only reads/writes it through
// get/set closures (never owns it itself), so assigning `auto.value` here is
// what actually makes the UI update. auto_update is a full-replace PUT (the
// whole tools map, not a per-tool patch): AutoSaveQueue queues sends one at a
// time and always builds a send's body from the state *after* every edit
// made so far, so two mode changes fired before the first PUT's response
// lands can no longer have the later response silently drop the other's.
type Auto = { mode: Mode, tools: Record<string, Mode> }
const auto = ref<Auto>({ mode: 'notify', tools: {} })
const autoQueue = new AutoSaveQueue<Auto>(
  { mode: 'notify', tools: {} },
  () => auto.value,
  v => { auto.value = v },
  async (v) => {
    try {
      await $fetch('/api/cli-tools/auto-update', { method: 'PUT', body: v })
      toast.add({ title: t('cliup.saved'), color: 'success' })
    } catch (e) {
      toast.add({ title: apiError(e), color: 'error' })
      throw e
    }
  }
)
const mode = computed({
  get: () => auto.value.mode,
  set: (v: Mode) => {
    auto.value = { mode: v, tools: { ...auto.value.tools } }
    autoQueue.queue()
  }
})
function toolMode(id: string) {
  return auto.value.tools[id] ?? DEFAULT
}
function setToolMode(id: string, v: string) {
  const tools = { ...auto.value.tools }
  if (v === DEFAULT) delete tools[id]
  else tools[id] = v as Mode
  auto.value = { mode: auto.value.mode, tools }
  autoQueue.queue()
}

function upgradeText(u: Upgrade) {
  switch (u.state) {
    case 'waiting': return u.draining ? t('cliup.draining', { n: u.waiting }) : t('cliup.waiting', { n: u.waiting })
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
  <UAlert
    v-if="loadError && !data" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="t('cliup.loadError')"
    :actions="[{ label: t('common.refresh'), onClick: load }]"
  />
  <USkeleton v-else-if="!data" class="h-24 w-full" />
  <UCard v-else-if="data.enabled && installed.length" :ui="{ body: 'space-y-3 sm:p-4' }">
    <div class="flex flex-wrap items-center gap-2">
      <UIcon name="i-lucide-circle-arrow-up" class="size-4 text-(--ui-text-muted)" />
      <p class="font-medium">{{ t('cliup.title') }}</p>
      <InfoTip :text="t('cliup.help')" icon-class="size-4 text-(--ui-text-muted)" />
      <UBadge v-if="outdated" color="warning" variant="subtle" size="sm" :label="t('cliup.outdatedCount', { n: outdated })" />
      <USelect v-model="mode" :items="modeItems" size="sm" class="ms-auto w-44" :aria-label="t('cliup.mode')" />
    </div>
    <UAlert
      v-if="untold" color="warning" variant="subtle" icon="i-lucide-bell-off"
      :description="t('cliup.noAlertChat')" :ui="{ description: 'text-xs' }"
    />
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
        <ul v-if="x.installs?.length" class="space-y-0.5 ps-1">
          <li v-for="i in x.installs" :key="i.path" class="flex flex-wrap items-center gap-x-2 text-xs text-(--ui-text-muted)">
            <span class="min-w-0 break-all font-mono">{{ i.path }}</span>
            <span class="font-mono">{{ i.version }}</span>
            <UBadge v-if="i.outdated" color="warning" variant="subtle" size="sm" :label="t('cliup.newer', { v: x.latest ?? '' })" />
          </li>
        </ul>
        <p v-if="badOf(x.id)" class="flex items-start gap-1.5 text-xs text-(--ui-text-muted)">
          <UIcon name="i-lucide-undo-2" class="mt-0.5 size-3.5 shrink-0" />
          <span class="min-w-0 break-words">{{ t('cliup.bad', { v: badOf(x.id) ?? '' }) }}</span>
        </p>
        <p v-if="x.upgrade && recent(x.upgrade)" class="flex items-start gap-1.5 text-xs" :class="upgradeColor(x.upgrade)">
          <UIcon v-if="active(x.upgrade)" name="i-lucide-loader-circle" class="mt-0.5 size-3.5 shrink-0 animate-spin" />
          <span class="min-w-0 break-words">{{ upgradeText(x.upgrade) }}</span>
        </p>
      </li>
    </ul>
  </UCard>
</template>
