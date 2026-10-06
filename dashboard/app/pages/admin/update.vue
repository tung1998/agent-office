<script setup lang="ts">
// Rebuild office from its source and restart on the new build; the supervisor
// (office run) rolls back to the previous build if the new one fails.
interface UpdateState { status: 'idle' | 'running' | 'failed' | 'restarting', step?: string, error?: string, started_at?: string, finished_at?: string }
interface Status {
  supervised: boolean
  build: { version: string, revision?: string, time?: string, dirty: boolean }
  last: { state: 'ok' | 'rolled_back' | 'failed', message: string, at: string } | null
  busy: { chats: number, tasks: number }
  source?: { root: string, ui_dir: string }
  state?: UpdateState
}

const toast = useToast()
const { t, dateLocale } = useLang()
const { data, refresh } = await useLiveFetch<Status>('/api/system/update')
const runTests = ref(true)
const starting = ref(false)
const restarting = ref(false)
const state = computed(() => data.value?.state)
const running = computed(() => state.value?.status === 'running')
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
      if (state.value?.status !== 'running') clearInterval(poll)
    } catch { waitForRestart() } // the server went away: it is restarting
  }, 1500)
}
onMounted(() => { if (running.value) { showLog.value = true; watchUpdate() } })
onBeforeUnmount(() => { clearInterval(poll); clearInterval(restartPoll) })

async function start(force = false) {
  starting.value = true
  try {
    await $fetch('/api/system/update', { method: 'POST', body: { test: runTests.value, force } })
    showLog.value = true
    streamKey.value++
    await refresh()
    watchUpdate()
  } catch (e) {
    const d = (e as { data?: { code?: string, error?: string } }).data
    if (d?.code === 'busy' && confirm(t('admin.updateBusyConfirm', { msg: d.error ?? '' }))) {
      starting.value = false
      return start(true)
    }
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    starting.value = false
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
interface Changes { changed: boolean, head: string, commits: { hash: string, subject: string, at: string }[], more: boolean, uncommitted: number, unknown?: boolean }
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
const when = (d: string) => new Date(d).toLocaleString(dateLocale.value)
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
            <p class="font-semibold">{{ t('admin.updateFromSource') }}</p>
            <p class="text-sm text-(--ui-text-muted)">
              {{ t('admin.updateFromSourceDesc') }}
            </p>
          </div>
          <div class="rounded-lg border border-(--ui-border) p-3 text-sm">
            <div class="flex items-center gap-2">
              <UIcon v-if="checkingChanges && !changes" name="i-lucide-loader-circle" class="size-4 animate-spin text-(--ui-text-muted)" />
              <template v-else-if="changes">
                <UIcon :name="upToDate ? 'i-lucide-circle-check' : 'i-lucide-circle-arrow-up'" class="size-4" :class="upToDate ? 'text-(--ui-success)' : 'text-primary'" />
                <span class="font-medium">{{ upToDate ? t('admin.updateLatest') : t('admin.updateHasChanges') }}</span>
              </template>
              <span v-else-if="changesError" class="text-(--ui-text-muted)">{{ t('admin.updateChangesUnknown', { msg: changesError }) }}</span>
              <UButton class="ms-auto" size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :label="t('admin.updateCheckAgain')" :loading="checkingChanges" :disabled="running" @click="checkChanges" />
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
          </div>
          <template v-if="!upToDate">
            <UCheckbox v-model="runTests" :label="t('admin.updateRunTests')" :disabled="running" />
            <p v-if="data.busy.chats || data.busy.tasks" class="text-sm text-(--ui-warning)">
              <UIcon name="i-lucide-triangle-alert" class="align-middle" />
              {{ t('admin.updateBusy', { chats: data.busy.chats, tasks: data.busy.tasks }) }}
            </p>
            <p class="text-xs text-(--ui-text-muted)">{{ t('admin.updateProcessNote') }}</p>
          </template>
          <div class="flex items-center gap-3">
            <UButton v-if="!upToDate || running" icon="i-lucide-refresh-cw" :label="t('admin.updateStart')" :loading="starting || running" :disabled="restarting" @click="start()" />
            <!-- up to date by the source; dependencies or the toolchain may still have moved -->
            <UButton v-else size="xs" color="neutral" variant="link" :label="t('admin.updateRebuildAnyway')" :loading="starting" :disabled="restarting" @click="start()" />
            <span v-if="running" class="text-sm text-(--ui-text-muted)">{{ state?.step }}…</span>
            <span v-else-if="state?.status === 'failed'" class="text-sm text-(--ui-error)">{{ state.error }}</span>
          </div>
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
