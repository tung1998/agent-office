<script setup lang="ts">
// One command of a project followed in a box (the Vận hành tab, one process):
// its state, start/stop/restart, CPU/RAM over the last minutes and, if
// asked, its console. Lays out by the box: a line when tiny, the console
// below the facts when tall, beside them when wide and short.
import type { WatchTone } from '../Status.vue'

interface ProcState { status: 'stopped' | 'running' | 'stopping' | 'exited' | 'crashed' | 'restarting', pid?: number, started_at?: string, finished_at?: string, exit_code?: number, restarts: number, port?: number, cpu: number, mem_bytes: number }
interface Proc { id: string, name: string, command: string, cwd: string, kind: 'service' | 'job', state: ProcState }

const { t } = useLang()
const toast = useToast()
const { projectId, config, set, size, narrow, tiny, short } = useWatchBox()
const { manages } = useAuth()
const isAdmin = computed(() => manages(projectId.value)) // its project's admin or manager (ADR-224)

const { data, error, refresh } = useLiveFetch<{ processes: Proc[] }>(() => `/api/projects/${projectId.value}/processes`, { lazy: true, server: false })
const procs = computed(() => data.value?.processes ?? [])
const proc = computed(() => procs.value.find(p => p.id === config.value.processId) ?? null)
const procItems = computed(() => procs.value.map(p => ({ label: p.name, value: p.id })))
// CPU/RAM move without a change event: a sample every 3s while metrics show
useWatchPoll(() => refresh(), () => (config.value.metrics ? 3 : 0))

// the last minutes of CPU/RAM, kept in the box (the API gives only "now")
const cpuHist = ref<number[]>([])
const memHist = ref<number[]>([])
watch(() => proc.value?.state, (s) => {
  if (!s || s.status !== 'running') return
  cpuHist.value = [...cpuHist.value, s.cpu].slice(-40)
  memHist.value = [...memHist.value, s.mem_bytes].slice(-40)
})
watch(() => config.value.processId, () => { cpuHist.value = []; memHist.value = [] })

const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => { clock = setInterval(() => { now.value = Date.now() }, 1000) })
onBeforeUnmount(() => clearInterval(clock))
const uptime = computed(() => {
  const s = proc.value?.state
  return s?.status === 'running' && s.started_at ? fmtUptime((now.value - new Date(s.started_at).getTime()) / 1000) : ''
})

const tone = computed<WatchTone>(() => ({ running: 'ok', stopped: 'idle', stopping: 'busy', exited: 'idle', crashed: 'bad', restarting: 'busy' } as const)[proc.value?.state.status ?? 'stopped'])
const stateLabel = computed(() => {
  const s = proc.value?.state
  if (!s) return ''
  const l = t(`ops.status.${s.status}`)
  return s.status === 'crashed' || s.status === 'exited' ? `${l} (${s.exit_code ?? '?'})` : l
})
const live = computed(() => ['running', 'stopping', 'restarting'].includes(proc.value?.state.status ?? ''))

