<script setup lang="ts">
// Docker Compose services of a project: state, ports, CPU/RAM, start/stop, logs.
import type { Attachment } from './PromptInput.vue'

interface Port { url: string, published: number, target: number, protocol: string }
interface Stats { cpu: string, mem: string, mem_perc: string, net: string, block: string, pids: string }
interface Container { id: string, name: string, image: string, state: string, status: string, health?: string, ports: Port[], stats?: Stats }
interface Service { name: string, container?: Container }
interface ActionState { status: string, exit_code?: number, started_at?: string }
interface View { docker: { available: boolean, version?: string, error?: string }, files: string[], file: string, services: Service[], action?: ActionState }

const props = defineProps<{ projectId: string }>()
const emit = defineEmits<{ askAgent: [text: string, files: Attachment[], send?: boolean] }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()

const file = ref('')
// Render right away: first a fast load without stats, then keep polling with
// CPU/RAM (docker stats samples for a moment, so it never blocks the view).
const data = ref<View | null>(null)
const loadError = ref('')
let loading = false
async function load(withStats = true) {
  if (loading) return
  loading = true
  try {
    const v = await $fetch<View>(`/api/projects/${props.projectId}/compose`, { query: { file: file.value, stats: withStats ? '1' : '0' } })
    if (!withStats && data.value) { // keep the last numbers until fresh ones arrive
      const old = new Map(data.value.services.map(s => [s.name, s.container?.stats]))
      for (const sv of v.services) if (sv.container && !sv.container.stats) sv.container.stats = old.get(sv.name)
    }
    data.value = v
    loadError.value = ''
    if (!file.value && v.file) file.value = v.file
  } catch (e) {
    loadError.value = apiError(e)
  } finally {
    loading = false
  }
}
const refresh = () => load(true)
const services = computed(() => data.value?.services ?? [])
const running = computed(() => services.value.filter(s => s.container?.state === 'running').length)
const actionRunning = computed(() => data.value?.action?.status === 'running')

// poll only while visible (the panel is kept alive when switching sections)
let poll: ReturnType<typeof setTimeout> | undefined
function schedule() {
  clearTimeout(poll)
  poll = setTimeout(async () => { await load(true); schedule() }, actionRunning.value ? 2000 : 5000)
}
async function start() {
  if (!data.value) await load(false)
  load(true)
  schedule()
}
onMounted(start)
onActivated(start)
onDeactivated(() => clearTimeout(poll))
onBeforeUnmount(() => clearTimeout(poll))
watch(file, (f, old) => { if (old) load(false) })

// what the log pane shows: a service's logs, or the last docker action's output
const view = ref<{ kind: 'service', name: string } | { kind: 'action' } | null>(null)
watch(services, (list) => {
  if (!view.value && list[0]) view.value = { kind: 'service', name: list.find(s => s.container?.state === 'running')?.name ?? list[0].name }
}, { immediate: true })
const logUrl = computed(() => {
  const v = view.value
  if (!v) return null
  if (v.kind === 'action') return `/api/projects/${props.projectId}/compose/action/stream`
  return `/api/projects/${props.projectId}/compose/logs?file=${encodeURIComponent(file.value)}&service=${encodeURIComponent(v.name)}`
})

