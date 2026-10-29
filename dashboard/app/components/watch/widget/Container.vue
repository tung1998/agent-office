<script setup lang="ts">
// One Docker Compose service of a project in a box: its container's state,
// up/stop/restart, CPU/RAM and, if asked, its logs.
import type { WatchTone } from '../Status.vue'

interface Stats { cpu: string, mem: string, mem_perc: string, net: string, block: string, pids: string }
interface Container { id: string, name: string, image: string, state: string, status: string, health?: string, ports: { url: string, published: number }[], stats?: Stats }
interface View { docker: { available: boolean, error?: string }, file: string, services: { name: string, container?: Container }[] }

const { t } = useLang()
const toast = useToast()
const { projectId, config, set, size, narrow, tiny, short } = useWatchBox()
const { manages } = useAuth()
const isAdmin = computed(() => manages(projectId.value)) // its project's admin or manager (ADR-224)

const { data, error, refresh } = useLiveFetch<View>(() => `/api/projects/${projectId.value}/compose`, { query: { stats: 1 }, lazy: true, server: false })
const services = computed(() => data.value?.services ?? [])
const svc = computed(() => services.value.find(s => s.name === config.value.service) ?? null)
const c = computed(() => svc.value?.container)
const svcItems = computed(() => services.value.map(s => ({ label: s.name, value: s.name })))
// docker stats move without a change event
useWatchPoll(() => refresh(), () => (config.value.metrics ? 5 : 0))

const cpuHist = ref<number[]>([])
watch(() => c.value?.stats?.cpu, (v) => {
  if (v) cpuHist.value = [...cpuHist.value, Number.parseFloat(v) || 0].slice(-40)
})
watch(() => config.value.service, () => { cpuHist.value = [] })

const state = computed<{ tone: WatchTone, label: string }>(() => {
  const x = c.value
  if (!x) return { tone: 'idle', label: t('compose.state.notCreated') }
  if (x.state === 'running') {
    if (x.health === 'unhealthy') return { tone: 'bad', label: t('compose.state.unhealthy') }
    if (x.health === 'starting') return { tone: 'busy', label: t('compose.state.starting') }
    return { tone: 'ok', label: t('compose.state.running') }
  }
  if (x.state === 'restarting') return { tone: 'busy', label: t('compose.state.restarting') }
  if (x.state === 'exited' && !/\(0\)/.test(x.status)) return { tone: 'bad', label: t('compose.state.error') }
  return { tone: 'idle', label: t('compose.state.stopped') }
})
const running = computed(() => c.value?.state === 'running')

