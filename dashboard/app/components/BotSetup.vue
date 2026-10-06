<script setup lang="ts">
// A Telegram / Discord bot and its commands (ADR-049): the bot's own settings
// (shared), then its commands, each opening onto what it does and sends. Each
// command is one automation underneath: "@bot" (a tag or a DM) is the basic
// one, the "/" ones are custom; the office's own commands are listed, fixed.
const props = defineProps<{ projectId: string, botId: string }>()
const toast = useToast()
const saveError = useSaveError()
const { t } = useLang()
const isNew = computed(() => props.botId === 'new')

const _f1 = useLiveFetch<{ channels: Channel[] }>(() => `/api/projects/${props.projectId}/channels`)
const { data: chData, refresh: refreshCh } = _f1
const _f2 = useLiveFetch<{ automations: Automation[] }>(() => `/api/projects/${props.projectId}/automations`)
const { data: autoData } = _f2
await Promise.all([_f1, _f2]) // started together: one round trip, not 2 (a phone over a VPN)
const { data: agentsData } = useLiveFetch<{ agents: Agent[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const channel = computed(() => chData.value?.channels.find(c => c.id === props.botId))
useFollowBot(() => channel.value?.state === 'connecting', () => refreshCh())

// the bot's settings
const bot = reactive({ kind: (channel.value?.kind ?? 'discord') as 'telegram' | 'discord', token: '', allow: (channel.value?.allow ?? []).join('\n'), refusal: channel.value?.refusal ?? '',
  approvers: (channel.value?.approvers ?? []).join('\n'),
  // the line on top of its answers ("" = the default, "-" = none)
  headerOn: channel.value?.header !== '-', header: channel.value?.header === '-' ? '' : (channel.value?.header ?? ''),
  // what a run shows: the answer only, or its steps too (as the CLI); a command may override it
  replyMode: (channel.value?.reply_mode ?? '') as '' | 'steps' })
const replyModes = computed(() => [
  { value: '' as const, label: t('bot.replyAnswer'), icon: 'i-lucide-message-square' },
  { value: 'steps' as const, label: t('bot.replySteps'), icon: 'i-lucide-list-tree' }
])
// a command's own: the bot's (default) or one of the two
const cmdReplyModes = computed(() => [
  { value: 'inherit', label: t('bot.replyInherit', { mode: bot.replyMode === 'steps' ? t('bot.replySteps') : t('bot.replyAnswer') }) },
  { value: 'answer', label: t('bot.replyAnswer') },
  { value: 'steps', label: t('bot.replySteps') }
])
const ids = (s: string) => s.split(/[\n,]/).map(x => x.trim()).filter(Boolean)
// the bot's settings as they were read: a save over someone else's change is refused (409)
let botVersion = channel.value?.version
const settingsOpen = ref(isNew.value)
const guideOpen = ref(false)

// its commands: drafts of their automations
interface Cmd { key: string, id?: string, draft: AutomationDraft, open: boolean, version?: string }
let seq = 0
function draftFor(command: string): AutomationDraft {
  const d = emptyDraft()
  d.source = bot.kind
  d.action = 'chat'
  d.config.channel_id = isNew.value ? '' : props.botId
  d.config.command = command
  return d
}
const cmds = ref<Cmd[]>([])
const removed: string[] = []
{
  const mine = (autoData.value?.automations ?? []).filter(a => isChannelSource(a.source) && a.config.channel_id === props.botId && !isNew.value)
  cmds.value = mine.map(a => ({ key: `k${seq++}`, id: a.id, version: a.version, draft: draftFrom(a), open: false }))
  cmds.value.sort((a, b) => Number(!!a.draft.config.command) - Number(!!b.draft.config.command)) // "@bot" first
  if (!cmds.value.some(c => !c.draft.config.command)) cmds.value.unshift({ key: `k${seq++}`, draft: draftFor(''), open: isNew.value })
}
function addCommand() {
  cmds.value.forEach((c) => { c.open = false })
  const d = draftFor('')
  d.config.command = ' ' // a command, its name still to type
  cmds.value.push({ key: `k${seq++}`, draft: d, open: true })
}
// the project's skills as commands: pick some (or all), each becomes "/skill <text>"
interface Skill { name: string, description: string, source: string }
const skillsOpen = ref(false)
const { data: skillsData } = useLiveFetch<{ skills: Skill[] }>(() => `/api/projects/${props.projectId}/skills`, { lazy: true })
const skills = computed(() => skillsData.value?.skills ?? [])
const hasSkill = (name: string) => cmds.value.some(c => c.draft.config.skill === name)
const picked = ref<string[]>([])
const addable = computed(() => skills.value.filter(s => !hasSkill(s.name)))
const allPicked = computed({
  get: () => addable.value.length > 0 && addable.value.every(s => picked.value.includes(s.name)),
  set: (v: boolean) => { picked.value = v ? addable.value.map(s => s.name) : [] }
})
function togglePick(name: string, v: boolean) {
  picked.value = v ? [...picked.value, name] : picked.value.filter(n => n !== name)
}
function openSkills() {
  picked.value = []
  skillsOpen.value = true
}
function addSkills() {
  const taken = new Set(cmds.value.map(c => commandName(c.draft.config.command ?? '')))
  for (const s of skills.value.filter(x => picked.value.includes(x.name))) {
    let name = commandName(s.name.replace(/:/g, '-')) || 'skill'
    for (let i = 2; taken.has(name); i++) name = `${commandName(s.name.replace(/:/g, '-')).slice(0, 29)}-${i}`
    taken.add(name)
    const d = draftFor(name)
    d.config.skill = s.name
    d.config.command_description = s.description.slice(0, 100)
    d.config.command_arg = t('auto.cmdArgDefault')
    cmds.value.push({ key: `k${seq++}`, draft: d, open: false })
  }
  skillsOpen.value = false
}
function removeCommand(c: Cmd) {
  if (c.id) removed.push(c.id)
  cmds.value = cmds.value.filter(x => x !== c)
}
watch(() => bot.kind, (k) => { cmds.value.forEach((c) => { c.draft.source = k }) })

const botLabel = computed(() => channel.value?.bot_name ? `@${channel.value.bot_name}` : '@bot')
const cmdLabel = (name: string) => bot.kind === 'telegram' ? name.replace(/-/g, '_') : name
// a name as the "/" menus take it (the server makes it safe the same way)
const commandName = (s: string) => s.replace(/^\/+/, '').normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/đ/gi, 'd') // i18n-ignore: the letter đ, not text
  .toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32)
