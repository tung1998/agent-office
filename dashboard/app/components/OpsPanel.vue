<script setup lang="ts">
// "Vận hành" tab: run and watch the project's commands (dev server, build,
// test…) like pm2, with live logs.
import type { Attachment } from './PromptInput.vue'
import type { MonitorItem } from './MonitorChip.vue'

interface ProcState {
  status: 'stopped' | 'running' | 'stopping' | 'exited' | 'crashed' | 'restarting'
  pid?: number
  started_at?: string
  finished_at?: string
  exit_code?: number
  restarts: number
  port?: number
  cpu: number
  mem_bytes: number
}
interface Proc { id: string, name: string, command: string, cwd: string, kind: 'service' | 'job', source: string, autostart: boolean, autorestart: boolean, env: Record<string, string>, state: ProcState, version: string }
// what a command runs with, found without running it (PORT from its flag, its
// script, the folder's .env or its framework's default)
interface EnvHint { key: string, value: string, source: 'command' | 'script' | 'env-file' | 'default', file?: string, tool?: string, flag?: string, append?: string }
// a row of the form's environment: hint = found; saved = in the process's env
interface EnvRow { key: string, value: string, hint?: EnvHint, saved: boolean }
interface Suggestion { name: string, command: string, cwd: string, kind: 'service' | 'job', source: string, description: string, recommended: boolean }
interface Detection { package_manager?: string, suggestions: Suggestion[], compose: { file: string }[] }

const props = defineProps<{ projectId: string, hasFolder: boolean }>()
const emit = defineEmits<{ askAgent: [text: string, files: Attachment[], send?: boolean] }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
type Section = 'processes' | 'containers' | 'env'
// each process's monitor, on its row (cheap to list: no checks run)
const { data: monData, refresh: refreshMon } = useLiveFetch<{ monitors: MonitorItem[] }>('/api/monitors', { query: { project: props.projectId }, lazy: true })
const monitorOf = (p: Proc) => monData.value?.monitors.find(m => m.type === 'process' && m.target === p.id)
const navItems = computed(() => [
  { value: 'processes', label: t('ops.nav.processes'), icon: 'i-lucide-square-terminal', count: procs.value.length, alert: procs.value.some(p => p.state.status === 'crashed') },
  { value: 'containers', label: t('ops.nav.containers'), icon: 'i-lucide-container' },
  ...(isAdmin.value ? [{ value: 'env', label: t('ops.nav.env'), icon: 'i-lucide-file-key' }] : [])
])
const section = ref<Section>((['processes', 'containers', 'env'] as const).find(v => v === useRoute().query.section) ?? 'processes')

const { data, refresh } = await useLiveFetch<{ processes: Proc[] }>(() => `/api/projects/${props.projectId}/processes`)
const procs = computed(() => data.value?.processes ?? [])
const selectedId = ref<string | null>(null)
const selected = computed(() => procs.value.find(p => p.id === selectedId.value) ?? null)
watch(procs, (list) => {
  if (!selectedId.value || !list.some(p => p.id === selectedId.value)) selectedId.value = list[0]?.id ?? null
}, { immediate: true })

// pinned = frequently used; once something is pinned the rest fold away so the
// terminal stays in view. Per-viewer convenience, kept in localStorage.
const pinKey = `office:ops-pins:${props.projectId}`
const pins = ref<Set<string>>(new Set())
onMounted(() => {
  try {
    pins.value = new Set(JSON.parse(localStorage.getItem(pinKey) ?? '[]'))
  } catch { /* bad value: start empty */ }
})
function togglePin(p: Proc) {
  const s = new Set(pins.value)
  if (s.has(p.id)) s.delete(p.id)
  else s.add(p.id)
  pins.value = s
  try {
    localStorage.setItem(pinKey, JSON.stringify([...s]))
  } catch { /* private mode: keep in memory */ }
}
const showOthers = ref(false)
const isActive = (p: Proc) => ['running', 'restarting', 'stopping', 'crashed'].includes(p.state.status)
// pinned first; running/crashed and the selected one never fold away
const mainProcs = computed(() => {
  if (!procs.value.some(p => pins.value.has(p.id))) return procs.value
  const pinned = procs.value.filter(p => pins.value.has(p.id))
  return [...pinned, ...procs.value.filter(p => !pins.value.has(p.id) && (isActive(p) || p.id === selectedId.value))]
})
const otherProcs = computed(() => procs.value.filter(p => !mainProcs.value.includes(p)))

