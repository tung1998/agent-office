<script setup lang="ts">
// Rebuild office from its source and restart on the new build; the supervisor
// (office run) rolls back to the previous build if the new one fails.
// waiting: built, the new build waits for office to be idle (ADR-255)
interface UpdateState {
  status: 'idle' | 'running' | 'waiting' | 'failed' | 'cancelled' | 'restarting'
  step?: string, error?: string, started_at?: string, finished_at?: string
  when_idle?: boolean, by?: string, waiting_since?: string, wait_until?: string, busy?: string
}
// the work a restart cuts off, by kind
interface BusyItem { id: string, name: string, project_id: string, link: string }
interface Busy { chats: number, chat_items: BusyItem[], workflows: BusyItem[], automations: BusyItem[], burn: BusyItem[], tasks: BusyItem[], parked?: BusyItem[] }
interface Status {
  supervised: boolean
  build: { version: string, revision?: string, time?: string, dirty: boolean }
  last: { state: 'ok' | 'rolled_back' | 'failed', message: string, at: string } | null
  busy: Busy
  auto: { enabled: boolean }
  behind: number // commits of the source the running build lacks (-1: unknown)
  source?: { root: string, ui_dir: string }
  state?: UpdateState
}

const toast = useToast()
const { t, when } = useLang()
const { data, refresh } = await useLiveFetch<Status>('/api/system/update')
const runTests = ref(true)
const pull = ref(true) // the remote's new commits first (when there are)
const starting = ref(false)
const restarting = ref(false)
const state = computed(() => data.value?.state)
const running = computed(() => state.value?.status === 'running')
const waiting = computed(() => state.value?.status === 'waiting')
const busyCount = computed(() => {
  const b = data.value?.busy
  return b ? b.chats + b.workflows.length + b.automations.length + b.burn.length + b.tasks.length : 0
})
// what runs, by kind, for the list under the buttons
const busyGroups = computed(() => {
  const b = data.value?.busy
  if (!b) return []
  return [
    { key: 'chats', label: t('admin.updateBusyChats'), items: b.chat_items, more: Math.max(b.chats - b.chat_items.length, 0) },
    { key: 'workflows', label: t('admin.updateBusyWorkflows'), items: b.workflows, more: 0 },
    { key: 'automations', label: t('admin.updateBusyAutomations'), items: b.automations, more: 0 },
    { key: 'burn', label: t('admin.updateBusyBurn'), items: b.burn, more: 0 },
    { key: 'tasks', label: t('admin.updateBusyTasks'), items: b.tasks, more: 0 }
  ].filter(g => g.items.length || g.more)
})
// workflows that only wait: a restart does not hurt them, they go on after it (ADR-256)
const parked = computed(() => data.value?.busy?.parked ?? [])
const showLog = ref(false)
const streamKey = ref(0) // new stream per update

let poll: ReturnType<typeof setInterval> | undefined
let restartPoll: ReturnType<typeof setInterval> | undefined
function watchUpdate() {
  clearInterval(poll)
  poll = setInterval(async () => {
    try {
      await refresh()
      if (state.value?.status === 'restarting') waitForRestart()
      if (state.value?.status !== 'running' && state.value?.status !== 'waiting') clearInterval(poll)
    } catch { waitForRestart() } // the server went away: it is restarting
  }, 1500)
}
watch(() => state.value?.status, (status) => {
  if (status === 'running' || status === 'waiting') { showLog.value = true; watchUpdate() }
  else if (status === 'restarting') { showLog.value = true; waitForRestart() }
}, { immediate: true })
onBeforeUnmount(() => { clearInterval(poll); clearInterval(restartPoll) })

// whenIdle: build now, restart once nothing runs (the default); else now,
// cutting off what runs (after a confirm)
async function start(force = false, whenIdle = false) {
  starting.value = true
  try {
    await $fetch('/api/system/update', { method: 'POST', body: { test: runTests.value, pull: pull.value && !!changes.value?.remote?.behind, force, when_idle: whenIdle } })
    showLog.value = true
    streamKey.value++
    await refresh()
    watchUpdate()
  } catch (e) {
    const d = (e as { data?: { code?: string, error?: string } }).data
    if (d?.code === 'busy') {
      // the confirm already says it: no toast when the person declines
      if (!confirm(t('admin.updateBusyConfirm', { msg: d.error ?? '' }))) return
      starting.value = false
      return start(true, false)
    }
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    starting.value = false
  }
}