const busy = ref('')
async function act(action: 'up' | 'stop' | 'restart') {
  if (!svc.value) return
  busy.value = action
  try {
    await $fetch(`/api/projects/${projectId.value}/compose/action`, { method: 'POST', body: { file: data.value?.file ?? '', action, service: svc.value.name } })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

const side = computed(() => size.value.w >= 720 && size.value.h < 420)
const showConsole = computed(() => !!config.value.console && !tiny.value)
const logUrl = computed(() => svc.value ? `/api/projects/${projectId.value}/compose/logs?file=${encodeURIComponent(data.value?.file ?? '')}&service=${encodeURIComponent(svc.value.name)}` : null)
const opsLink = computed(() => ({ path: `/projects/${projectId.value}`, query: { tab: 'ops', section: 'containers' } }))
</script>

<template>
  <div class="h-full min-h-0">
    <div v-if="!data && !error" class="space-y-2 p-3"><USkeleton class="h-6 w-1/2" /><USkeleton class="h-16 w-full" /></div>
    <div v-else-if="error" class="p-3">
      <UAlert color="error" variant="subtle" :title="apiError(error)" :actions="[{ label: t('common.refresh'), onClick: () => refresh() }]" />
    </div>
    <WatchEmpty v-else-if="!data!.docker.available" icon="i-lucide-container" :text="data!.docker.error || t('watchW.container.noDocker')" />
    <WatchEmpty v-else-if="!services.length" icon="i-lucide-container" :text="t('watchW.container.none')">
      <UButton size="sm" color="neutral" variant="soft" icon="i-lucide-arrow-up-right" :label="t('watchW.openOps')" :to="opsLink" />
    </WatchEmpty>
    <WatchEmpty v-else-if="!svc" icon="i-lucide-container" :text="t('watchW.container.pick')">
      <USelect :model-value="(config.service as string) || undefined" :items="svcItems" :placeholder="t('watchW.container.pickShort')" class="w-full" @update:model-value="(v) => set('service', v)" />
    </WatchEmpty>

    <div v-else-if="tiny" class="flex h-full items-center gap-2 px-3">
      <WatchStatus :tone="state.tone" :label="state.label" />
      <span class="min-w-0 flex-1 truncate text-sm font-medium">{{ svc.name }}</span>
      <template v-if="config.controls && isAdmin">
        <UButton v-if="!running" size="xs" icon="i-lucide-play" :aria-label="t('compose.up')" :loading="busy === 'up'" @click="act('up')" />
        <UButton v-else size="xs" color="neutral" variant="outline" icon="i-lucide-square" :aria-label="t('ops.stop')" :loading="busy === 'stop'" @click="act('stop')" />
      </template>
    </div>

    <div v-else class="flex h-full min-h-0 gap-2 p-2" :class="side ? 'flex-row' : 'flex-col'">
      <div class="flex shrink-0 flex-col gap-2" :class="side ? 'w-72 overflow-y-auto' : ''">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
          <WatchStatus :tone="state.tone" :label="state.label" :size="!showConsole && !short ? 'lg' : 'sm'" />
          <a v-for="p in c?.ports ?? []" :key="p.url" :href="p.url" target="_blank" rel="noopener" class="text-xs text-(--ui-primary) tabular-nums">:{{ p.published }} ↗</a>
          <div v-if="config.controls && isAdmin" class="ms-auto flex items-center gap-1">
            <UButton v-if="!running" size="xs" icon="i-lucide-play" :label="narrow ? undefined : t('compose.up')" :aria-label="t('compose.up')" :loading="busy === 'up'" @click="act('up')" />
            <template v-else>
              <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-square" :label="narrow ? undefined : t('ops.stop')" :aria-label="t('ops.stop')" :loading="busy === 'stop'" @click="act('stop')" />
              <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-rotate-cw" :aria-label="t('ops.restart')" :title="t('ops.restart')" :loading="busy === 'restart'" @click="act('restart')" />
            </template>
          </div>
        </div>
        <p v-if="c && (!short || side)" class="truncate text-xs text-(--ui-text-muted)" :title="c.image">{{ c.image }} · {{ c.status }}</p>
        <template v-if="config.metrics && c?.stats && !(short && showConsole && !side)">
          <div class="grid gap-1.5" :class="narrow ? 'grid-cols-2' : 'grid-cols-4'">
            <WatchTile label="CPU" :value="c.stats.cpu" icon="i-lucide-cpu" />
            <WatchTile label="RAM" :value="c.stats.mem.split('/')[0]!.trim()" icon="i-lucide-memory-stick" />
            <WatchTile :label="t('watchW.net')" :value="c.stats.net" icon="i-lucide-arrow-down-up" />
            <WatchTile label="PIDs" :value="c.stats.pids" icon="i-lucide-list-tree" />
          </div>
          <div v-if="!short && cpuHist.length > 1">
            <p class="text-[11px] text-(--ui-text-dimmed)">CPU</p><WatchSparkline :values="cpuHist" label="CPU" />
          </div>
        </template>
      </div>
      <LogTerminal v-if="showConsole" fill class="min-h-0 min-w-0 flex-1" :url="logUrl" :title="svc.name" :empty="t('ops.emptyLog')" />
    </div>
  </div>
</template>
