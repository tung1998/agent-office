<script setup lang="ts">
// A project's automations: schedules, webhooks and chat bots' messages that
// start a chat turn, a task or a script as a job (ADR-040, ADR-049).
const props = defineProps<{ projectId: string }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t, dateLocale } = useLang()

const { data, refresh } = await useLiveFetch<{ automations: Automation[] }>(() => `/api/projects/${props.projectId}/automations`)
const { data: agentsData } = useLiveFetch<{ agents: Agent[] }>(() => `/api/projects/${props.projectId}/chat/agents`, { lazy: true })
const list = computed(() => data.value?.automations ?? [])
// a bot is one row: its commands are its automations (ADR-049)
const others = computed(() => list.value.filter(a => !isChannelSource(a.source)))
const bots = computed(() => {
  const by = new Map<string, Automation[]>()
  for (const a of list.value) {
    if (isChannelSource(a.source) && a.config.channel_id) by.set(a.config.channel_id, [...(by.get(a.config.channel_id) ?? []), a])
  }
  return [...by.entries()].map(([id, cmds]) => {
    const st = cmds[0]!.bot_status
    const last = cmds.map(c => c.last_job).filter(Boolean).sort((x, y) => y!.created_at.localeCompare(x!.created_at))[0] ?? null
    return { id, cmds, kind: st?.kind ?? cmds[0]!.source, name: st?.bot_name ? `@${st.bot_name}` : t('bot.title'), error: st?.state === 'connecting' ? '' : (st?.last_error ?? ''), connecting: st?.state === 'connecting', last, enabled: st?.enabled ?? true }
  })
})
type BotRow = (typeof bots.value)[number]
useFollowBot(() => bots.value.some(b => b.connecting), () => refresh())
// on/off for the whole bot: off, it disconnects and hears nothing (its commands stay)
async function toggleBot(b: BotRow, enabled: boolean) {
  try {
    await $fetch(`/api/channels/${b.id}`, { method: 'PATCH', body: { enabled } })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function removeBot(b: BotRow) {
  if (!confirm(t('bot.deleteConfirm', { name: b.name }))) return
  try {
    for (const a of b.cmds) await $fetch(`/api/automations/${a.id}`, { method: 'DELETE' }) // the last one takes the bot along
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
function botMenu(b: BotRow) {
  return [
    [
      { label: t('auto.detail'), icon: 'i-lucide-eye', to: `/projects/${props.projectId}/bots/${b.id}` },
      { label: t('auto.edit'), icon: 'i-lucide-pencil', to: `/projects/${props.projectId}/bots/${b.id}/edit` },
      { label: t('auto.reconnect'), icon: 'i-lucide-refresh-cw', onSelect: () => reconnect(b.cmds[0]!) }
    ],
    [{ label: t('auto.delete'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => removeBot(b) }]
  ]
}
const agentName = (id: string) => agentsData.value?.agents.find(a => a.id === id)?.name ?? t('auto.lead')
// ADR-074: an automation's effective permission is full access when it
// overrides to one, or when it follows an agent that itself has one.
function isFullAccess(a: Automation): boolean {
  if (a.permission_mode === 'override') return !!a.override_full_access
  const agents = agentsData.value?.agents ?? []
  const agent = a.agent_id ? agents.find(x => x.id === a.agent_id) : (agents.find(x => x.tier === 'lead') ?? agents[0])
  return !!agent?.permissions.full_access
}
const when = (d?: string | null) => d ? new Date(d).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' }) : t('auto.never')


async function toggle(a: Automation, enabled: boolean) {
  try {
    await $fetch(`/api/automations/${a.id}`, { method: 'PATCH', body: automationBody({ ...a, enabled }) })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
// the bot stopped (a wrong token, an intent off): try again once it is fixed on Discord/Telegram
async function reconnect(a: Automation) {
  try {
    await $fetch(`/api/channels/${a.config.channel_id}`, { method: 'PATCH', body: { enabled: true } })
    toast.add({ title: t('auto.reconnecting'), color: 'info' })
    await refresh() // connecting now: followed until it runs or fails
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function remove(a: Automation) {
  if (!confirm(t('auto.deleteConfirm', { name: a.name }))) return
  try {
    await $fetch(`/api/automations/${a.id}`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
// the row's ⋯ menu: what fits this automation (a test run is for schedules and
// webhooks; a bot's automation runs on a message, its bot may need a reconnect)
function rowMenu(a: Automation) {
  const link = `/projects/${props.projectId}/automations/${a.id}`
  return [
    [
      { label: t('auto.detail'), icon: 'i-lucide-eye', to: link },
      { label: t('auto.edit'), icon: 'i-lucide-pencil', to: `${link}/edit` },
      ...(isChannelSource(a.source)
        ? [{ label: t('auto.reconnect'), icon: 'i-lucide-refresh-cw', onSelect: () => reconnect(a) }]
        : [{ label: t('auto.runNow'), icon: 'i-lucide-play', onSelect: () => runNow(a) }])
    ],
    [{ label: t('auto.delete'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => remove(a) }]
  ]
}
async function runNow(a: Automation) {
  try {
    await $fetch(`/api/automations/${a.id}/run`, { method: 'POST', body: {} })
    toast.add({ title: t('auto.ran'), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <div class="max-w-5xl space-y-3">
    <div class="flex items-center justify-between gap-2">
      <p class="text-sm text-(--ui-text-muted)">{{ list.length ? '' : t('auto.empty') }}</p>
      <UButton v-if="isAdmin" icon="i-lucide-plus" size="sm" :label="t('auto.new')" :to="`/projects/${projectId}/automations/new`" />
    </div>

    <UCard v-if="list.length" :ui="{ body: 'p-0 sm:p-0' }">
      <!-- bots: one row each, opening their setup -->
      <div v-for="b in bots" :key="b.id" class="flex flex-wrap items-center gap-3 border-b border-(--ui-border) px-4 py-3 last:border-0">
        <UIcon :name="b.kind === 'telegram' ? 'i-lucide-send' : 'i-lucide-gamepad-2'" class="size-5 shrink-0 text-(--ui-text-muted)" />
        <NuxtLink :to="`/projects/${projectId}/bots/${b.id}`" class="min-w-0 flex-1 hover:underline">
          <span class="block truncate font-medium">{{ b.name }}</span>
          <span class="block truncate text-xs text-(--ui-text-muted)">
            {{ b.kind === 'telegram' ? 'Telegram' : 'Discord' }} · {{ t('bot.commands', { n: b.cmds.length }) }}
            <template v-for="c in b.cmds.filter(x => x.config.command)" :key="c.id"> · /{{ c.config.command }}</template>
          </span>
        </NuxtLink>
        <UBadge v-if="b.error" color="error" variant="subtle" size="sm" icon="i-lucide-bot" :label="b.error" :title="b.error" class="max-w-64 truncate" />
        <UBadge v-else-if="b.connecting" color="warning" variant="subtle" size="sm" icon="i-lucide-loader-circle" :label="t('channels.connecting')" />
        <span v-else class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)">
          <JobStatusBadge v-if="b.last && b.last.status !== 'done'" :status="b.last.status" />
          {{ when(b.last?.created_at) }}
        </span>
        <USwitch v-if="isAdmin" :model-value="b.enabled" size="sm" @update:model-value="(v: boolean) => toggleBot(b, v)" />
        <UDropdownMenu v-if="isAdmin" :items="botMenu(b)" :content="{ align: 'end' }">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-ellipsis" :aria-label="t('chat.more')" />
        </UDropdownMenu>
      </div>
      <div v-for="a in others" :key="a.id" class="flex flex-wrap items-center gap-3 border-b border-(--ui-border) px-4 py-3 last:border-0">
        <UIcon :name="a.source === 'schedule' ? 'i-lucide-alarm-clock' : isChannelSource(a.source) ? (a.config.command ? 'i-lucide-square-slash' : 'i-lucide-messages-square') : 'i-lucide-webhook'" class="size-5 shrink-0 text-(--ui-text-muted)" />
        <NuxtLink :to="`/projects/${projectId}/automations/${a.id}`" class="min-w-0 flex-1 hover:underline">
          <span class="block truncate font-medium">{{ a.name }}</span>
          <span class="block truncate text-xs text-(--ui-text-muted)">
            {{ scheduleText(a, t) }}
            · {{ a.action === 'script' ? t('auto.toScript') : t('auto.toChat', { agent: agentName(a.agent_id) }) }}
          </span>
        </NuxtLink>
        <span v-if="a.config.tags?.length" class="flex flex-wrap items-center gap-1"><ChatTags :tags="a.config.tags" /></span>
        <UBadge v-if="isFullAccess(a)" color="warning" variant="subtle" size="sm" icon="i-lucide-shield-alert" :label="t('auto.adminBadge')" :title="t('auto.adminBadgeHint')" />
        <UBadge v-if="a.disabled_code" color="error" variant="subtle" size="sm" icon="i-lucide-circle-alert" :label="t('auto.disabledBy', { reason: a.disabled_reason })" class="max-w-64 truncate" />
        <UBadge v-else-if="a.bot_status?.last_error" color="error" variant="subtle" size="sm" icon="i-lucide-bot" :label="a.bot_status.last_error" :title="a.bot_status.last_error" class="max-w-64 truncate" />
        <span v-else class="flex items-center gap-1.5 text-xs text-(--ui-text-muted)">
          <JobStatusBadge v-if="a.last_job && a.last_job.status !== 'done'" :status="a.last_job.status" />
          {{ when(a.last_job?.created_at) }}
        </span>
        <USwitch v-if="isAdmin" :model-value="a.enabled" size="sm" @update:model-value="(v: boolean) => toggle(a, v)" />
        <UDropdownMenu v-if="isAdmin" :items="rowMenu(a)" :content="{ align: 'end' }">
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-ellipsis" :aria-label="t('chat.more')" />
        </UDropdownMenu>
      </div>
    </UCard>

  </div>
</template>