// a waiting update: drop it, or swap it in now (cutting off what runs)
const settling = ref(false)
async function cancelWaiting() {
  settling.value = true
  try {
    await $fetch('/api/system/update', { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    settling.value = false
  }
}
async function hurry() {
  if (!confirm(t('admin.updateHurryConfirm', { msg: state.value?.busy ?? '' }))) return
  settling.value = true
  try {
    await $fetch('/api/system/update', { method: 'POST', body: { force: true } })
    await refresh()
    watchUpdate()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    settling.value = false
  }
}

// update on its own when the source gets new commits (ADR-255)
const savingAuto = ref(false)
async function setAuto(enabled: boolean) {
  savingAuto.value = true
  try {
    await $fetch('/api/system/update/auto', { method: 'PUT', body: { enabled } })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    savingAuto.value = false
  }
}

// the server restarts: wait until it answers again, then reload the page
function waitForRestart() {
  if (restarting.value) return
  restarting.value = true
  clearInterval(poll)
  const started = Date.now()
  restartPoll = setInterval(async () => {
    try {
      const h = await $fetch<{ status: string }>('/api/health')
      if (h.status === 'ok' && Date.now() - started > 3000) {
        clearInterval(restartPoll)
        window.location.reload()
      }
    } catch { /* still restarting */ }
  }, 1500)
}

// does the source differ from the running build? (git, so only on open and
// on "check again"; null until known or when it cannot be told)
interface Commit { hash: string, subject: string, at: string }
// remote: the source's branch on origin (main/master): commits not pulled yet
interface Changes { changed: boolean, head: string, commits: Commit[], more: boolean, uncommitted: number, unknown?: boolean, remote?: { branch: string, behind: number, ahead: number, commits: Commit[], more: boolean, error?: string } }
const changes = ref<Changes | null>(null)
const changesError = ref('')
const checkingChanges = ref(false)
async function checkChanges() {
  if (!data.value?.source) return
  checkingChanges.value = true
  changesError.value = ''
  try {
    changes.value = await $fetch<Changes>('/api/system/update/changes')
  } catch (e) {
    changes.value = null
    changesError.value = apiError(e)
  } finally {
    checkingChanges.value = false
  }
}
onMounted(checkChanges)
// the update button shows when something changed, or when that is unknown
const upToDate = computed(() => changes.value?.changed === false)

const lastMeta = computed(() => ({
  ok: { color: 'success' as const, icon: 'i-lucide-circle-check', title: t('admin.updateLastOk') },
  rolled_back: { color: 'warning' as const, icon: 'i-lucide-undo-2', title: t('admin.updateLastRolledBack') },
  failed: { color: 'error' as const, icon: 'i-lucide-circle-x', title: t('admin.updateLastFailed') }
}))
</script>

<template>
  <PageShell :title="t('admin.updateTitle')">
    <div v-if="data" class="mx-auto max-w-3xl space-y-4">
      <UCard>
        <div class="flex flex-wrap items-start gap-4">
          <UIcon name="i-lucide-package" class="mt-0.5 size-6 text-primary" />
          <div class="min-w-0 flex-1 space-y-1 text-sm">
            <p class="font-semibold">
              Agent Office {{ data.build.version }}
              <code v-if="data.build.revision" class="ms-1 text-xs text-(--ui-text-muted)">{{ data.build.revision.slice(0, 7) }}</code>
              <UBadge v-if="data.build.dirty" color="warning" variant="subtle" size="sm" :label="t('admin.updateDirty')" class="ms-1" />
            </p>
            <p v-if="data.build.time" class="text-xs text-(--ui-text-muted)">{{ t('admin.updateCommitAt', { t: when(data.build.time) }) }}</p>
            <p v-if="data.source" class="truncate font-mono text-xs text-(--ui-text-muted)">{{ t('admin.updateSource', { path: data.source.root }) }}</p>
          </div>
          <UBadge :color="data.supervised ? 'success' : 'neutral'" variant="subtle" :icon="data.supervised ? 'i-lucide-shield-check' : 'i-lucide-shield-off'" :label="data.supervised ? t('admin.updateHasSupervisor') : t('admin.updateNoSupervisor')" />
        </div>
      </UCard>

      <UAlert
        v-if="data.last && data.last.state !== 'ok' || data.last?.state === 'ok' && !running && !restarting" :color="lastMeta[data.last!.state].color" variant="subtle"
        :icon="lastMeta[data.last!.state].icon" :title="lastMeta[data.last!.state].title" :description="`${data.last!.message} · ${when(data.last!.at)}`"
      />

      <UAlert
        v-if="!data.supervised" color="neutral" variant="subtle" icon="i-lucide-info" :title="t('admin.updateNoSupervisorTitle')"
        :description="t('admin.updateNoSupervisorDesc')"
      />
      <UAlert
        v-else-if="!data.source" color="neutral" variant="subtle" icon="i-lucide-info" :title="t('admin.updateNoSourceTitle')"
        :description="t('admin.updateNoSourceDesc')"
      />

      <UCard v-else>
        <div class="space-y-4">
          <div>
            <p class="font-semibold"><InfoLabel :label="t('admin.updateFromSource')" :info="t('admin.updateFromSourceDesc')" /></p>
          </div>
          <div class="rounded-lg border border-(--ui-border) p-3 text-sm">
            <div class="flex items-center gap-2">
              <UIcon v-if="checkingChanges && !changes" name="i-lucide-loader-circle" class="size-4 animate-spin text-(--ui-text-muted)" />
              <template v-else-if="changes">
                <UIcon :name="upToDate ? 'i-lucide-circle-check' : 'i-lucide-circle-arrow-up'" class="size-4" :class="upToDate ? 'text-(--ui-success)' : 'text-primary'" />
                <span class="font-medium">{{ upToDate ? t('admin.updateLatest') : t('admin.updateHasChanges') }}</span>
              </template>
              <span v-else-if="changesError" class="text-(--ui-text-muted)">{{ t('admin.updateChangesUnknown', { msg: changesError }) }}</span>
              <UButton class="ms-auto" size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :label="t('admin.updateCheckAgain')" :loading="checkingChanges" :disabled="running || waiting" @click="checkChanges" />
            </div>
            <template v-if="changes && !upToDate">
              <ul v-if="changes.commits.length" class="mt-2 space-y-1">
                <li v-for="c in changes.commits" :key="c.hash" class="flex items-baseline gap-2 text-xs">
                  <code class="shrink-0 text-(--ui-text-muted)">{{ c.hash }}</code>
                  <span class="min-w-0 truncate">{{ c.subject }}</span>
                  <span class="ms-auto shrink-0 text-(--ui-text-dimmed)">{{ when(c.at) }}</span>
                </li>
                <li v-if="changes.more" class="text-xs text-(--ui-text-dimmed)">{{ t('admin.updateMoreCommits') }}</li>
              </ul>
              <p v-if="changes.uncommitted" class="mt-2 text-xs text-(--ui-warning)">{{ t('admin.updateUncommitted', { n: changes.uncommitted }) }}</p>
              <p v-if="changes.unknown" class="mt-2 text-xs text-(--ui-text-muted)">{{ t('admin.updateUnknownRevision') }}</p>
            </template>
            <template v-if="changes?.remote">
              <template v-if="changes.remote.behind">
                <p class="mt-3 flex items-center gap-1.5 text-xs font-medium">
                  <UIcon name="i-lucide-cloud-download" class="size-3.5 text-primary" />
                  {{ t('admin.updateRemoteBehind', { n: changes.remote.behind, branch: changes.remote.branch }) }}
                </p>
                <ul class="mt-1 space-y-1">
                  <li v-for="c in changes.remote.commits" :key="c.hash" class="flex items-baseline gap-2 text-xs">
                    <code class="shrink-0 text-(--ui-text-muted)">{{ c.hash }}</code>
                    <span class="min-w-0 truncate">{{ c.subject }}</span>
                    <span class="ms-auto shrink-0 text-(--ui-text-dimmed)">{{ when(c.at) }}</span>
                  </li>
                  <li v-if="changes.remote.more" class="text-xs text-(--ui-text-dimmed)">{{ t('admin.updateMoreCommits') }}</li>
                </ul>
              </template>
              <p v-else-if="!changes.remote.error" class="mt-2 text-xs text-(--ui-text-muted)">
                {{ t('admin.updateRemoteSame', { branch: changes.remote.branch }) }}<template v-if="changes.remote.ahead"> · {{ t('admin.updateRemoteAhead', { n: changes.remote.ahead }) }}</template>
              </p>
              <p v-if="changes.remote.error" class="mt-2 text-xs text-(--ui-text-muted)">{{ changes.remote.error }}</p>
            </template>
          </div>
          <template v-if="!upToDate">
            <UCheckbox v-if="changes?.remote?.behind" v-model="pull" :label="t('admin.updatePull', { branch: changes.remote.branch })" :disabled="running || waiting" />
            <UCheckbox v-model="runTests" :label="t('admin.updateRunTests')" :disabled="running || waiting" />
          </template>
          <!-- what a restart would cut off now: an update when idle waits for it -->
          <div v-if="busyCount" class="rounded-lg border border-(--ui-border) p-3 text-sm">
            <p class="flex items-center gap-1.5 font-medium">
              <UIcon name="i-lucide-activity" class="size-4 text-(--ui-warning)" />
              <InfoLabel :label="t('admin.updateBusyTitle', { n: busyCount })" :info="t('admin.updateBusyInfo')" />
            </p>
            <div v-for="g in busyGroups" :key="g.key" class="mt-2">
              <p class="text-xs text-(--ui-text-muted)">{{ g.label }}</p>
              <ul class="mt-0.5 space-y-0.5">
                <li v-for="it in g.items.slice(0, 8)" :key="it.id" class="truncate text-xs">
                  <NuxtLink :to="it.link" class="hover:underline">{{ it.name }}</NuxtLink>
                </li>
                <li v-if="g.items.length > 8 || g.more" class="text-xs text-(--ui-text-dimmed)">{{ t('admin.updateBusyMore', { n: Math.max(g.items.length - 8, 0) + g.more }) }}</li>
              </ul>
            </div>
          </div>
          <div v-if="parked.length" class="rounded-lg border border-(--ui-border) p-3 text-sm">
            <p class="flex items-center gap-1.5 font-medium">
              <UIcon name="i-lucide-pause" class="size-4 text-(--ui-info)" />
              <InfoLabel :label="t('admin.updateParkedTitle', { n: parked.length })" :info="t('admin.updateParkedInfo')" />
            </p>
            <ul class="mt-1 space-y-0.5">
              <li v-for="it in parked.slice(0, 8)" :key="it.id" class="truncate text-xs">
                <NuxtLink :to="it.link" class="hover:underline">{{ it.name }}</NuxtLink>
              </li>
            </ul>
          </div>
          <UAlert
            v-if="waiting" color="primary" variant="subtle" icon="i-lucide-hourglass" :title="t('admin.updateWaitingTitle')"
            :description="t('admin.updateWaitingDesc', { busy: state?.busy ?? '', until: state?.wait_until ? when(state.wait_until) : '' })"
            :actions="[
              { label: t('admin.updateHurry'), color: 'warning', variant: 'outline', loading: settling, onClick: hurry },
              { label: t('admin.updateCancel'), color: 'neutral', variant: 'outline', loading: settling, onClick: cancelWaiting }
            ]"
          />
          <div v-else class="flex flex-wrap items-center gap-3">
            <template v-if="!upToDate || running">
              <UButton icon="i-lucide-clock" :label="t('admin.updateWhenIdle')" :title="t('admin.updateWhenIdleHelp')" :loading="(starting || running) && state?.when_idle !== false" :disabled="restarting || running" @click="start(false, true)" />
              <UButton color="neutral" variant="outline" icon="i-lucide-zap" :label="t('admin.updateNow')" :loading="starting && !running" :disabled="restarting || running" @click="start()" />
            </template>
            <!-- up to date by the source; dependencies or the toolchain may still have moved -->
            <UButton v-else size="xs" color="neutral" variant="link" :label="t('admin.updateRebuildAnyway')" :loading="starting" :disabled="restarting" @click="start(false, true)" />
            <span v-if="running" class="text-sm text-(--ui-text-muted)">{{ state?.step }}…</span>
            <span v-else-if="state?.status === 'failed'" class="text-sm text-(--ui-error)">{{ state.error }}</span>
            <span v-else-if="state?.status === 'cancelled'" class="text-sm text-(--ui-text-muted)">{{ state.error }}</span>
          </div>
          <p class="text-xs text-(--ui-text-muted)">{{ t('admin.updateProcessNote') }}</p>
          <USwitch
            :model-value="data.auto.enabled" :loading="savingAuto" :label="t('admin.updateAuto')" :description="t('admin.updateAutoDesc')"
            @update:model-value="setAuto"
          />
        </div>
      </UCard>

      <div v-if="restarting" class="flex items-center gap-2 rounded-lg border border-(--ui-border) p-4 text-sm">
        <UIcon name="i-lucide-loader-circle" class="size-5 animate-spin text-primary" />
        {{ t('admin.updateRestarting') }}
      </div>

      <LogTerminal v-if="showLog && data.source" :key="streamKey" url="/api/system/update/stream" :title="t('admin.updateStreamTitle')" :subtitle="state?.step" :empty="t('admin.updateStreamEmpty')" />
    </div>
  </PageShell>
</template>