const agentName = (id: string) => agentsData.value?.agents.find(a => a.id === id)?.name ?? t('channels.agentLead')
function summary(c: Cmd) {
  const d = c.draft
  if (d.action === 'script') return t('bot.doScript')
  return t('bot.doReply', { agent: agentName(d.agent_id) })
}
const systemCommands = computed(() => [
  { name: 'create-conversation', arg: '', desc: t('cmd.create') },
  { name: 'close-conversation', arg: '', desc: t('cmd.close') },
  ...(bot.kind === 'discord' ? [{ name: 'create-thread', arg: t('cmd.threadArg'), desc: t('cmd.thread') }] : []),
  { name: 'pending', arg: '', desc: t('cmd.pending') },
  { name: 'approve', arg: t('cmd.numberArg'), desc: t('cmd.approve') },
  { name: 'reject', arg: t('cmd.numberArg'), desc: t('cmd.reject') },
  { name: 'mode', arg: 'ask | direct', desc: t('cmd.mode') }
])
const keywordsText = (c: Cmd) => (c.draft.config.keywords ?? []).join(', ')
const setKeywords = (c: Cmd, v: string) => { c.draft.config.keywords = v.split(/[,\n]/).map(s => s.trim()).filter(Boolean) }

// the chat next to it fills the command that is open
const openCmd = computed(() => cmds.value.find(c => c.open))
const highlight = ref<string[]>([])
function applyPatch(p: Record<string, unknown>) {
  const c = openCmd.value
  if (!c) return toast.add({ title: t('bot.openOneFirst'), color: 'warning' })
  const changed = mergeDraft(c.draft, p)
  if (!changed.length) return
  highlight.value = changed
  setTimeout(() => { highlight.value = [] }, 4000)
  toast.add({ title: t('auto.filled', { n: changed.length }), color: 'info' })
}
const pageContext = () => JSON.stringify({
  page: 'automation.bot', bot: { kind: bot.kind, name: botLabel.value },
  commands: cmds.value.map(c => ({ command: c.draft.config.command ? `/${c.draft.config.command}` : botLabel.value, action: c.draft.action, open: c.open })),
  draft: openCmd.value ? { ...automationBody(openCmd.value.draft), bot: undefined } : null
})

