<script setup lang="ts">
// One bot (ADR-049): how it is doing, its commands and every job they ran.
const route = useRoute()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const projectId = computed(() => route.params.id as string)
const bid = computed(() => route.params.bid as string)

const _f1 = useLiveFetch<{ channels: Channel[] }>(() => `/api/projects/${projectId.value}/channels`)
const { data: chData, refresh: refreshCh } = _f1
const _f2 = useLiveFetch<{ automations: Automation[] }>(() => `/api/projects/${projectId.value}/automations`)
const { data: autoData, refresh: refreshAutos } = _f2
await Promise.all([_f1, _f2]) // started together: one round trip, not 2 (a phone over a VPN)
const { data: agentsData } = useLiveFetch<{ agents: Agent[] }>(() => `/api/projects/${projectId.value}/chat/agents`, { lazy: true })
const bot = computed(() => chData.value?.channels.find(c => c.id === bid.value))
useFollowBot(() => bot.value?.state === 'connecting', () => refreshCh())
const cmds = computed(() => (autoData.value?.automations ?? []).filter(a => isChannelSource(a.source) && a.config.channel_id === bid.value)
  .sort((a, b) => Number(!!a.config.command) - Number(!!b.config.command)))
const name = computed(() => bot.value?.bot_name ? `@${bot.value.bot_name}` : t('bot.title'))
const agentName = (id: string) => agentsData.value?.agents.find(a => a.id === id)?.name ?? t('channels.agentLead')
function summary(a: Automation) {
  if (a.action === 'script') return t('bot.doScript')
  return t('bot.doReply', { agent: agentName(a.agent_id) })
}
const cmdLabel = (a: Automation) => a.config.command ? `/${bot.value?.kind === 'telegram' ? a.config.command.replace(/-/g, '_') : a.config.command}` : name.value
// the jobs of all its commands, and the messages none of them took
const jobFilter = computed(() => ({ origin_ids: [bid.value, ...cmds.value.map(a => a.id)] }))

async function act(fn: () => Promise<unknown>, ok?: string) {
  try {
    await fn()
    if (ok) toast.add({ title: ok, color: 'success' })
    await Promise.all([refreshCh(), refreshAutos()])
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const toggle = (enabled: boolean) => act(() => $fetch(`/api/channels/${bid.value}`, { method: 'PATCH', body: { enabled } }))
const reconnect = () => act(() => $fetch(`/api/channels/${bid.value}`, { method: 'PATCH', body: { enabled: true } }), t('auto.reconnecting'))
async function remove() {
  if (!confirm(t('bot.deleteConfirm', { name: name.value }))) return
  await act(async () => {
    for (const a of cmds.value) await $fetch(`/api/automations/${a.id}`, { method: 'DELETE' }) // the last one takes the bot along
  })
  await navigateTo({ path: `/projects/${projectId.value}`, query: { tab: 'automations' } })
}
</script>

<template>
  <PageShell :title="name">
    <template #actions>
      <template v-if="bot && isAdmin">
        <UButton size="sm" icon="i-lucide-pencil" :label="t('auto.edit')" :to="`/projects/${projectId}/bots/${bid}/edit`" />
        <UDropdownMenu
          :content="{ align: 'end' }"
          :items="[[{ label: t('auto.reconnect'), icon: 'i-lucide-refresh-cw', onSelect: reconnect }],
                   [{ label: t('auto.delete'), icon: 'i-lucide-trash', color: 'error' as const, onSelect: remove }]]"
        >
          <UButton icon="i-lucide-ellipsis" color="neutral" variant="ghost" :aria-label="t('project.actionsAria')" />
        </UDropdownMenu>
      </template>
    </template>

    <div v-if="bot" class="space-y-4">
      <UButton :to="{ path: `/projects/${projectId}`, query: { tab: 'automations' } }" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2" :label="t('auto.back')" />
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <UIcon :name="bot.kind === 'discord' ? 'i-lucide-gamepad-2' : 'i-lucide-send'" class="size-5 text-(--ui-text-muted)" />
        <span class="text-(--ui-text-muted)">{{ bot.kind === 'discord' ? 'Discord' : 'Telegram' }}</span>
        <span class="flex items-center gap-1.5 text-xs" :class="botStatus(bot, t).tone === 'error' ? 'text-(--ui-error)' : 'text-(--ui-text-muted)'">
          <span class="size-1.5 rounded-full" :class="botDot[botStatus(bot, t).tone]" />
          {{ botStatus(bot, t).text }}
        </span>
        <USwitch v-if="isAdmin" class="ms-auto" :model-value="bot.enabled" size="sm" @update:model-value="toggle" />
      </div>

      <!-- its commands, each with what it does and how its last run went -->
      <UCard :ui="{ body: 'p-0 sm:p-0' }">
        <NuxtLink
          v-for="a in cmds" :key="a.id" :to="`/projects/${projectId}/automations/${a.id}`"
          class="flex flex-wrap items-center gap-3 border-b border-(--ui-border) px-4 py-2.5 last:border-0 hover:bg-(--ui-bg-elevated)/50"
        >
          <span class="font-mono text-sm">{{ cmdLabel(a) }}<span v-if="a.config.command_arg" class="text-(--ui-text-dimmed)"> &lt;{{ a.config.command_arg }}&gt;</span></span>
          <UBadge v-if="!a.config.command" :label="t('bot.basic')" color="neutral" variant="subtle" size="sm" />
          <span class="min-w-0 flex-1 truncate text-xs text-(--ui-text-muted)">→ {{ summary(a) }}</span>
          <UBadge v-if="!a.enabled" :label="t('auto.off')" color="neutral" variant="subtle" size="sm" />
          <ChatTags v-if="a.config.tags?.length" :tags="a.config.tags" />
          <JobStatusBadge v-if="a.last_job && a.last_job.status !== 'done'" :status="a.last_job.status" />
        </NuxtLink>
      </UCard>

      <p class="text-sm font-medium">{{ t('auto.history') }}</p>
      <JobsTable :key="jobFilter.origin_ids.join()" :filter="jobFilter" />
    </div>
  </PageShell>
</template>