const busy = ref('')
async function act(action: string, service = '') {
  if (action === 'down' && !service && !confirm(t('compose.confirmDown'))) return
  busy.value = action + service
  try {
    await $fetch(`/api/projects/${props.projectId}/compose/action`, { method: 'POST', body: { file: file.value, action, service } })
    view.value = { kind: 'action' }
    setTimeout(refresh, 800)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
async function askAgent(s: Service, fix = false) {
  try {
    const att = await $fetch<Attachment>(`/api/projects/${props.projectId}/compose/log-attachment`, { method: 'POST', body: { file: file.value, service: s.name } })
    const why = s.container ? t('compose.reasonRunning', { status: s.container.status }) : t('compose.reasonNotRunning')
    const text = fix
      ? t('compose.askFixPrompt', { name: s.name, file: file.value, why })
      : t('compose.askPrompt', { name: s.name, file: file.value, why })
    emit('askAgent', text, [att], fix)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

function stateOf(s: Service) {
  const c = s.container
  if (!c) return { label: t('compose.state.notCreated'), color: 'neutral' as const, dot: 'bg-(--ui-text-dimmed)' }
  if (c.state === 'running') {
    if (c.health === 'unhealthy') return { label: t('compose.state.unhealthy'), color: 'error' as const, dot: 'bg-(--ui-error)' }
    if (c.health === 'starting') return { label: t('compose.state.starting'), color: 'warning' as const, dot: 'bg-(--ui-warning)' }
    return { label: t('compose.state.running'), color: 'success' as const, dot: 'bg-(--ui-success)' }
  }
  if (c.state === 'restarting') return { label: t('compose.state.restarting'), color: 'warning' as const, dot: 'bg-(--ui-warning)' }
  if (c.state === 'exited' && !/\(0\)/.test(c.status)) return { label: t('compose.state.error'), color: 'error' as const, dot: 'bg-(--ui-error)' }
  return { label: t('compose.state.stopped'), color: 'neutral' as const, dot: 'bg-(--ui-text-dimmed)' }
}
const stackMenu = computed(() => [[
  { label: t('compose.menu.pull'), icon: 'i-lucide-download', onSelect: () => act('pull') },
  { label: t('compose.menu.build'), icon: 'i-lucide-hammer', onSelect: () => act('build') },
  { label: t('compose.menu.viewOutput'), icon: 'i-lucide-terminal', onSelect: () => { view.value = { kind: 'action' } } }
], [
  { label: t('compose.menu.down'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => act('down') }
]])
</script>

<template>
  <div class="space-y-4">
    <template v-if="!data">
      <div class="flex items-center gap-2">
        <slot name="nav" />
        <span v-if="!loadError" class="flex items-center gap-1.5 text-sm text-(--ui-text-muted)"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> {{ t('compose.loading') }}</span>
      </div>
      <UAlert v-if="loadError" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="loadError" />
      <div v-else class="grid gap-4 lg:grid-cols-[minmax(0,24rem)_minmax(0,1fr)]">
        <div class="space-y-2">
          <div v-for="i in 3" :key="i" class="h-24 animate-pulse rounded-lg border border-(--ui-border) bg-(--ui-bg-elevated)/50" />
        </div>
        <div class="h-[26rem] max-h-[60vh] animate-pulse rounded-lg border border-(--ui-border) bg-neutral-950" />
      </div>
    </template>

    <slot v-else-if="!data.files.length" name="nav" />
    <div v-if="data && !data.files.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
      <UIcon name="i-lucide-container" class="mx-auto size-8 text-(--ui-text-dimmed)" />
      <p class="mt-2 font-medium">{{ t('compose.noFile.title') }}</p>
      <p class="text-sm text-(--ui-text-muted)">{{ t('compose.noFile.desc') }}</p>
    </div>

    <template v-else-if="data">
      <div class="flex flex-wrap items-center gap-2">
        <slot name="nav" />
        <USelect v-if="data.files.length > 1" v-model="file" :items="data.files" size="sm" class="w-48" />
        <span v-if="services.length" class="text-sm text-(--ui-text-muted)" :title="`${data.file} · Docker ${data.docker.version ?? '?'}`">{{ t('compose.running', { running, total: services.length }) }}</span>
        <UBadge v-if="actionRunning" color="info" variant="subtle" icon="i-lucide-loader-circle" :label="t('compose.actionRunning')" :ui="{ leadingIcon: 'animate-spin' }" />
        <div v-if="isAdmin && data.docker.available" class="ms-auto flex gap-2">
          <UButton size="sm" icon="i-lucide-play" :label="t('compose.upStack')" :loading="busy === 'up'" :disabled="actionRunning" @click="act('up')" />
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-square" :label="t('compose.stopStack')" :loading="busy === 'stop'" :disabled="actionRunning" @click="act('stop')" />
          <UDropdownMenu :items="stackMenu" :content="{ align: 'end' }">
            <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis" :aria-label="t('compose.otherActions')" :disabled="actionRunning" />
          </UDropdownMenu>
        </div>
      </div>

      <UAlert v-if="data.docker.error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('compose.dockerErrorTitle')" :description="data.docker.error" />

      <div v-if="services.length" class="grid gap-4 lg:grid-cols-[minmax(0,24rem)_minmax(0,1fr)]">
        <div class="space-y-2">
          <div
            v-for="s in services" :key="s.name" role="button" tabindex="0"
            class="rounded-lg border p-3 transition"
            :class="view?.kind === 'service' && view.name === s.name ? 'border-(--ui-primary) bg-(--ui-primary)/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
            @click="view = { kind: 'service', name: s.name }" @keydown.enter="view = { kind: 'service', name: s.name }"
          >
            <div class="flex items-center gap-2">
              <span class="size-2 shrink-0 rounded-full" :class="[stateOf(s).dot, { 'animate-pulse': s.container?.state === 'running' }]" />
              <p class="min-w-0 flex-1 truncate font-medium">{{ s.name }}</p>
              <UBadge :color="stateOf(s).color" variant="subtle" size="sm" :label="stateOf(s).label" />
            </div>
            <p v-if="s.container" class="mt-1 truncate text-xs text-(--ui-text-muted)">{{ s.container.image }} · {{ s.container.status }}</p>
            <div v-if="s.container?.stats" class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-(--ui-text-muted) tabular-nums">
              <span>CPU {{ s.container.stats.cpu }}</span>
              <span>RAM {{ s.container.stats.mem.split(' / ')[0] }}</span>
              <span>{{ t('compose.net') }} {{ s.container.stats.net }}</span>
            </div>
            <div v-if="s.container?.ports.length" class="mt-1 flex flex-wrap gap-2 text-xs">
              <a
                v-for="p in s.container.ports" :key="p.published" :href="`http://localhost:${p.published}`" target="_blank" rel="noopener"
                class="text-(--ui-primary)" @click.stop
              >:{{ p.published }} → {{ p.target }} ↗</a>
            </div>
            <div class="mt-2 flex items-center gap-1" @click.stop>
              <template v-if="isAdmin && data.docker.available">
                <UButton
                  v-if="s.container?.state !== 'running'" size="xs" icon="i-lucide-play" :label="t('compose.up')"
                  :loading="busy === 'up' + s.name" :disabled="actionRunning" @click="act('up', s.name)"
                />
                <template v-else>
                  <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-square" :label="t('compose.stop')" :loading="busy === 'stop' + s.name" :disabled="actionRunning" @click="act('stop', s.name)" />
                  <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-rotate-cw" :aria-label="t('compose.restart')" :loading="busy === 'restart' + s.name" :disabled="actionRunning" @click="act('restart', s.name)" />
                </template>
              </template>
              <UButton
                v-if="stateOf(s).color === 'error'" size="xs" color="error" variant="soft"
                icon="i-lucide-wrench" :label="t('compose.fix')" class="ms-auto" @click="askAgent(s, true)"
              />
              <UButton
                v-else-if="s.container" size="xs" color="neutral" variant="ghost"
                icon="i-lucide-bot" :label="t('compose.askAgent')" class="ms-auto" @click="askAgent(s)"
              />
            </div>
          </div>
        </div>

        <LogTerminal
          :url="logUrl"
          :title="view?.kind === 'action' ? t('compose.actionTitle') : view?.name ?? ''"
          :subtitle="view?.kind === 'action' ? (data.action ? t('compose.actionStatus', { status: data.action.status }) : '') : t('compose.serviceLogSubtitle')"
          :empty="view?.kind === 'action' ? t('compose.emptyActionLog') : t('compose.emptyServiceLog')"
        />
      </div>
    </template>
  </div>
</template>
