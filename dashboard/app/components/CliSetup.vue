<script setup lang="ts">
// Installs, updates and signs in a local AI CLI (Claude Code, Codex, Gemini CLI, Antigravity CLI) through the office
// server, which runs on the same machine. Emits `ready` once usable. Once
// signed in it shows the account with Sign in again / Sign out: a session the
// server revoked can still read signed in to the CLI.
interface Method { id: string, label: string, command: string, requires: string, available: boolean, recommended: boolean }
interface JobView {
  id: string
  action: 'install' | 'update' | 'login'
  command: string
  state: 'running' | 'succeeded' | 'failed' | 'cancelled'
  output: string
  urls: string[]
  codes: string[]
  error?: string
}
interface ToolStatus {
  id: string
  name: string
  bin: string
  docs_url: string
  login_command: string
  logout_command: string
  update_command?: string
  installed: boolean
  version?: string
  auth: { logged_in: boolean, account?: string, detail?: string }
  methods: Method[]
  job?: JobView
}

// provider: the connection whose account it is (one signed in to an account of its own, ADR-178)
const props = defineProps<{ tool: 'claude' | 'codex' | 'gemini' | 'antigravity', provider?: string }>()
const emit = defineEmits<{ ready: [boolean] }>()
const toast = useToast()
const { t } = useLang()

const status = ref<ToolStatus | null>(null)
const disabled = ref(false) // server started with --cli-setup=false
const loadError = ref('')
const method = ref('')
const job = ref<JobView | null>(null)
const code = ref('')
const outputEl = ref<HTMLElement | null>(null)
const query = computed(() => props.provider ? { provider: props.provider } : {})
let timer: ReturnType<typeof setTimeout> | undefined

const ready = computed(() => !!status.value?.installed && !!status.value?.auth.logged_in)
watch(ready, v => emit('ready', v), { immediate: true })
watch(disabled, (v) => { if (v) emit('ready', true) })

async function load() {
  loadError.value = ''
  try {
    status.value = await $fetch<ToolStatus>(`/api/cli-tools/${props.tool}`, { query: query.value })
    const methods = status.value.methods
    if (!method.value) method.value = (methods.find(m => m.recommended && m.available) ?? methods.find(m => m.available))?.id ?? ''
    if (status.value.job?.state === 'running') follow(status.value.job)
  } catch (e) {
    if ((e as { statusCode?: number }).statusCode === 404) disabled.value = true
    else loadError.value = apiError(e)
  }
}

function follow(j: JobView) {
  job.value = j
  clearTimeout(timer)
  const tick = async () => {
    try {
      job.value = await $fetch<JobView>(`/api/cli-jobs/${j.id}`)
    } catch {
      return
    }
    await nextTick()
    outputEl.value?.scrollTo({ top: outputEl.value.scrollHeight })
    if (job.value.state === 'running') {
      timer = setTimeout(tick, 1000)
    } else {
      await load()
      if (job.value.state === 'succeeded' && job.value.action === 'install') {
        toast.add({ id: `cli-${j.id}`, title: t('cli.installed', { name: status.value?.name ?? '' }), color: 'success' })
      }
      if (job.value.action === 'update') {
        const name = status.value?.name ?? ''
        toast.add(job.value.state === 'succeeded'
          ? { id: `cli-${j.id}`, title: t('cli.updated', { name }), description: status.value?.version, color: 'success' }
          : { id: `cli-${j.id}`, title: t('cli.updateFailed', { name }), description: job.value.error || job.value.output.slice(-300), color: 'error' })
      }
      if (job.value.action === 'login' && status.value?.auth.logged_in) {
        toast.add({ id: `cli-${j.id}`, title: t('cli.loggedIn', { name: status.value?.name ?? '' }), description: status.value.auth.account, color: 'success' })
      }
    }
  }
  tick()
}

