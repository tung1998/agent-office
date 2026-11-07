<script setup lang="ts">
// Where incidents (monitor down, process crashed, MCP server broken) go when
// no dashboard tab is open to show the badge (ADR-076/077/078).
interface Bot { id: string, name: string, kind: 'discord' | 'telegram', project: string }
const { t } = useLang()
const toast = useToast()
const { data, refresh } = useLiveFetch<{ channel_id: string, chat_id: string, min_severity: 'error' | 'warning', base_url: string, bots: Bot[] }>('/api/incident-alert', { lazy: true })
const form = reactive({ channel_id: '', chat_id: '', min_severity: 'error' as 'error' | 'warning', base_url: '' })
const { stale, reset: resync } = useDraft(data, form, d => Object.assign(form, { channel_id: d.channel_id, chat_id: d.chat_id, min_severity: d.min_severity, base_url: d.base_url }))
const NONE = '__none'
const bot = computed({ get: () => form.channel_id || NONE, set: (v: string) => { form.channel_id = v === NONE ? '' : v } })
const botItems = computed(() => [{ label: t('alert.off'), value: NONE }, ...(data.value?.bots ?? []).map(b => ({ label: `${b.name} · ${b.kind === 'discord' ? 'Discord' : 'Telegram'} · ${b.project}`, value: b.id }))])
const kind = computed(() => data.value?.bots.find(b => b.id === form.channel_id)?.kind)
const severityItems = computed(() => [{ label: t('incidentAlert.severity.error'), value: 'error' }, { label: t('incidentAlert.severity.warning'), value: 'warning' }])
const saving = ref(false)
async function save() {
  if (saving.value) return
  saving.value = true
  try {
    await $fetch('/api/incident-alert', { method: 'PUT', body: { ...form } })
    toast.add({ title: t('incidentAlert.saved'), color: 'success' })
    await refresh()
    resync()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
    <div class="flex items-center gap-2">
      <UIcon name="i-lucide-siren" class="size-4 text-(--ui-text-muted)" />
      <p class="font-medium">{{ t('incidentAlert.title') }}</p>
      <InfoTip :text="t('incidentAlert.help')" icon-class="size-4 text-(--ui-text-muted)" />
    </div>
    <StaleNotice :show="stale" @reload="resync" />
    <USkeleton v-if="!data" class="h-10 w-full" />
    <div v-else class="grid gap-3 sm:grid-cols-[1fr_12rem_10rem_auto] sm:items-end">
      <UFormField :label="t('alert.bot')">
        <USelect v-model="bot" :items="botItems" class="w-full" />
      </UFormField>
      <UFormField :label="kind === 'telegram' ? t('alert.chatTelegram') : t('alert.chatDiscord')">
        <UInput v-model="form.chat_id" class="w-full font-mono text-xs" :disabled="!form.channel_id" placeholder="123456789012345678" />
      </UFormField>
      <UFormField :label="t('incidentAlert.minSeverity')">
        <USelect v-model="form.min_severity" :items="severityItems" class="w-full" />
      </UFormField>
      <div class="flex gap-2">
        <UButton icon="i-lucide-save" :label="t('common.save')" :loading="saving" @click="save()" />
      </div>
    </div>
    <UFormField v-if="data" :label="t('incidentAlert.baseUrl')" :help="t('incidentAlert.baseUrlHelp')">
      <UInput v-model="form.base_url" class="w-full font-mono text-xs" :placeholder="t('incidentAlert.baseUrlPlaceholder')" />
    </UFormField>
  </UCard>
</template>
