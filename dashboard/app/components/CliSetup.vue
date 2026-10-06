<script setup lang="ts">
// Installs and signs in a local AI CLI (Claude Code, Codex) through the office
// server, which runs on the same machine. Emits `ready` once usable.
interface Method { id: string, label: string, command: string, requires: string, available: boolean, recommended: boolean }
interface JobView {
  id: string
  action: 'install' | 'login'
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
  installed: boolean
  version?: string
  auth: { logged_in: boolean, account?: string, detail?: string }
  methods: Method[]
  job?: JobView
}

const props = defineProps<{ tool: 'claude' | 'codex' }>()
const emit = defineEmits<{ ready: [boolean] }>()
const toast = useToast()
const { t } = useLang()

const status = ref<ToolStatus | null>(null)
const disabled = ref(false) // server started with --cli-setup=false
const method = ref('')
const job = ref<JobView | null>(null)
const code = ref('')
const outputEl = ref<HTMLElement | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined

const ready = computed(() => !!status.value?.installed && !!status.value?.auth.logged_in)
watch(ready, v => emit('ready', v), { immediate: true })
watch(disabled, (v) => { if (v) emit('ready', true) })

async function load() {
  try {
    status.value = await $fetch<ToolStatus>(`/api/cli-tools/${props.tool}`)
    const methods = status.value.methods
    if (!method.value) method.value = (methods.find(m => m.recommended && m.available) ?? methods.find(m => m.available))?.id ?? ''
    if (status.value.job?.state === 'running') follow(status.value.job)
  } catch (e) {
    if ((e as { statusCode?: number }).statusCode === 404) disabled.value = true
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
      if (job.value.action === 'login' && status.value?.auth.logged_in) {
        toast.add({ id: `cli-${j.id}`, title: t('cli.loggedIn', { name: status.value?.name ?? '' }), description: status.value.auth.account, color: 'success' })
      }
    }
  }
  tick()
}

async function start(action: 'install' | 'login') {
  try {
    const body = action === 'install' ? { method: method.value } : {}
    follow(await $fetch<JobView>(`/api/cli-tools/${props.tool}/${action}`, { method: 'POST', body }))
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
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
  <div v-if="status && !disabled && (!ready || running)" class="space-y-3 rounded-lg border border-(--ui-border) p-3">
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
      <UButton v-if="!running" icon="i-lucide-log-in" :label="t('cli.loginBtn', { name: status.name })" size="sm" @click="start('login')" />
      <template v-if="running && job?.action === 'login'">
        <div v-if="job.urls.length || job.codes.length" class="flex flex-wrap items-center gap-2">
          <UButton
            v-for="u in job.urls.slice(0, 1)" :key="u" :to="u" target="_blank" external
            icon="i-lucide-external-link" :label="t('cli.openLoginPage')" size="sm"
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
          <span class="text-xs text-(--ui-text-muted)">{{ job.action === 'install' ? t('cli.installing') : t('cli.waitingLogin') }}</span>
          <UButton :label="t('cli.cancel')" size="xs" color="neutral" variant="ghost" class="ms-auto" @click="cancel" />
        </template>
        <span v-else class="text-xs text-(--ui-error)">{{ t('cli.failed', { detail: job.error ? `: ${job.error}` : '' }) }}</span>
      </div>
    </template>
  </div>
</template>