const busy = ref('')
async function act(action: 'start' | 'stop' | 'restart') {
  if (!proc.value) return
  busy.value = action
  try {
    await $fetch(`/api/processes/${proc.value.id}/${action}`, { method: 'POST' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

// the console beside the facts when the box is wide but short, below otherwise
const side = computed(() => size.value.w >= 720 && size.value.h < 420)
const showConsole = computed(() => !!config.value.console && !tiny.value)
const opsLink = computed(() => ({ path: `/projects/${projectId.value}`, query: { tab: 'ops' } }))
</script>

<template>
  <div class="h-full min-h-0">
    <div v-if="!data && !error" class="space-y-2 p-3"><USkeleton class="h-6 w-1/2" /><USkeleton class="h-16 w-full" /></div>
    <div v-else-if="error" class="p-3">
      <UAlert color="error" variant="subtle" :title="apiError(error)" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    </div>
    <WatchEmpty v-else-if="!procs.length" icon="i-lucide-square-terminal" :text="t('watchW.process.none')">
      <UButton size="sm" color="neutral" variant="soft" icon="i-lucide-arrow-up-right" :label="t('watchW.openOps')" :to="opsLink" />
    </WatchEmpty>
    <WatchEmpty v-else-if="!proc" icon="i-lucide-square-terminal" :text="t('watchW.process.pick')">
      <USelect :model-value="(config.processId as string) || undefined" :items="procItems" :placeholder="t('watchW.process.pickShort')" class="w-full" @update:model-value="(v) => set('processId', v)" />
    </WatchEmpty>

    <!-- tiny: one line, the state and the main button -->
    <div v-else-if="tiny" class="flex h-full items-center gap-2 px-3">
      <WatchStatus :tone="tone" :label="stateLabel" />
      <span class="min-w-0 flex-1 truncate text-sm font-medium">{{ proc.name }}</span>
      <template v-if="config.controls && isAdmin">
        <UButton v-if="!live" size="xs" icon="i-lucide-play" :aria-label="t('ops.start')" :loading="busy === 'start'" @click="act('start')" />
        <UButton v-else size="xs" color="neutral" variant="outline" icon="i-lucide-square" :aria-label="t('ops.stop')" :loading="busy === 'stop'" @click="act('stop')" />
      </template>
    </div>

    <div v-else class="flex h-full min-h-0 gap-2 p-2" :class="side ? 'flex-row' : 'flex-col'">
      <!-- the facts -->
      <div class="flex shrink-0 flex-col gap-2" :class="side ? 'w-72 overflow-y-auto' : ''">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
          <WatchStatus :tone="tone" :label="stateLabel" :size="!showConsole && !short ? 'lg' : 'sm'" />
          <a v-if="proc.state.port && proc.state.status === 'running'" :href="`http://localhost:${proc.state.port}`" target="_blank" rel="noopener" class="text-xs text-(--ui-primary) tabular-nums">:{{ proc.state.port }} ↗</a>
          <div v-if="config.controls && isAdmin" class="ms-auto flex items-center gap-1">
            <UButton v-if="!live" size="xs" icon="i-lucide-play" :label="narrow ? undefined : t('ops.start')" :aria-label="t('ops.start')" :loading="busy === 'start'" @click="act('start')" />
            <template v-else>
              <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-square" :label="narrow ? undefined : t('ops.stop')" :aria-label="t('ops.stop')" :loading="busy === 'stop' || proc.state.status === 'stopping'" @click="act('stop')" />
              <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-rotate-cw" :aria-label="t('ops.restart')" :title="t('ops.restart')" :loading="busy === 'restart'" @click="act('restart')" />
            </template>
          </div>
        </div>
        <code v-if="!short || side" class="block truncate text-xs text-(--ui-text-muted)" :title="proc.command">$ {{ proc.command }}</code>
        <template v-if="config.metrics && proc.state.status === 'running' && !(short && showConsole && !side)">
          <div class="grid gap-1.5" :class="narrow ? 'grid-cols-2' : 'grid-cols-4'">
            <WatchTile :label="t('watchW.uptime')" :value="uptime" icon="i-lucide-timer" />
            <WatchTile label="CPU" :value="`${proc.state.cpu.toFixed(1)}%`" icon="i-lucide-cpu" />
            <WatchTile label="RAM" :value="fmtBytes(proc.state.mem_bytes)" icon="i-lucide-memory-stick" />
            <WatchTile :label="t('watchW.restarts')" :value="String(proc.state.restarts)" icon="i-lucide-rotate-cw" />
          </div>
          <div v-if="!short && cpuHist.length > 1" class="grid gap-3" :class="narrow ? 'grid-cols-1' : 'grid-cols-2'">
            <div><p class="text-[11px] text-(--ui-text-dimmed)">CPU</p><WatchSparkline :values="cpuHist" label="CPU" /></div>
            <div><p class="text-[11px] text-(--ui-text-dimmed)">RAM</p><WatchSparkline :values="memHist" label="RAM" /></div>
          </div>
        </template>
      </div>
      <!-- the console -->
      <LogTerminal
        v-if="showConsole" fill class="min-h-0 min-w-0 flex-1" :url="`/api/processes/${proc.id}/stream`" :title="proc.name"
        :subtitle="narrow ? undefined : proc.cwd" :empty="t('ops.emptyLog')"
      />
    </div>
  </div>
</template>