// states and monitors come as events (useLiveFetch); only the CPU/RAM of what
// runs is asked for, while the tab is open (logs stream via SSE)
let poll: ReturnType<typeof setInterval> | undefined
const startPoll = () => { clearInterval(poll); poll = setInterval(() => { if (procs.value.some(p => p.state.status === 'running')) refresh() }, 5000) }
onMounted(startPoll)
onActivated(() => { refresh(); refreshMon(); startPoll() }) // another tab: no polling
onDeactivated(() => clearInterval(poll))
onBeforeUnmount(() => clearInterval(poll))

// ---- actions ----
const busy = ref<string | null>(null)
async function act(p: Proc, action: 'start' | 'stop' | 'restart') {
  busy.value = p.id + action
  try {
    await $fetch(`/api/processes/${p.id}/${action}`, { method: 'POST' })
    selectedId.value = p.id
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = null
  }
}
async function toggleAutostart(p: Proc) {
  try {
    await $fetch(`/api/processes/${p.id}`, { method: 'PATCH', body: { autostart: !p.autostart } })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function remove(p: Proc) {
  if (!confirm(t('ops.confirmDelete', { name: p.name }))) return
  if (busy.value === p.id + 'delete') return
  busy.value = p.id + 'delete'
  try {
    await $fetch(`/api/processes/${p.id}`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = null
  }
}
// fix=true: "Sửa lỗi" sends at once; the agent reads logs itself with the office tools
async function askAgent(p: Proc, fix = false) {
  try {
    const att = await $fetch<Attachment>(`/api/processes/${p.id}/log-attachment`, { method: 'POST' })
    const why = p.state.status === 'crashed' ? t('ops.reasonCrashed', { code: p.state.exit_code ?? '' }) : t('ops.reasonRunning', { status: statusMeta.value[p.state.status].label.toLowerCase() })
    const text = fix
      ? t('ops.askFixPrompt', { name: p.name, command: p.command, why })
      : t('ops.askPrompt', { name: p.name, command: p.command, why })
    emit('askAgent', text, [att], fix)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// ---- detect / add ----
const detectOpen = ref(false)
const detection = ref<Detection | null>(null)
const picked = ref<Set<string>>(new Set())
const detecting = ref(false)
async function detect() {
  detecting.value = true
  try {
    detection.value = await $fetch<Detection>(`/api/projects/${props.projectId}/ops/detect`)
    const have = new Set(procs.value.map(p => p.name))
    picked.value = new Set(detection.value.suggestions.filter(s => s.recommended && !have.has(s.name)).map(s => s.name))
    detectShowAll.value = false
    detectOpen.value = true
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    detecting.value = false
  }
}
function toggle(name: string) {
  const s = new Set(picked.value)
  if (s.has(name)) s.delete(name)
  else s.add(name)
  picked.value = s
}
const existing = computed(() => new Set(procs.value.map(p => p.name)))
// the usual ones (dev, build, test…) first; the rest behind "show more"
const detectShowAll = ref(false)
const commonSuggestions = computed(() => (detection.value?.suggestions ?? []).filter(s => s.recommended))
const shownSuggestions = computed(() => {
  const all = detection.value?.suggestions ?? []
  if (detectShowAll.value || !commonSuggestions.value.length) return [...commonSuggestions.value, ...all.filter(s => !s.recommended)]
  return commonSuggestions.value
})
const hiddenSuggestions = computed(() => (detection.value?.suggestions.length ?? 0) - shownSuggestions.value.length)
async function addPicked() {
  const list = detection.value?.suggestions.filter(s => picked.value.has(s.name)) ?? []
  for (const s of list) {
    try {
      await $fetch(`/api/projects/${props.projectId}/processes`, { method: 'POST', body: { name: s.name, command: s.command, cwd: s.cwd, kind: s.kind, source: s.source } })
    } catch (e) {
      toast.add({ title: `${s.name}: ${apiError(e)}`, color: 'error' })
    }
  }
  detectOpen.value = false
  await refresh()
}

const formOpen = ref(false)
const editing = ref<Proc | null>(null)
const form = reactive({ name: '', command: '', cwd: '.', kind: 'service' as 'service' | 'job', autostart: false, autorestart: false, version: '' })
const envRows = ref<EnvRow[]>([])
function openForm(p?: Proc) {
  editing.value = p ?? null
  Object.assign(form, p
    ? { name: p.name, command: p.command, cwd: p.cwd, kind: p.kind, autostart: p.autostart, autorestart: p.autorestart, version: p.version }
    : { name: '', command: '', cwd: '.', kind: 'service', autostart: false, autorestart: false, version: '' })
  envRows.value = Object.entries(p?.env ?? {}).map(([key, value]) => ({ key, value, saved: true }))
  formOpen.value = true
  detectEnv()
}

// found again as the command or folder changes; what was set or typed stays
const detectingEnv = ref(false)
let detectEnvSeq = 0
async function detectEnv() {
  const seq = ++detectEnvSeq
  if (!form.command.trim()) {
    envRows.value = envRows.value.filter(r => !r.hint || r.saved || changed(r))
    detectingEnv.value = false
    return
  }
  detectingEnv.value = true
  try {
    const { env } = await $fetch<{ env: EnvHint[] }>(`/api/projects/${props.projectId}/ops/env`, { query: { cwd: form.cwd || '.', command: form.command } })
    if (seq !== detectEnvSeq) return
    const kept = envRows.value.filter(r => !r.hint || r.saved || changed(r))
    const rows: EnvRow[] = kept.map(r => ({ ...r, hint: env.find(h => h.key === r.key) }))
    for (const h of env) {
      if (!rows.some(r => r.key === h.key)) rows.push({ key: h.key, value: h.value, hint: h, saved: false })
    }
    envRows.value = rows
  } catch { /* a hint, not needed to save */ } finally {
    if (seq === detectEnvSeq) detectingEnv.value = false
  }
}
let envTimer: ReturnType<typeof setTimeout> | undefined
watch(() => [form.command, form.cwd], () => {
  if (!formOpen.value) return
  detectEnvSeq++
  clearTimeout(envTimer)
  envTimer = setTimeout(detectEnv, 500)
})
const changed = (r: EnvRow) => !r.hint || r.value !== r.hint.value
// where a value goes when saved: the command's own flag, a flag added to it,
// or the process's environment
function envTarget(r: EnvRow): 'flag' | 'append' | 'env' {
  if (r.hint?.flag && !r.saved) return 'flag'
  if (r.hint?.append && !r.saved) return 'append'
  return 'env'
}
function envNote(r: EnvRow) {
  const h = r.hint
  const from = !h
    ? ''
    : h.source === 'command'
      ? t('ops.env.fromCommand')
      : h.source === 'script'
        ? t('ops.env.fromScript', { file: h.file ?? '' })
        : h.source === 'env-file'
          ? t('ops.env.fromFile', { file: h.file ?? '' })
          : t('ops.env.fromDefault', { tool: h.tool ?? '' })
  if (!changed(r) && !r.saved) return from
  const to = { flag: t('ops.env.toFlag'), append: t('ops.env.toAppend'), env: t('ops.env.toEnv') }[envTarget(r)]
  return h ? `${from} · ${to}` : to
}
// set in package.json: a variable may not change it
const envWarn = (r: EnvRow) => r.hint?.source === 'script' && changed(r) && envTarget(r) === 'env'
function resetEnv(r: EnvRow) {
  if (r.hint) Object.assign(r, { value: r.hint.value, saved: false })
  else envRows.value = envRows.value.filter(x => x !== r)
}
const addEnv = () => envRows.value.push({ key: '', value: '', saved: true })

// the command and environment to save: a changed flag is changed in the
// command, a tool without a variable gets the flag added, the rest is env
function envToSave() {
  let command = form.command
  const env: Record<string, string> = {}
  for (const r of envRows.value) {
    const key = r.key.trim()
    if (!key || (!r.saved && !changed(r))) continue
    const target = envTarget(r)
    if (target === 'flag') command = command.replace(r.hint!.flag!, r.hint!.flag!.replace(new RegExp(`${r.hint!.value}$`), r.value.trim()))
    else if (target === 'append') command += r.hint!.append!.replace('%s', r.value.trim())
    else env[key] = r.value
  }
  return { command, env }
}

async function saveForm() {
  try {
    const body = { ...form, ...envToSave() }
    if (editing.value) await $fetch(`/api/processes/${editing.value.id}`, { method: 'PATCH', body })
    else await $fetch(`/api/projects/${props.projectId}/processes`, { method: 'POST', body })
    formOpen.value = false
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const envPrefix = (p: Proc) => Object.entries(p.env ?? {}).map(([k, v]) => `${k}=${v} `).join('')

// ---- display ----
const statusMeta = computed<Record<ProcState['status'], { label: string, color: 'success' | 'error' | 'neutral' | 'warning' | 'info', dot: string }>>(() => ({
  running: { label: t('ops.status.running'), color: 'success', dot: 'bg-(--ui-success)' },
  stopped: { label: t('ops.status.stopped'), color: 'neutral', dot: 'bg-(--ui-text-dimmed)' },
  stopping: { label: t('ops.status.stopping'), color: 'warning', dot: 'bg-(--ui-warning)' },
  exited: { label: t('ops.status.exited'), color: 'info', dot: 'bg-(--ui-info)' },
  crashed: { label: t('ops.status.crashed'), color: 'error', dot: 'bg-(--ui-error)' },
  restarting: { label: t('ops.status.restarting'), color: 'warning', dot: 'bg-(--ui-warning)' }
}))
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
const startClock = () => { clearInterval(clock); clock = setInterval(() => { now.value = Date.now() }, 1000) }
onMounted(startClock)
onActivated(startClock)
onDeactivated(() => clearInterval(clock))
onBeforeUnmount(() => clearInterval(clock))
function uptime(p: Proc) {
  if (p.state.status !== 'running' || !p.state.started_at) return ''
  const s = Math.max(0, Math.floor((now.value - new Date(p.state.started_at).getTime()) / 1000))
  return s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
}
const mem = (b: number) => b >= 1 << 30 ? `${(b / (1 << 30)).toFixed(1)} GB` : `${Math.round(b / (1 << 20))} MB`
</script>

<template>
  <div class="space-y-4">
    <UAlert v-if="!hasFolder" color="neutral" variant="subtle" icon="i-lucide-monitor" :title="t('ops.noFolder.title')" :description="t('ops.noFolder.desc')" />

    <template v-else>
      <!-- kept alive: switching back shows the last state instantly -->
      <KeepAlive>
        <ComposePanel v-if="section === 'containers'" :project-id="projectId" @ask-agent="(t, f, send) => emit('askAgent', t, f, send)">
          <template #nav><SegmentedNav v-model="section" :items="navItems" /></template>
        </ComposePanel>
        <EnvPanel v-else-if="section === 'env' && isAdmin" :project-id="projectId">
          <template #nav><SegmentedNav v-model="section" :items="navItems" /></template>
        </EnvPanel>
      </KeepAlive>
      <template v-if="section === 'processes'">
      <div class="flex flex-wrap items-center gap-2">
        <SegmentedNav v-model="section" :items="navItems" />
        <div v-if="isAdmin" class="ms-auto flex gap-2">
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-scan-search" :label="t('ops.scanProject')" :loading="detecting" @click="detect" />
          <UButton size="sm" icon="i-lucide-plus" :label="t('ops.addCommand')" @click="openForm()" />
        </div>
      </div>

      <div v-if="!procs.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
        <UIcon name="i-lucide-activity" class="mx-auto size-8 text-(--ui-text-dimmed)" />
        <p class="mt-2 font-medium">{{ t('ops.empty.title') }}</p>
        <p class="text-sm text-(--ui-text-muted)">{{ t('ops.empty.desc') }}</p>
        <UButton v-if="isAdmin" class="mt-4" icon="i-lucide-scan-search" :label="t('ops.scanProject')" :loading="detecting" @click="detect" />
      </div>

      <div v-else class="grid gap-4 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
        <!-- process list: scrolls on its own so the terminal beside it stays put -->
        <div class="space-y-2 lg:max-h-[min(26rem,60vh)] lg:overflow-y-auto lg:pe-1">
          <div
            v-for="p in showOthers ? [...mainProcs, ...otherProcs] : mainProcs" :key="p.id" role="button" tabindex="0"
            class="rounded-lg border p-3 transition"
            :class="p.id === selectedId ? 'border-(--ui-primary) bg-(--ui-primary)/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
            @click="selectedId = p.id" @keydown.enter="selectedId = p.id"
          >
            <div class="flex items-center gap-2">
              <span class="size-2 shrink-0 rounded-full" :class="[statusMeta[p.state.status].dot, { 'animate-pulse': p.state.status === 'running' || p.state.status === 'restarting' }]" />
              <p class="min-w-0 flex-1 truncate font-medium">{{ p.name }}</p>
              <UButton
                size="xs" color="neutral" variant="ghost" :icon="pins.has(p.id) ? 'i-lucide-pin-off' : 'i-lucide-pin'"
                :class="pins.has(p.id) ? 'text-(--ui-primary)' : 'text-(--ui-text-dimmed)'"
                :aria-label="pins.has(p.id) ? t('ops.unpin') : t('ops.pin')" :title="pins.has(p.id) ? t('ops.unpin') : t('ops.pin')"
                @click.stop="togglePin(p)"
              />
              <UBadge :color="statusMeta[p.state.status].color" variant="subtle" size="sm" :label="statusMeta[p.state.status].label + (p.state.status === 'crashed' || p.state.status === 'exited' ? ` (${p.state.exit_code})` : '')" />
            </div>
            <code class="mt-1 block truncate text-xs text-(--ui-text-muted)">{{ p.cwd !== '.' ? `${p.cwd} $ ` : '$ ' }}{{ envPrefix(p) }}{{ p.command }}</code>
            <div v-if="p.state.status === 'running'" class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-(--ui-text-muted) tabular-nums">
              <span>PID {{ p.state.pid }}</span>
              <span>{{ uptime(p) }}</span>
              <span>CPU {{ p.state.cpu.toFixed(1) }}%</span>
              <span>RAM {{ mem(p.state.mem_bytes) }}</span>
              <a v-if="p.state.port" :href="`http://localhost:${p.state.port}`" target="_blank" rel="noopener" class="text-(--ui-primary)" @click.stop>:{{ p.state.port }} ↗</a>
            </div>
            <div class="mt-2 flex flex-wrap items-center gap-1" @click.stop>
              <UBadge v-if="p.autorestart" color="neutral" variant="outline" size="sm" :label="t('ops.autorestartBadge')" />
              <UBadge v-if="p.autostart" color="neutral" variant="outline" size="sm" :label="t('ops.autostartBadge')" />
              <!-- a job ends on its own: only a service is worth watching -->
              <MonitorChip v-if="monData && (p.kind === 'service' || monitorOf(p))" :project-id="projectId" type="process" :target="p.id" :monitor="monitorOf(p)" @changed="refreshMon()" />
              <div class="ms-auto flex shrink-0 flex-wrap items-center justify-end gap-1">
                <template v-if="isAdmin">
                  <UButton
                    v-if="p.state.status !== 'running' && p.state.status !== 'stopping'" size="xs" icon="i-lucide-play" :label="t('ops.start')"
                    :loading="busy === p.id + 'start'" @click="act(p, 'start')"
                  />
                  <template v-else>
                    <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-square" :label="t('ops.stop')" :loading="busy === p.id + 'stop' || p.state.status === 'stopping'" @click="act(p, 'stop')" />
                    <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-rotate-cw" :aria-label="t('ops.restart')" :loading="busy === p.id + 'restart'" @click="act(p, 'restart')" />
                  </template>
                </template>
                <UButton
                  v-if="p.state.status === 'crashed'" size="xs" color="error" variant="soft" icon="i-lucide-wrench" :label="t('ops.fix')"
                  @click="askAgent(p, true)"
                />
                <UDropdownMenu
                  v-if="isAdmin"
                  :items="[[{ label: t('ops.menu.askAgent'), icon: 'i-lucide-bot', onSelect: () => askAgent(p) }, { label: t('ops.menu.edit'), icon: 'i-lucide-pencil', onSelect: () => openForm(p) }, { label: p.autostart ? t('ops.menu.autostartOff') : t('ops.menu.autostartOn'), icon: p.autostart ? 'i-lucide-power-off' : 'i-lucide-power', onSelect: () => toggleAutostart(p) }], [{ label: t('ops.menu.delete'), icon: 'i-lucide-trash-2', color: 'error', onSelect: () => remove(p) }]]"
                  :content="{ align: 'end' }"
                >
                  <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-ellipsis" :aria-label="t('ops.actionsLabel')" :loading="busy === p.id + 'delete'" :disabled="busy === p.id + 'delete'" />
                </UDropdownMenu>
              </div>
            </div>
          </div>
          <UButton
            v-if="otherProcs.length" block size="sm" color="neutral" variant="ghost"
            :icon="showOthers ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
            :label="showOthers ? t('ops.hideOthers') : t('ops.showOthers', { n: otherProcs.length })"
            @click="showOthers = !showOthers"
          />
        </div>

        <!-- logs -->
        <LogTerminal
          v-if="selected" :url="`/api/processes/${selected.id}/stream`" :title="selected.name" :subtitle="selected.command"
          :empty="t('ops.emptyLog')"
        />
      </div>
      </template>
    </template>

    <!-- detection -->
    <UModal v-model:open="detectOpen" :title="t('ops.detect.title')" :ui="{ content: 'max-w-2xl' }">
      <template #body>
        <div v-if="detection" class="space-y-3">
          <p class="text-sm text-(--ui-text-muted)">
            <template v-if="detection.package_manager">{{ t('ops.detect.usingPm', { pm: detection.package_manager }) }}</template>{{ t('ops.detect.choose') }}
          </p>
          <p v-if="!detection.suggestions.length" class="py-6 text-center text-(--ui-text-muted)">{{ t('ops.detect.none') }}</p>
          <div class="max-h-96 divide-y divide-(--ui-border) overflow-auto rounded-lg border border-(--ui-border)">
            <label v-for="s in shownSuggestions" :key="s.name" class="flex cursor-pointer items-start gap-3 px-3 py-2" :class="{ 'opacity-50': existing.has(s.name) }">
              <UCheckbox :model-value="picked.has(s.name)" :disabled="existing.has(s.name)" class="mt-0.5" @update:model-value="toggle(s.name)" />
              <div class="min-w-0 flex-1">
                <p class="text-sm font-medium">
                  {{ s.name }}
                  <UBadge color="neutral" variant="subtle" size="sm" :label="s.kind === 'service' ? t('ops.detect.kindService') : t('ops.detect.kindJob')" class="ms-1" />
                  <span v-if="existing.has(s.name)" class="text-xs font-normal text-(--ui-text-muted)">{{ t('ops.detect.alreadyHave') }}</span>
                </p>
                <code class="block truncate text-xs text-(--ui-text-muted)">{{ s.command }}<template v-if="s.description"> → {{ s.description }}</template></code>
              </div>
              <span class="shrink-0 text-xs text-(--ui-text-dimmed)">{{ s.source }}</span>
            </label>
            <button v-if="hiddenSuggestions" type="button" class="flex w-full items-center justify-center gap-1 px-3 py-2 text-sm text-(--ui-text-muted) hover:text-(--ui-text)" @click="detectShowAll = true">
              <UIcon name="i-lucide-chevron-down" class="size-4" />{{ t('ops.detect.showAll', { n: hiddenSuggestions }) }}
            </button>
          </div>
          <UAlert
            v-if="detection.compose.length" color="info" variant="subtle" icon="i-lucide-container"
            :title="t('ops.detect.composeAlert', { files: detection.compose.map(c => c.file).join(', ') })" :description="t('ops.detect.composeDesc')"
          />
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('ops.detect.cancel')" @click="detectOpen = false" />
          <UButton :label="t('ops.detect.add', { n: picked.size })" :disabled="!picked.size" @click="addPicked" />
        </div>
      </template>
    </UModal>

    <!-- add / edit -->
    <UModal v-model:open="formOpen" :title="editing ? t('ops.form.editTitle', { name: editing.name }) : t('ops.form.addTitle')" :ui="{ content: 'max-w-xl' }">
      <template #body>
        <form id="proc-form" class="space-y-4" @submit.prevent="saveForm">
          <UFormField :label="t('ops.form.name')" required>
            <UInput v-model="form.name" class="w-full" :placeholder="t('ops.form.namePlaceholder')" />
          </UFormField>
          <UFormField :label="t('ops.form.command')" required :help="t('ops.form.commandHelp')">
            <UInput v-model="form.command" class="w-full font-mono" :placeholder="t('ops.form.commandPlaceholder')" />
          </UFormField>
          <UFormField>
            <template #label><InfoLabel :label="t('ops.form.cwd')" :info="t('ops.form.cwdHelp')" /></template>
            <UInput v-model="form.cwd" class="w-full font-mono" placeholder="." />
          </UFormField>
          <UFormField :label="t('ops.env.title')">
            <template #hint>
              <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-scan-search" :loading="detectingEnv" :label="t('ops.env.redetect')" @click="detectEnv" />
            </template>
            <div class="space-y-2">
              <div v-for="(r, i) in envRows" :key="i" class="space-y-0.5">
                <div class="flex items-center gap-2">
                  <UInput v-model="r.key" class="w-1/2 shrink-0 font-mono" :disabled="!!r.hint" placeholder="PORT" :aria-label="t('ops.env.key')" />
                  <UInput v-model="r.value" class="min-w-0 flex-1 font-mono" :aria-label="t('ops.env.value')" />
                  <UButton
                    v-if="r.saved || changed(r)" size="sm" color="neutral" variant="ghost" :icon="r.hint ? 'i-lucide-rotate-ccw' : 'i-lucide-x'"
                    :aria-label="r.hint ? t('ops.env.reset') : t('ops.env.remove')" :title="r.hint ? t('ops.env.reset') : t('ops.env.remove')" @click="resetEnv(r)"
                  />
                </div>
                <p v-if="envNote(r)" class="text-xs" :class="envWarn(r) ? 'text-(--ui-warning)' : 'text-(--ui-text-dimmed)'">{{ envWarn(r) ? t('ops.env.scriptWarn', { file: r.hint?.file ?? '' }) : envNote(r) }}</p>
              </div>
              <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-plus" :label="t('ops.env.add')" @click="addEnv" />
            </div>
          </UFormField>
          <URadioGroup
            v-model="form.kind" orientation="horizontal"
            :items="[{ label: t('ops.form.kindService'), value: 'service' }, { label: t('ops.form.kindJob'), value: 'job' }]"
          />
          <div class="flex flex-wrap gap-4">
            <UCheckbox v-model="form.autorestart" :disabled="form.kind === 'job'" :label="t('ops.form.autorestart')" />
            <UCheckbox v-model="form.autostart" :label="t('ops.form.autostart')" />
          </div>
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('ops.form.cancel')" @click="formOpen = false" />
          <UButton type="submit" form="proc-form" :label="t('ops.form.save')" />
        </div>
      </template>
    </UModal>
  </div>
</template>