// Save: the first command carries the bot (made or updated with it), the
// others name it; removed commands go last (the last one takes the bot along)
const saving = ref(false)
async function save() {
  const bad = cmds.value.find(c => c.draft.config.command !== '' && !commandName(c.draft.config.command ?? ''))
  if (bad) return toast.add({ title: t('bot.needName'), color: 'error' })
  saving.value = true
  let channelId = isNew.value ? '' : props.botId
  // one command at a time: when one fails, the ones before it are saved (their
  // ids kept, so Save again goes on from there) and the person is told which
  let saved = 0
  let at: Cmd | undefined
  try {
    for (const [i, c] of cmds.value.entries()) {
      at = c
      const d = c.draft
      d.source = bot.kind
      d.config.channel_id = channelId
      d.config.command = d.config.command ? commandName(d.config.command) : ''
      d.name = d.config.command ? `/${d.config.command}` : t('bot.tagName', { bot: botLabel.value })
      const body = { ...automationBody(d), version: c.version }
      body.bot = i === 0 ? { token: bot.token || undefined, allow: ids(bot.allow), refusal: bot.refusal, approvers: ids(bot.approvers), header: bot.headerOn ? bot.header.trim() : '-', reply_mode: bot.replyMode, version: botVersion } : undefined
      const res = c.id
        ? await $fetch<{ automation: Automation }>(`/api/automations/${c.id}`, { method: 'PATCH', body })
        : await $fetch<{ automation: Automation }>(`/api/projects/${props.projectId}/automations`, { method: 'POST', body })
      c.id = res.automation.id
      c.version = res.automation.version // Save again after a failure goes on from here
      channelId = res.automation.config.channel_id ?? channelId
      if (i === 0) {
        await refreshCh()
        botVersion = chData.value?.channels.find(x => x.id === channelId)?.version
      }
      saved++
    }
    at = undefined
    for (const id of removed.splice(0)) await $fetch(`/api/automations/${id}`, { method: 'DELETE' })
    toast.add({ title: t('auto.saved'), color: 'success' })
    await navigateTo(`/projects/${props.projectId}/bots/${channelId}`) // its detail: how it runs
  } catch (e) {
    if (at && saved > 0) {
      at.open = true // the command that failed, opened to fix it
      toast.add({ title: t('bot.savedPartly', { n: saved, total: cmds.value.length, cmd: at.draft.name || at.draft.config.command || '' }), description: apiError(e), color: 'warning' })
    } else {
      if (at) at.open = true
      saveError(e, () => reloadNuxtApp())
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="grid gap-4 lg:h-[calc(100vh-9rem)] lg:min-h-[36rem] lg:grid-cols-2">
    <div class="flex flex-col lg:min-h-0">
      <div class="@container space-y-4 lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pe-1">
        <!-- 1. the bot -->
        <section class="space-y-3 rounded-xl border border-(--ui-border) p-4">
          <div class="flex flex-wrap items-center gap-2">
            <p class="flex items-center gap-2 text-sm font-semibold">
              <span class="flex size-5 items-center justify-center rounded-full bg-primary/15 text-xs text-primary">1</span>{{ t('bot.sectionBot') }}
            </p>
            <UButton
              class="ms-auto" size="xs" color="neutral" variant="ghost" icon="i-lucide-info"
              :label="bot.kind === 'discord' ? t('channels.guideDiscord') : t('channels.guideTelegram')" @click="guideOpen = true"
            />
          </div>
          <div v-if="isNew" class="grid grid-cols-2 gap-2">
            <button
              v-for="k in (['discord', 'telegram'] as const)" :key="k" type="button"
              class="flex items-center gap-2.5 rounded-lg border px-3 py-3 text-left transition"
              :class="bot.kind === k ? 'border-primary bg-primary/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'" @click="bot.kind = k"
            >
              <UIcon :name="k === 'discord' ? 'i-lucide-gamepad-2' : 'i-lucide-send'" class="size-5" :class="bot.kind === k ? 'text-primary' : 'text-(--ui-text-muted)'" />
              <span class="font-medium">{{ k === 'discord' ? 'Discord' : 'Telegram' }}</span>
            </button>
          </div>
          <div v-else-if="channel" class="flex flex-wrap items-center gap-2 text-sm">
            <UIcon :name="channel.kind === 'discord' ? 'i-lucide-gamepad-2' : 'i-lucide-send'" class="size-5 text-(--ui-text-muted)" />
            <span class="font-medium">{{ botLabel }}</span>
            <span class="text-(--ui-text-muted)">· {{ channel.kind === 'discord' ? 'Discord' : 'Telegram' }}</span>
            <span class="flex items-center gap-1.5 text-xs" :class="botStatus(channel, t).tone === 'error' ? 'text-(--ui-error)' : 'text-(--ui-text-muted)'">
              <span class="size-1.5 rounded-full" :class="botDot[botStatus(channel, t).tone]" />
              {{ botStatus(channel, t).text }}
            </span>
          </div>
          <UButton
            v-if="!isNew" size="xs" color="neutral" variant="ghost" class="-ms-2" :icon="settingsOpen ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'"
            :label="t('auto.tabBot')" @click="settingsOpen = !settingsOpen"
          />
          <div v-show="settingsOpen" class="space-y-3">
            <UFormField :label="t('channels.token')" :help="bot.kind === 'discord' ? t('channels.tokenHelpDiscord') : t('channels.tokenHelpTelegram')" :required="isNew">
              <UInput
                v-model="bot.token" type="password" name="bot-token" autocomplete="new-password" class="w-full font-mono"
                :placeholder="isNew ? (bot.kind === 'discord' ? 'MTI3…' : '123456789:AAF…') : t('channels.tokenKept')"
              />
            </UFormField>
            <!-- two lists (ADR-081): admins run as the agent's own, users propose for an admin to approve -->
            <UFormField :label="t('bot.admins')" :help="t('bot.adminsHelp')">
              <UTextarea v-model="bot.approvers" :rows="1" autoresize class="w-full font-mono text-xs" :placeholder="bot.kind === 'discord' ? '123456789012345678' : '123456789'" />
            </UFormField>
            <UAlert v-if="ids(bot.approvers).includes('*')" color="error" variant="subtle" icon="i-lucide-shield-alert" :title="t('bot.adminsNoAnyone')" />
            <UFormField :label="t('channels.allow')" :help="bot.kind === 'discord' ? t('channels.allowHelpDiscord') : t('channels.allowHelpTelegram')">
              <UTextarea v-model="bot.allow" :rows="2" autoresize class="w-full font-mono text-xs" :placeholder="bot.kind === 'discord' ? '123456789012345678' : '123456789'" />
            </UFormField>
            <!-- "*": anyone may message it, proposing only -->
            <UAlert
              v-if="bot.allow.split(/[\n,]/).some(s => s.trim() === '*')" color="warning" variant="subtle" icon="i-lucide-shield-alert"
              :title="t('bot.anyoneTitle')" :description="t('bot.anyoneDesc')"
            />
            <!-- who answered, where: on top of each answer -->
            <UFormField :label="t('bot.header')" :help="bot.headerOn ? t('bot.headerHelp') : undefined">
              <div class="flex items-center gap-2">
                <USwitch v-model="bot.headerOn" />
                <UInput v-if="bot.headerOn" v-model="bot.header" class="min-w-0 flex-1 font-mono text-xs" placeholder="{agent} · {project} · {branch}" />
              </div>
            </UFormField>
            <!-- the commands' default: each may override it (Advanced) -->
            <UFormField :label="t('bot.replyMode')" :help="bot.replyMode === 'steps' ? t('bot.replyStepsHelp') : t('bot.replyAnswerHelp')">
              <div class="flex flex-wrap gap-2">
                <button
                  v-for="o in replyModes" :key="o.value" type="button" class="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-sm transition"
                  :class="bot.replyMode === o.value ? 'border-primary bg-primary/5 text-primary' : 'border-(--ui-border) text-(--ui-text-muted) hover:border-(--ui-border-accented)'"
                  @click="bot.replyMode = o.value"
                >
                  <UIcon :name="o.icon" class="size-4" />{{ o.label }}
                </button>
              </div>
            </UFormField>
            <UFormField :label="t('channels.refusal')" :help="t('channels.refusalHelp')">
              <UInput v-model="bot.refusal" class="w-full" :placeholder="t('channels.refusalPlaceholder')" />
            </UFormField>
          </div>
        </section>

        <!-- 2. its commands, each with what it does -->
        <section class="space-y-3 rounded-xl border border-(--ui-border) p-4">
          <p class="flex items-center gap-2 text-sm font-semibold">
            <span class="flex size-5 items-center justify-center rounded-full bg-primary/15 text-xs text-primary">2</span>{{ t('bot.sectionCommands') }}
          </p>
          <div class="overflow-hidden rounded-lg border border-(--ui-border)">
            <div v-for="c in cmds" :key="c.key" class="border-b border-(--ui-border) last:border-0">
              <div class="flex cursor-pointer items-center gap-2 px-3 py-2 hover:bg-(--ui-bg-elevated)/50" @click="c.open = !c.open">
                <UIcon :name="c.open ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" class="size-4 shrink-0 text-(--ui-text-muted)" />
                <span class="shrink-0 font-mono text-sm">
                  <template v-if="!c.draft.config.command">{{ botLabel }} <span class="text-(--ui-text-dimmed)">&lt;{{ t('cmd.tagArg') }}&gt;</span></template>
                  <template v-else>/{{ cmdLabel(commandName(c.draft.config.command) || '…') }}<span v-if="c.draft.config.command_arg" class="text-(--ui-text-dimmed)"> &lt;{{ c.draft.config.command_arg }}&gt;</span></template>
                </span>
                <UBadge v-if="!c.draft.config.command" :label="t('bot.basic')" color="neutral" variant="subtle" size="sm" />
                <UBadge v-else-if="c.draft.config.skill" :label="t('bot.skillBadge')" color="primary" variant="subtle" size="sm" icon="i-lucide-sparkles" />
                <span class="min-w-0 flex-1 truncate text-xs text-(--ui-text-muted)">→ {{ summary(c) }}</span>
                <USwitch v-model="c.draft.enabled" size="sm" @click.stop />
                <UButton
                  v-if="c.draft.config.command" size="xs" color="neutral" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('common.delete')"
                  @click.stop="removeCommand(c)"
                />
              </div>
              <div v-if="c.open" class="space-y-3 border-t border-(--ui-border) bg-(--ui-bg-elevated)/20 p-3">
                <!-- the trigger: a tag (optionally only some messages) or the command's name -->
                <details v-if="!c.draft.config.command" class="group">
                  <summary class="flex cursor-pointer list-none items-center gap-1.5 text-xs font-medium text-(--ui-text-muted)">
                    <UIcon name="i-lucide-chevron-right" class="size-3.5 transition group-open:rotate-90" />{{ t('bot.onlySome') }}
                  </summary>
                  <div class="mt-2 grid gap-3 @lg:grid-cols-2">
                    <UFormField :label="t('auto.keywords')" :help="t('auto.keywordsHelp')">
                      <UInput :model-value="keywordsText(c)" class="w-full" :placeholder="t('auto.keywordsPlaceholder')" @update:model-value="(v: string | number) => setKeywords(c, String(v))" />
                    </UFormField>
                    <UFormField :label="t('auto.scope')" :help="t('auto.scopeHelp')">
                      <UInput v-model="c.draft.config.scope" class="w-full" :placeholder="t('auto.scopePlaceholder')" />
                    </UFormField>
                  </div>
                </details>
                <template v-else>
                  <p v-if="c.draft.config.skill" class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)">
                    <UIcon name="i-lucide-sparkles" class="size-3.5 text-primary" />{{ t('bot.callsSkill', { skill: c.draft.config.skill }) }}
                  </p>
                  <div class="grid gap-3 @lg:grid-cols-2">
                    <UFormField :label="t('auto.cmdName')" :help="t('auto.cmdNameHelp')" required>
                      <UInput
                        :model-value="c.draft.config.command.trim()" class="w-full font-mono" placeholder="don-hang"
                        @update:model-value="(v: string | number) => { c.draft.config.command = commandName(String(v)) || ' ' }"
                      >
                        <template #leading><span class="font-mono text-(--ui-text-muted)">/</span></template>
                      </UInput>
                    </UFormField>
                    <UFormField :label="t('auto.cmdDescription')">
                      <UInput v-model="c.draft.config.command_description" class="w-full" :placeholder="t('auto.cmdDescriptionPlaceholder')" />
                    </UFormField>
                  </div>
                  <UFormField :label="t('bot.cmdArg')" :help="t('bot.cmdArgHelp')">
                    <UInput v-model="c.draft.config.command_arg" class="w-full @lg:w-64" :placeholder="t('auto.cmdArgDefault')" />
                  </UFormField>
                </template>
                <!-- what it does and sends -->
                <AutomationForm :project-id="projectId" :form="c.draft" :highlight="highlight" command />
                <!-- Advanced: this command's own way over the bot's -->
                <details v-if="c.draft.action === 'chat'" class="group" :open="!!c.draft.config.reply_mode">
                  <summary class="flex cursor-pointer list-none items-center gap-1.5 text-xs font-medium text-(--ui-text-muted)">
                    <UIcon name="i-lucide-chevron-right" class="size-3.5 transition group-open:rotate-90" />{{ t('bot.advanced') }}
                  </summary>
                  <UFormField :label="t('bot.replyMode')" class="mt-2">
                    <USelect
                      :model-value="c.draft.config.reply_mode || 'inherit'" :items="cmdReplyModes" class="w-full @lg:w-72"
                      @update:model-value="(v: string) => { c.draft.config.reply_mode = v === 'inherit' ? '' : v as 'answer' | 'steps' }"
                    />
                  </UFormField>
                </details>
              </div>
            </div>
            <!-- the office's own: listed, fixed -->
            <div v-for="s in systemCommands" :key="s.name" class="flex items-center gap-2 border-b border-(--ui-border) bg-(--ui-bg-elevated)/30 px-3 py-2 text-sm last:border-0">
              <UIcon name="i-lucide-lock" class="size-4 shrink-0 text-(--ui-text-dimmed)" />
              <span class="shrink-0 font-mono">/{{ cmdLabel(s.name) }}<span v-if="s.arg" class="text-(--ui-text-dimmed)"> &lt;{{ s.arg }}&gt;</span></span>
              <span class="min-w-0 flex-1 truncate text-xs text-(--ui-text-muted)">{{ s.desc }}</span>
            </div>
          </div>
          <div class="flex flex-wrap gap-2">
            <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-plus" :label="t('bot.addCommand')" @click="addCommand" />
            <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-sparkles" :label="t('bot.addSkills')" @click="openSkills" />
          </div>
          <p class="text-xs text-(--ui-text-muted)">{{ t('auto.replyNoTools') }}</p>
        </section>
      </div>
      <div class="flex justify-end gap-2 border-t border-(--ui-border) pt-3">
        <UButton color="neutral" variant="ghost" :label="t('org.form.close')" :to="{ path: `/projects/${projectId}`, query: { tab: 'automations' } }" />
        <UButton icon="i-lucide-save" :loading="saving" :label="t('auto.save')" @click="save" />
      </div>
    </div>
    <div class="h-[32rem] lg:h-auto lg:min-h-0">
      <ChatPanel :project-id="projectId" purpose="automation" :page-context="pageContext" @automation-patch="applyPatch" />
    </div>
    <UModal v-model:open="skillsOpen" :title="t('bot.addSkills')" :ui="{ content: 'max-w-xl' }">
      <template #body>
        <p v-if="!skills.length" class="text-sm text-(--ui-text-muted)">{{ t('bot.noSkills') }}</p>
        <div v-else class="space-y-2">
          <UCheckbox v-model="allPicked" :label="t('bot.pickAll', { n: addable.length })" :disabled="!addable.length" />
          <div class="max-h-96 divide-y divide-(--ui-border) overflow-y-auto rounded-lg border border-(--ui-border)">
            <label v-for="s in skills" :key="s.name" class="flex cursor-pointer items-start gap-2.5 px-3 py-2" :class="hasSkill(s.name) ? 'opacity-60' : 'hover:bg-(--ui-bg-elevated)/50'">
              <UCheckbox
                :model-value="hasSkill(s.name) || picked.includes(s.name)" :disabled="hasSkill(s.name)" class="mt-0.5"
                @update:model-value="(v: boolean | 'indeterminate') => togglePick(s.name, v === true)"
              />
              <span class="min-w-0 flex-1">
                <span class="flex items-center gap-1.5">
                  <span class="font-mono text-sm">/{{ cmdLabel(commandName(s.name.replace(/:/g, '-'))) }}</span>
                  <UBadge :label="s.source" color="neutral" variant="outline" size="sm" />
                  <span v-if="hasSkill(s.name)" class="text-xs text-(--ui-text-muted)">{{ t('bot.already') }}</span>
                </span>
                <span class="line-clamp-2 block text-xs text-(--ui-text-muted)">{{ s.description }}</span>
              </span>
            </label>
          </div>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="skillsOpen = false" />
          <UButton :label="t('bot.addN', { n: picked.length })" :disabled="!picked.length" @click="addSkills" />
        </div>
      </template>
    </UModal>
    <UModal v-model:open="guideOpen" :title="bot.kind === 'discord' ? t('channels.guideDiscord') : t('channels.guideTelegram')">
      <template #body><BotGuide :kind="bot.kind" plain /></template>
    </UModal>
  </div>
</template>
