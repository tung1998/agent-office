<script setup lang="ts">
// One automation: its state, actions, and the jobs it started.
const route = useRoute()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const projectId = computed(() => route.params.id as string)
const aid = computed(() => route.params.aid as string)

const { data, refresh } = await useLiveFetch<{ automation: Automation }>(() => `/api/automations/${aid.value}`)
const a = computed(() => data.value?.automation)
const secret = ref<{ url: string, secret: string, auth?: string, authName?: string } | null>(null)
// a bot's command goes back to its bot; others to the automations
const isBotCmd = computed(() => !!a.value && isChannelSource(a.value.source) && !!a.value.config.channel_id)
const back = computed(() => isBotCmd.value ? `/projects/${projectId.value}/bots/${a.value!.config.channel_id}` : { path: `/projects/${projectId.value}`, query: { tab: 'automations' } })
const backLabel = computed(() => isBotCmd.value ? (a.value!.bot_status?.bot_name ? `@${a.value!.bot_status.bot_name}` : t('bot.title')) : t('auto.back'))

async function act(fn: () => Promise<unknown>, ok?: string) {
  try {
    await fn()
    if (ok) toast.add({ title: ok, color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const runNow = () => act(() => $fetch(`/api/automations/${aid.value}/run`, { method: 'POST', body: {} }), t('auto.ran'))
const enable = () => act(() => $fetch(`/api/automations/${aid.value}`, { method: 'PATCH', body: automationBody({ ...a.value!, enabled: true }) }))
async function rotate() {
  if (!confirm(t('auto.rotateConfirm'))) return
  await act(async () => {
    const res = await $fetch<{ secret: string }>(`/api/automations/${aid.value}/rotate-secret`, { method: 'POST', body: {} })
    secret.value = { url: `${location.origin}${a.value!.webhook_url}`, secret: res.secret, auth: a.value!.config.auth, authName: a.value!.config.auth_name }
  })
}
async function remove() {
  if (!a.value || !confirm(t('auto.deleteConfirm', { name: a.value.name }))) return
  await act(() => $fetch(`/api/automations/${aid.value}`, { method: 'DELETE' }))
  await navigateTo({ path: `/projects/${projectId.value}`, query: { tab: 'automations' } })
}
</script>

<template>
  <PageShell :title="a?.name ?? ''">
    <template #actions>
      <template v-if="a && isAdmin">
        <UButton v-if="!isChannelSource(a.source)" size="sm" icon="i-lucide-play" :label="t('auto.runNow')" :title="t('auto.runNowHelp')" @click="runNow" />
        <UDropdownMenu
          :content="{ align: 'end' }"
          :items="[[
            { label: t('auto.edit'), icon: 'i-lucide-pencil', to: isBotCmd ? `/projects/${projectId}/bots/${a.config.channel_id}/edit` : `/projects/${projectId}/automations/${aid}/edit` },
            ...(a.source === 'webhook' ? [{ label: t('auto.rotate'), icon: 'i-lucide-key-round', onSelect: rotate }] : [])
          ], [{ label: t('auto.delete'), icon: 'i-lucide-trash', color: 'error' as const, onSelect: remove }]]"
        >
          <UButton icon="i-lucide-ellipsis" color="neutral" variant="ghost" :aria-label="t('project.actionsAria')" />
        </UDropdownMenu>
      </template>
    </template>

    <div v-if="a" class="space-y-4">
      <UButton :to="back" icon="i-lucide-arrow-left" size="xs" color="neutral" variant="ghost" class="-ms-2" :label="backLabel" />
      <div class="flex flex-wrap items-center gap-2 text-xs text-(--ui-text-muted)">
        <UIcon :name="a.source === 'schedule' ? 'i-lucide-alarm-clock' : isChannelSource(a.source) ? (a.config.command ? 'i-lucide-square-slash' : 'i-lucide-messages-square') : 'i-lucide-webhook'" class="size-4" />
        <span class="font-mono">{{ scheduleText(a, t) }}</span>
        <span>{{ a.action === 'script' ? t('auto.actionScript') : t('auto.actionChat') }}</span>
        <UBadge :label="a.enabled ? t('auto.on') : t('auto.off')" :color="a.enabled ? 'success' : 'neutral'" variant="subtle" size="sm" />
      </div>
      <UAlert
        v-if="a.disabled_code" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="t('auto.disabledBy', { reason: a.disabled_reason })"
        :actions="isAdmin ? [{ label: t('auto.enable'), color: 'error', variant: 'outline', onClick: enable }] : []"
      />
      <p class="text-sm font-medium">{{ t('auto.history') }}</p>
      <JobsTable :filter="{ origin_id: aid }" />
      <template v-if="isAdmin">
        <p class="text-sm font-medium">{{ t('audit.history') }}</p>
        <AuditLog :filter="{ resource: 'automation', resource_id: aid }" compact />
      </template>
    </div>

    <WebhookSecretModal :value="secret" @close="secret = null" />
  </PageShell>
</template>
