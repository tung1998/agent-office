<script setup lang="ts">
// The automation form: trigger, action, content, limits (ADR-042). It edits
// the draft it is given; the builder page saves it. highlight marks fields an
// agent just changed; "Chạy thử" runs the script now without saving.
// command: a bot's command (its trigger is set on the bot's page): only what
// it does, what it sends and its limits
const props = defineProps<{ projectId: string, form: AutomationDraft, highlight?: string[], command?: boolean }>()
const emit = defineEmits<{ tested: [{ output: string, exit_code: number, timed_out: boolean }] }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t, dateLocale } = useLang()
// eslint-disable-next-line vue/no-mutating-props -- the draft is the parent's reactive object, edited in place
const form = props.form

const { data: agentsData } = useLiveFetch<{ agents: Agent[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
// a bot's messages start it (ADR-049): bots are set up on their own page,
// each of its commands being one automation edited here in "command" mode
const fromChannel = computed(() => isChannelSource(form.source))
// the three sources, as cards (a bot opens the bot's setup page)
const sourceCards = computed(() => [
  { value: 'schedule', icon: 'i-lucide-alarm-clock', title: t('auto.sourceSchedule'), desc: t('auto.sourceScheduleDesc'), active: !botsOpen.value && form.source === 'schedule',
    pick: () => { botsOpen.value = false; form.source = 'schedule' } },
  { value: 'webhook', icon: 'i-lucide-webhook', title: t('auto.sourceWebhook'), desc: t('auto.sourceWebhookDesc'), active: !botsOpen.value && form.source === 'webhook' && !form.config.pull_request,
    pick: () => { botsOpen.value = false; form.source = 'webhook'; form.config.pull_request = false } },
  // a GitHub/Bitbucket pull request: a webhook whose token is in its URL (they send no header)
  { value: 'pr', icon: 'i-lucide-git-pull-request', title: t('auto.sourcePR'), desc: t('auto.sourcePRDesc'), active: !botsOpen.value && form.source === 'webhook' && !!form.config.pull_request,
    pick: () => { botsOpen.value = false; form.source = 'webhook'; form.config.pull_request = true; form.config.auth = 'query'; form.config.auth_name = 'token' } },
  // a bot's messages: made on the bot's page (a command of it); here, which bot
  { value: 'channel', icon: 'i-lucide-messages-square', title: t('auto.sourceChannel'), desc: t('auto.sourceChannelDesc'), active: botsOpen.value || fromChannel.value,
    pick: () => { botsOpen.value = true } }
])
const botsOpen = ref(false)
const { data: projBots } = useLiveFetch<{ channels: Channel[] }>(() => `/api/projects/${props.projectId}/channels`, { lazy: true, immediate: isAdmin.value })
// a Select item cannot have "" as its value: "the lead" is a sentinel
const LEAD = '__lead'
const chatAgent = computed({ get: () => form.agent_id || LEAD, set: (v: string) => { form.agent_id = v === LEAD ? '' : v } })
// who answers in a chat: one agent ("" = the lead)
const replyAgentOptions = computed(() => [{ label: t('channels.agentLead'), value: LEAD }, ...(agentsData.value?.agents ?? []).map(a => ({ label: a.name, value: a.id }))])
// tags each run's chat gets; the project's chat tags to pick again
const tags = computed({ get: () => form.config.tags ?? [], set: (v: string[]) => { form.config.tags = v } })
const { data: tagsData } = useLiveFetch<{ tags: { tag: string }[] }>(() => `/api/projects/${props.projectId}/chat-tags`, { lazy: true })
const projectTags = computed(() => (tagsData.value?.tags ?? []).map(x => x.tag))
const langOptions =[{ label: 'bash', value: 'bash' }, { label: 'node', value: 'node' }, { label: 'python', value: 'python' }]

const presets = computed(() => [
  { label: t('auto.presetEvery5'), every: 5, cron: '' },
  { label: t('auto.presetHourly'), every: 0, cron: '0 * * * *' },
  { label: t('auto.presetWeekday8'), every: 0, cron: '0 8 * * 1-5' },
  { label: t('auto.presetDaily8'), every: 0, cron: '0 8 * * *' }
])
function usePreset(p: { every: number, cron: string }) {
  form.config.every_minutes = p.every
  form.config.cron = p.cron
}
// a schedule either repeats every N minutes or runs at fixed times (cron)
const schedKind = computed({
  get: () => (form.config.every_minutes ?? 0) > 0 ? 'every' : 'cron',
  set: (v: string) => {
    if (v === 'every') {
      form.config.cron = ''
      form.config.every_minutes = form.config.every_minutes || 15
    } else {
      form.config.every_minutes = 0
      form.config.cron = form.config.cron || '0 8 * * *'
    }
  }
})
const presetActive = (p: { every: number, cron: string }) => (form.config.every_minutes ?? 0) === p.every && (form.config.cron ?? '') === p.cron
// every IANA zone the browser/Node knows, searchable
const timezones = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? ['UTC', 'Asia/Ho_Chi_Minh']
const tz = computed({ get: () => form.config.timezone || 'Asia/Ho_Chi_Minh', set: (v: string) => { form.config.timezone = v } })
// the saved zone stays pickable even if this runtime spells it differently
const tzItems = computed(() => timezones.includes(tz.value) ? timezones : [tz.value, ...timezones])
// an agent answers (a bot's chat) or is sent the message (a schedule, a webhook), or a script runs
const actions = computed(() => [
  fromChannel.value
    ? { value: 'chat' as const, icon: 'i-lucide-message-circle-reply', title: t('auto.cardReply'), desc: t('auto.cardReplyDesc') }
    : { value: 'chat' as const, icon: 'i-lucide-send', title: t('auto.cardAgent'), desc: t('auto.cardAgentDesc') },
  { value: 'script' as const, icon: 'i-lucide-square-terminal', title: t('auto.cardScript'), desc: fromChannel.value ? t('auto.cardScriptChannelDesc') : t('auto.cardScriptDesc') }
])

// when a schedule stops on its own, as a Burn: the weekly limit's reset, after
// some hours, at a time, or never (turned off by hand)
const { data: resetData } = useLiveFetch<{ weekly_reset: string | null }>(() => `/api/projects/${props.projectId}/automations/weekly-reset?agent_id=${form.agent_id}`, { lazy: true })
const weeklyReset = computed(() => resetData.value?.weekly_reset ?? null)
const endMode = ref<'none' | 'reset' | 'hours' | 'at'>(form.config.ends_at ? 'at' : 'none')
const endHours = ref(8)
const localInput = (iso: string) => { const d = new Date(iso); return new Date(d.getTime() - d.getTimezoneOffset() * 60e3).toISOString().slice(0, 16) }
const endAt = ref(form.config.ends_at ? localInput(form.config.ends_at) : localInput(new Date(Date.now() + 8 * 3600e3).toISOString()))
watch([endMode, endHours, endAt, weeklyReset], () => {
  switch (endMode.value) {
    case 'reset': form.config.ends_at = weeklyReset.value; break
    case 'hours': form.config.ends_at = new Date(Date.now() + Math.max(1, endHours.value) * 3600e3).toISOString(); break
    case 'at': form.config.ends_at = endAt.value ? new Date(endAt.value).toISOString() : null; break
    default: form.config.ends_at = null
  }
})
const endItems = computed(() => [
  { value: 'none', label: t('burn.endNone') },
  ...(weeklyReset.value ? [{ value: 'reset', label: t('burn.endReset', { at: fmt(weeklyReset.value) }) }] : []),
  { value: 'hours', label: t('burn.endHours') },
  { value: 'at', label: t('burn.endAt') }
])

// next runs, asked from the server (same parser as the scheduler)
const preview = ref<{ next: string[], error?: string }>({ next: [] })
let timer: ReturnType<typeof setTimeout> | undefined
watch(() => [form.source, form.config.every_minutes, form.config.cron, form.config.timezone, form.config.ends_at], () => {
  clearTimeout(timer)
  if (form.source !== 'schedule') return
  timer = setTimeout(async () => {
    const q = new URLSearchParams({ every: String(form.config.every_minutes || 0), cron: form.config.cron ?? '', tz: form.config.timezone ?? '', ends: form.config.ends_at ?? '' })
    try {
      preview.value = await $fetch(`/api/automations/preview-schedule?${q}`)
    } catch { preview.value = { next: [] } }
  }, 400)
}, { immediate: true })
const fmt = (d: string) => new Date(d).toLocaleString(dateLocale.value, { weekday: 'short', day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })

const placeholders = computed(() => fromChannel.value
  ? ['{{message}}', '{{user}}', '{{now}}', '{{today}}', '{{automation}}']
  : form.config.pull_request
    ? ['{{diff}}', '{{payload.number}}', '{{payload.title}}', '{{payload.author}}', '{{payload.url}}', '{{payload.source}}', '{{payload.target}}']
    : ['{{payload}}', '{{payload.x}}', '{{now}}', '{{today}}', '{{yesterday}}', '{{source}}', '{{automation}}'])
// the project's bots, for where answers go
const { data: chData } = useLiveFetch<{ channels: Channel[] }>(() => `/api/projects/${props.projectId}/channels`, { lazy: true, immediate: isAdmin.value })
const NO_BOT = '__none'
const notifyBot = computed({ get: () => form.config.notify_channel_id || NO_BOT, set: (v: string) => { form.config.notify_channel_id = v === NO_BOT ? '' : v } })
const notifyItems = computed(() => [{ label: t('auto.notifyNone'), value: NO_BOT }, ...(chData.value?.channels ?? []).map(c => ({ label: `${c.bot_name ? '@' + c.bot_name : c.name} · ${c.kind === 'discord' ? 'Discord' : 'Telegram'}`, value: c.id }))])
const notifyKind = computed(() => chData.value?.channels.find(c => c.id === form.config.notify_channel_id)?.kind)
// ADR-074: permission mode — "agent" follows the picked agent's own
// permissions, "override" (admin only) replaces them for this automation.
const effectiveAgent = computed(() => {
  const agents = agentsData.value?.agents ?? []
  if (form.agent_id) return agents.find(a => a.id === form.agent_id)
  return agents.find(a => a.tier === 'lead') ?? agents[0]
})
const effectiveFullAccess = computed(() => form.permission_mode === 'override' ? !!form.override_full_access : !!effectiveAgent.value?.permissions.full_access)

const promptEl = ref<{ textareaRef?: HTMLTextAreaElement } | null>(null)
function insert(p: string) {
  const el = promptEl.value?.textareaRef
  const at = el?.selectionStart ?? form.prompt.length
  form.prompt = form.prompt.slice(0, at) + p + form.prompt.slice(at)
}

// a section's box: its own on the automation page; none inside the bot page (it has one)
const box = computed(() => props.command ? '' : 'rounded-xl border border-(--ui-border) p-4')
const hl = (key: string) => props.highlight?.includes(key) ? 'rounded-lg ring-2 ring-primary/60 ring-offset-2 ring-offset-(--ui-bg) transition' : ''

// "Chạy thử": the script runs now, not saved
const testPayload = ref('')
const testing = ref(false)
const tested = ref<{ output: string, exit_code: number, timed_out: boolean } | null>(null)
async function testRun() {
  testing.value = true
  try {
    tested.value = await $fetch(`/api/projects/${props.projectId}/automations/test-script`, { method: 'POST', body: { script: form.script, payload: testPayload.value } })
    emit('tested', tested.value!)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <!-- sized by its own width: the form sits in half of the builder page -->
  <div class="@container space-y-4">
    <UFormField v-if="!command" :label="t('auto.name')" required :class="hl('name')">
      <UInput v-model="form.name" class="w-full" :placeholder="t('auto.namePlaceholder')" />
    </UFormField>

    <!-- 1. trigger -->
    <section v-if="!command" class="space-y-4 rounded-xl border border-(--ui-border) p-4" :class="hl('source') || hl('config')">
      <p class="flex items-center gap-2 text-sm font-semibold">
        <span class="flex size-5 items-center justify-center rounded-full bg-primary/15 text-xs text-primary">1</span>{{ t('auto.stepSource') }}
        <span class="font-normal text-(--ui-text-muted)">· {{ t('auto.stepSourceHint') }}</span>
      </p>
      <!-- where it starts: three small choices in a row -->
      <div class="flex flex-wrap gap-2">
        <button
          v-for="s in sourceCards" :key="s.value" type="button"
          class="flex items-center gap-2 rounded-lg border px-2.5 py-1.5 text-left text-sm transition"
          :class="s.active ? 'border-primary bg-primary/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
          @click="s.pick()"
        >
          <UIcon :name="s.icon" class="size-4 shrink-0" :class="s.active ? 'text-primary' : 'text-(--ui-text-muted)'" />
          <span class="font-medium whitespace-nowrap">{{ s.title }}</span>
          <UTooltip :text="s.desc">
            <UIcon name="i-lucide-info" class="size-3.5 shrink-0 text-(--ui-text-dimmed) hover:text-(--ui-text) max-sm:hidden" @click.stop />
          </UTooltip>
        </button>
      </div>

      <!-- a bot's messages: its commands live on the bot's page -->
      <div v-if="botsOpen" class="space-y-2">
        <p class="text-sm text-(--ui-text-muted)">{{ t('auto.channelPick') }}</p>
        <div class="flex flex-wrap gap-2">
          <UButton
            v-for="b in projBots?.channels ?? []" :key="b.id" size="sm" color="neutral" variant="outline"
            :icon="b.kind === 'discord' ? 'i-lucide-gamepad-2' : 'i-lucide-send'" :label="b.bot_name ? `@${b.bot_name}` : b.name"
            :to="`/projects/${projectId}/bots/${b.id}/edit`"
          />
          <UButton size="sm" icon="i-lucide-plus" :label="t('auto.channelNew')" :to="`/projects/${projectId}/bots/new`" />
        </div>
      </div>
      <template v-else-if="form.source === 'schedule'">
        <div class="flex flex-wrap gap-1.5">
          <UButton
            v-for="p in presets" :key="p.label" size="xs" :color="presetActive(p) ? 'primary' : 'neutral'" :variant="presetActive(p) ? 'soft' : 'outline'"
            :label="p.label" @click="usePreset(p)"
          />
        </div>
        <div class="grid gap-3 @lg:grid-cols-2">
          <UFormField :label="schedKind === 'every' ? t('auto.schedEvery') : t('auto.schedCron')" :help="schedKind === 'cron' ? t('auto.cronHelp') : undefined">
            <div class="flex items-center gap-2">
              <USelect v-model="schedKind" :items="[{ label: t('auto.schedEvery'), value: 'every' }, { label: t('auto.schedCron'), value: 'cron' }]" class="w-36 shrink-0" />
              <template v-if="schedKind === 'every'">
                <UInputNumber v-model="form.config.every_minutes" :min="1" class="w-32" />
                <span class="text-sm text-(--ui-text-muted)">{{ t('auto.unitMinutes') }}</span>
              </template>
              <UInput v-else v-model="form.config.cron" class="min-w-0 flex-1 font-mono" placeholder="0 8 * * 1-5" />
            </div>
          </UFormField>
          <UFormField :label="t('auto.timezone')">
            <USelectMenu v-model="tz" :items="tzItems" :search-input="{ placeholder: t('auto.tzSearch') }" class="w-full" />
          </UFormField>
        </div>
        <!-- it stops on its own, as a Burn -->
        <UFormField :label="t('burn.endLabel')" :class="hl('config')">
          <div class="flex flex-wrap items-center gap-2">
            <USelect v-model="endMode" :items="endItems" class="min-w-56" />
            <template v-if="endMode === 'hours'">
              <UInputNumber v-model="endHours" :min="1" :max="720" class="w-28" />
              <span class="text-sm text-(--ui-text-muted)">{{ t('auto.unitHours') }}</span>
            </template>
            <UInput v-else-if="endMode === 'at'" v-model="endAt" type="datetime-local" class="w-60" />
            <span v-if="form.config.ends_at" class="text-xs text-(--ui-text-muted)">{{ t('auto.endsAt', { at: fmt(form.config.ends_at) }) }}</span>
          </div>
        </UFormField>
        <div class="flex flex-wrap items-center gap-1.5 text-xs">
          <span class="me-1 text-(--ui-text-muted)">{{ t('auto.nextRuns') }}</span>
          <span v-if="preview.error" class="text-(--ui-error)">{{ preview.error }}</span>
          <span v-for="n in preview.next" v-else :key="n" class="rounded-md bg-(--ui-bg-elevated) px-2 py-0.5 tabular-nums">{{ fmt(n) }}</span>
        </div>
      </template>
      <p v-else-if="form.config.pull_request" class="flex items-start gap-1.5 text-sm text-(--ui-text-muted)">
        {{ t('auto.prShort') }}
        <UTooltip :text="t('auto.prWhere')"><UIcon name="i-lucide-info" class="mt-0.5 size-4 shrink-0 hover:text-(--ui-text)" /></UTooltip>
      </p>
      <template v-else>
        <div class="grid gap-3 @lg:grid-cols-2">
          <UFormField :label="t('auto.auth')">
            <USelect v-model="form.config.auth" class="w-full" :items="[{ label: t('auto.authBearer'), value: 'bearer' }, { label: t('auto.authHeader'), value: 'header' }, { label: t('auto.authQuery'), value: 'query' }]" />
          </UFormField>
          <UFormField v-if="form.config.auth !== 'bearer'" :label="t('auto.authName')">
            <UInput v-model="form.config.auth_name" class="w-full font-mono" :placeholder="form.config.auth === 'header' ? 'X-Office-Token' : 'token'" />
          </UFormField>
        </div>
        <p class="text-xs text-(--ui-text-muted)">{{ t('auto.tunnelHint') }}</p>
      </template>
    </section>

    <!-- 2. action -->
    <section class="space-y-4" :class="[box, hl('action')]">
      <p class="flex items-center gap-2 text-sm font-semibold">
        <span v-if="!command" class="flex size-5 items-center justify-center rounded-full bg-primary/15 text-xs text-primary">2</span>{{ t('auto.stepAction') }}
      </p>
      <div class="grid gap-2 @md:grid-cols-2">
        <button
          v-for="x in actions" :key="x.value" type="button"
          class="flex h-full items-start gap-2.5 rounded-lg border p-3 text-left transition"
          :class="form.action === x.value ? 'border-primary bg-primary/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
          @click="form.action = x.value"
        >
          <UIcon :name="x.icon" class="mt-0.5 size-4 shrink-0" :class="form.action === x.value ? 'text-primary' : 'text-(--ui-text-muted)'" />
          <span class="min-w-0">
            <span class="block text-sm font-medium">{{ x.title }}</span>
            <span class="block text-xs text-(--ui-text-muted)">{{ x.desc }}</span>
          </span>
        </button>
      </div>

      <template v-if="form.action === 'script'">
        <div class="flex flex-wrap items-end gap-3">
          <UFormField :label="t('auto.scriptLang')"><USelect v-model="form.script.lang" :items="langOptions" class="w-32" /></UFormField>
          <UFormField :label="t('auto.timeout')">
            <div class="flex items-center gap-2">
              <UInputNumber v-model="form.script.timeout_s" :min="1" :max="3600" class="w-32" />
              <span class="text-sm text-(--ui-text-muted)">{{ t('auto.unitSeconds') }}</span>
            </div>
          </UFormField>
        </div>
        <UFormField :label="t('auto.scriptBody')" :help="t('auto.scriptHelp')" :class="hl('script')">
          <UTextarea v-model="form.script.body" :rows="12" autoresize class="w-full font-mono text-xs" placeholder="grep -c ERROR logs/app.log || true" />
        </UFormField>
        <div class="space-y-2 rounded-lg bg-(--ui-bg-elevated)/50 p-3">
          <div class="flex flex-wrap items-center gap-2">
            <UInput v-model="testPayload" size="sm" class="min-w-0 flex-1 font-mono" :placeholder="t('auto.testPayload')" />
            <UButton v-if="isAdmin" size="sm" icon="i-lucide-play" :label="t('auto.testRun')" :loading="testing" :disabled="!form.script.body.trim()" @click="testRun" />
          </div>
          <template v-if="tested">
            <p class="text-xs" :class="tested.exit_code === 0 ? 'text-(--ui-success)' : 'text-(--ui-error)'">
              {{ tested.timed_out ? t('auto.testTimeout') : t('job.exit', { n: tested.exit_code }) }}
            </p>
            <pre class="max-h-60 overflow-auto rounded bg-(--ui-bg) p-2 font-mono text-xs">{{ tested.output || t('job.noOutput') }}</pre>
          </template>
        </div>
      </template>
      <div v-else-if="form.action === 'chat'" class="space-y-2">
        <div class="flex flex-wrap items-end gap-3">
          <UFormField :label="t('channels.agent')"><USelect v-model="chatAgent" :items="replyAgentOptions" class="min-w-56" /></UFormField>
        </div>
        <p class="text-xs text-(--ui-text-muted)">{{ t('auto.replyNoTools') }}</p>
        <UFormField :label="t('auto.tags')" :class="hl('config')">
          <template #hint>
            <UTooltip :text="t('auto.tagsHint')"><UIcon name="i-lucide-info" class="size-4 text-(--ui-text-muted)" /></UTooltip>
          </template>
          <TagPicker v-model="tags" :suggestions="projectTags" :autofocus="false" />
        </UFormField>
      </div>
    </section>

    <!-- 3. content -->
    <section v-if="form.action !== 'script'" class="space-y-3" :class="[box, hl('prompt')]">
      <p class="flex items-center gap-2 text-sm font-semibold">
        <span v-if="!command" class="flex size-5 items-center justify-center rounded-full bg-primary/15 text-xs text-primary">3</span>{{ t('auto.stepPrompt') }}
      </p>
      <UTextarea ref="promptEl" v-model="form.prompt" :rows="5" autoresize class="w-full" :placeholder="fromChannel ? t('auto.promptChannelPlaceholder') : t('auto.promptPlaceholder')" />
      <div class="flex flex-wrap items-center gap-1 text-xs text-(--ui-text-muted)">
        {{ t('auto.insert') }}
        <button v-for="p in placeholders" :key="p" type="button" class="rounded bg-(--ui-bg-elevated) px-1.5 py-0.5 font-mono hover:text-(--ui-text)" @click="insert(p)">{{ p }}</button>
      </div>
    </section>

    <!-- where a run's answer goes besides office (a bot's chat) -->
    <section v-if="!fromChannel && isAdmin" class="space-y-3" :class="box">
      <p class="flex items-center gap-2 text-sm font-semibold"><UIcon name="i-lucide-send" class="size-4" />{{ t('auto.notifyTitle') }}</p>
      <div class="grid gap-3 @lg:grid-cols-2">
        <UFormField :label="t('auto.notifyBot')">
          <USelect v-model="notifyBot" :items="notifyItems" class="w-full" />
        </UFormField>
        <UFormField v-if="form.config.notify_channel_id" :label="notifyKind === 'telegram' ? t('alert.chatTelegram') : t('alert.chatDiscord')">
          <template #hint>
            <UTooltip :text="notifyKind === 'telegram' ? t('auto.chatIdTelegram') : t('auto.chatIdDiscord')"><UIcon name="i-lucide-info" class="size-4 text-(--ui-text-muted)" /></UTooltip>
          </template>
          <UInput v-model="form.config.notify_chat_id" class="w-full font-mono text-xs" placeholder="123456789012345678" />
        </UFormField>
      </div>
    </section>

    <!-- permission (ADR-074) -->
    <section v-if="form.action === 'chat'" class="space-y-3" :class="box">
      <p class="flex items-center gap-2 text-sm font-semibold"><UIcon name="i-lucide-shield" class="size-4" />{{ t('auto.permTitle') }}</p>
      <div v-if="isAdmin" class="flex flex-wrap gap-2">
        <button
          type="button" class="rounded-lg border px-2.5 py-1.5 text-sm transition"
          :class="form.permission_mode !== 'override' ? 'border-primary bg-primary/5 text-primary' : 'border-(--ui-border) text-(--ui-text-muted) hover:border-(--ui-border-accented)'"
          @click="form.permission_mode = 'agent'"
        >{{ t('auto.permModeAgent') }}</button>
        <button
          type="button" class="rounded-lg border px-2.5 py-1.5 text-sm transition"
          :class="form.permission_mode === 'override' ? 'border-primary bg-primary/5 text-primary' : 'border-(--ui-border) text-(--ui-text-muted) hover:border-(--ui-border-accented)'"
          @click="form.permission_mode = 'override'"
        >{{ t('auto.permModeOverride') }}</button>
      </div>
      <p v-else class="text-xs text-(--ui-text-muted)">{{ form.permission_mode === 'override' ? t('auto.permModeOverride') : t('auto.permModeAgent') }}</p>

      <p v-if="form.permission_mode !== 'override'" class="text-xs text-(--ui-text-muted)">
        <template v-if="effectiveAgent">
          {{ effectiveAgent.permissions.full_access ? t('auto.permEffectiveAdmin', { name: effectiveAgent.name }) : t('auto.permEffectiveNormal', { name: effectiveAgent.name }) }}
        </template>
      </p>
      <template v-else-if="isAdmin">
        <label class="flex items-start gap-2.5">
          <USwitch size="sm" class="mt-0.5" :model-value="form.override_full_access ?? false" @update:model-value="(v: boolean) => { form.override_full_access = v }" />
          <span class="min-w-0 flex-1">
            <span class="text-sm">{{ t('org.agent.fullAccess') }}</span>
            <span v-if="form.override_full_access && form.override_admin_by" class="block text-xs text-(--ui-text-muted) italic">{{ t('org.agent.fullAccessBy', { who: form.override_admin_by }) }}</span>
          </span>
        </label>
      </template>
      <p v-else class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)">
        <UIcon name="i-lucide-lock" class="size-4" />{{ t('org.agent.permissionsLocked') }}
      </p>

      <p v-if="effectiveFullAccess" class="flex items-start gap-1.5 rounded-md bg-(--ui-warning)/10 p-2 text-xs text-(--ui-warning)">
        <UIcon name="i-lucide-info" class="mt-0.5 size-4 shrink-0" />{{ t('auto.permBudgetWarning') }}
      </p>
    </section>

    <!-- limits -->
    <details class="group" :class="box">
      <summary class="flex cursor-pointer list-none items-center gap-2 text-sm font-semibold">
        <UIcon name="i-lucide-chevron-right" class="size-4 transition group-open:rotate-90" />{{ t('auto.stepLimits') }}
      </summary>
      <p class="mt-2 text-xs text-(--ui-text-muted)">{{ t('auto.limitsHelp') }}</p>
      <div class="mt-3 grid gap-3 @lg:grid-cols-3">
        <UFormField :label="t('auto.maxPerHour')"><UInputNumber v-model="form.limits.max_runs_per_hour" :min="0" class="w-full" /></UFormField>
        <UFormField :label="t('auto.dailyCost')"><UInputNumber v-model="form.limits.daily_cost_usd" :min="0" :step="0.5" class="w-full" /></UFormField>
        <UFormField :label="t('auto.disableAfter')"><UInputNumber v-model="form.limits.disable_after_failures" :min="0" class="w-full" /></UFormField>
      </div>
      <!-- runs at once and how long each (ADR-082): at its time with as many still going, none more and none stopped -->
      <div class="mt-3 grid gap-3 @lg:grid-cols-3">
        <UFormField :label="t('auto.maxParallel')" :help="form.keep_context ? t('auto.maxParallelKept') : t('auto.maxParallelHelp')">
          <UInputNumber v-model="form.limits.max_parallel" :min="1" :max="10" :disabled="form.keep_context" class="w-full" />
        </UFormField>
        <UFormField :label="t('auto.maxMinutes')" :help="t('auto.maxMinutesHelp')"><UInputNumber v-model="form.limits.max_minutes" :min="0" class="w-full" /></UFormField>
      </div>
      <div v-if="form.source === 'webhook'" class="mt-3 grid gap-3 @lg:grid-cols-3">
        <UFormField :label="t('auto.debounce')"><UInputNumber v-model="form.limits.debounce_seconds" :min="0" :max="3600" class="w-full" /></UFormField>
        <UFormField :label="t('auto.debounceKey')" :help="t('auto.debounceKeyHelp')"><UInput v-model="form.limits.debounce_key" class="w-full font-mono" placeholder="issue.key" /></UFormField>
        <UFormField :label="t('auto.debounceMax')"><UInputNumber v-model="form.limits.debounce_max_seconds" :min="0" class="w-full" /></UFormField>
      </div>
    </details>
  </div>
</template>