async function start(action: 'install' | 'update' | 'login') {
  try {
    const body = action === 'install' ? { method: method.value } : {}
    follow(await $fetch<JobView>(`/api/cli-tools/${props.tool}/${action}`, { method: 'POST', body, query: query.value }))
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

async function logout(): Promise<boolean> {
  try {
    status.value = await $fetch<ToolStatus>(`/api/cli-tools/${props.tool}/logout`, { method: 'POST', body: {}, query: query.value })
    return true
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
    return false
  }
}

async function confirmLogout() {
  if (status.value && confirm(t('cli.confirmLogout', { name: status.value.name, cmd: status.value.logout_command }))) await logout()
}

// relogin signs out first: Gemini's sign-in goes straight to its chat while
// credentials exist, and a revoked Codex session is better gone
async function relogin() {
  if (await logout()) await start('login')
}

async function sendCode() {
  if (!job.value) return
  try {
    await $fetch(`/api/cli-jobs/${job.value.id}/input`, { method: 'POST', body: { text: code.value.trim() } })
    code.value = ''
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

async function cancel() {
  if (job.value) await $fetch(`/api/cli-jobs/${job.value.id}/cancel`, { method: 'POST', body: {} })
}

async function copy(text: string) {
  // over plain HTTP there is no clipboard API: show the code to copy by hand
  // rather than a "copied" that did nothing
  if (await copyText(text)) toast.add({ title: t('cli.copied'), color: 'neutral' })
  else toast.add({ title: text, color: 'neutral', duration: 0 })
}

const selectedMethod = computed(() => status.value?.methods.find(m => m.id === method.value))
const running = computed(() => job.value?.state === 'running')

watch(() => props.tool, () => { status.value = null; job.value = null; method.value = ''; load() })
onMounted(load)
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <!-- the status runs the CLI (version, sign-in): it can take seconds -->
  <div v-if="!status && !disabled" class="flex items-center gap-2 text-sm text-(--ui-text-muted)">
    <template v-if="loadError">
      <UIcon name="i-lucide-circle-alert" class="size-4 shrink-0 text-(--ui-error)" />
      <span class="min-w-0 flex-1">{{ loadError }}</span>
      <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :label="t('cli.retry')" @click="load" />
    </template>
    <template v-else>
      <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
      {{ t('cli.checking', { tool: props.tool }) }}
    </template>
  </div>
  <div v-else-if="status && !disabled && ready && !running" class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
    <UIcon name="i-lucide-circle-check" class="size-4 shrink-0 text-(--ui-success)" />
    <span class="min-w-0 flex-1 basis-40 truncate">
      {{ t('cli.loggedIn', { name: status.name }) }}<span v-if="status.auth.account" class="text-(--ui-text-muted)"> · {{ status.auth.account }}</span>
    </span>
    <UButton
      v-if="status.update_command" size="xs" color="neutral" variant="ghost" icon="i-lucide-circle-arrow-up"
      :label="t('cli.update')" :title="`${status.version ?? ''} · ${status.update_command}`" @click="start('update')"
    />
    <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :label="t('cli.relogin')" @click="relogin" />
    <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-log-out" :label="t('cli.logout')" @click="confirmLogout" />
  </div>
  <div v-else-if="status && !disabled && (!ready || running)" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
    <!-- install -->
    <template v-if="!status.installed">
      <div class="flex items-center gap-2 text-sm">
        <UIcon name="i-lucide-download" class="size-4 text-(--ui-warning)" />
        <span>{{ t('cli.notInstalledPrefix') }} <b>{{ status.name }}</b>.</span>
      </div>
      <div class="flex flex-wrap gap-2">
        <button
          v-for="m in status.methods" :key="m.id" type="button" :disabled="!m.available || running"
          class="rounded-md border px-2.5 py-1 text-xs transition disabled:cursor-not-allowed disabled:opacity-40"
          :class="method === m.id ? 'border-(--ui-primary) bg-(--ui-primary)/10 text-(--ui-primary)' : 'border-(--ui-border)'"
          :title="m.available ? m.command : t('cli.requiresCommand', { cmd: m.requires })"
          @click="method = m.id"
        >
          {{ m.label }}
        </button>
      </div>
      <code v-if="selectedMethod" class="block rounded bg-(--ui-bg-muted) px-2 py-1.5 text-xs">{{ selectedMethod.command }}</code>
      <UButton v-if="!running" icon="i-lucide-download" :label="t('cli.installBtn', { name: status.name })" size="sm" :disabled="!selectedMethod" @click="start('install')" />
    </template>

    <!-- login -->
    <template v-else-if="!status.auth.logged_in || (running && job?.action === 'login')">
      <div class="flex items-center gap-2 text-sm">
        <UIcon name="i-lucide-log-in" class="size-4 text-(--ui-warning)" />
        <span><b>{{ status.name }}</b> {{ t('cli.notLoggedInSuffix') }}</span>
      </div>
      <p v-if="status.auth.detail && !running" class="text-xs text-(--ui-text-muted)">{{ status.auth.detail }}</p>
      <UButton v-if="!running" icon="i-lucide-log-in" :label="t('cli.loginBtn', { name: status.name })" size="sm" @click="start('login')" />
      <template v-if="running && job?.action === 'login'">
        <div v-if="job.urls.length || job.codes.length" class="flex flex-wrap items-center gap-2">
          <UButton
            v-for="u in job.urls.slice(0, 1)" :key="u" :to="u" target="_blank" external
            icon="i-lucide-external-link" :label="t('cli.openLoginPage')" size="sm"
          />
          <!-- the link to open in another browser or on another machine -->
          <UButton
            v-for="u in job.urls.slice(0, 1)" :key="`copy-${u}`" icon="i-lucide-copy" :label="t('cli.copyLoginUrl')"
            size="sm" color="neutral" variant="outline" @click="copy(u)"
          />
          <UButton
            v-for="c in job.codes" :key="c" icon="i-lucide-copy" :label="c" size="sm" color="neutral" variant="outline"
            class="font-mono" @click="copy(c)"
          />
        </div>
        <p class="text-xs text-(--ui-text-muted)">
          {{ t('cli.loginHint') }}
        </p>
        <form class="flex gap-2" @submit.prevent="sendCode">
          <UInput v-model="code" :placeholder="t('cli.pastePlaceholder')" size="sm" class="flex-1 font-mono" />
          <UButton type="submit" :label="t('cli.sendCode')" size="sm" color="neutral" variant="outline" :disabled="!code.trim()" />
        </form>
      </template>
    </template>

    <!-- live output -->
    <template v-if="job && (running || job.state === 'failed')">
      <pre ref="outputEl" class="max-h-40 overflow-auto whitespace-pre-wrap rounded bg-(--ui-bg-muted) p-2 text-[11px] leading-snug">{{ job.output || '…' }}</pre>
      <div class="flex items-center gap-2">
        <template v-if="running">
          <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin text-(--ui-text-muted)" />
          <span class="text-xs text-(--ui-text-muted)">{{ job.action === 'install' ? t('cli.installing') : job.action === 'update' ? t('cli.updating') : t('cli.waitingLogin') }}</span>
          <UButton :label="t('cli.cancel')" size="xs" color="neutral" variant="ghost" class="ms-auto" @click="cancel" />
        </template>
        <span v-else class="text-xs text-(--ui-error)">{{ t('cli.failed', { detail: job.error ? `: ${job.error}` : '' }) }}</span>
      </div>
    </template>
  </div>
</template>
