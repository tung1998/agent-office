<script setup lang="ts">
// Where the AI connections' limit alerts go (ADR-069): a bot's chat, from how full.
interface Bot { id: string, name: string, kind: 'discord' | 'telegram', project: string }
const { t } = useLang()
const toast = useToast()
const { data, refresh } = useLiveFetch<{ channel_id: string, chat_id: string, threshold: number, bots: Bot[] }>('/api/limit-alert', { lazy: true })
const form = reactive({ channel_id: '', chat_id: '', threshold: 80 })
const { stale, reset: resync } = useDraft(data, form, d => Object.assign(form, { channel_id: d.channel_id, chat_id: d.chat_id, threshold: d.threshold })) // never over what is being typed
const NONE = '__none'
const bot = computed({ get: () => form.channel_id || NONE, set: (v: string) => { form.channel_id = v === NONE ? '' : v } })
const botItems = computed(() => [{ label: t('alert.off'), value: NONE }, ...(data.value?.bots ?? []).map(b => ({ label: `${b.name} · ${b.kind === 'discord' ? 'Discord' : 'Telegram'} · ${b.project}`, value: b.id }))])
const kind = computed(() => data.value?.bots.find(b => b.id === form.channel_id)?.kind)
const saving = ref(false)
async function save(test = false) {
  saving.value = true
  try {
    await $fetch('/api/limit-alert', { method: 'PUT', body: { ...form } })
    if (test) await $fetch('/api/limit-alert/test', { method: 'POST' })
    toast.add({ title: test ? t('alert.tested') : t('alert.saved'), color: 'success' })
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
      <UIcon name="i-lucide-gauge" class="size-4 text-(--ui-text-muted)" />
      <p class="font-medium">{{ t('alert.title') }}</p>
      <UTooltip :text="t('alert.help')"><UIcon name="i-lucide-info" class="size-4 text-(--ui-text-muted)" /></UTooltip>
    </div>
    <StaleNotice :show="stale" @reload="resync" />
    <USkeleton v-if="!data" class="h-10 w-full" />
    <div v-else class="grid gap-3 sm:grid-cols-[1fr_12rem_7rem_auto] sm:items-end">
      <UFormField :label="t('alert.bot')">
        <USelect v-model="bot" :items="botItems" class="w-full" />
      </UFormField>
      <UFormField :label="kind === 'telegram' ? t('alert.chatTelegram') : t('alert.chatDiscord')">
        <UInput v-model="form.chat_id" class="w-full font-mono text-xs" :disabled="!form.channel_id" placeholder="123456789012345678" />
      </UFormField>
      <UFormField :label="t('alert.threshold')">
        <UInputNumber v-model="form.threshold" :min="50" :max="95" :step="5" class="w-full" />
      </UFormField>
      <div class="flex gap-2">
        <UButton color="neutral" variant="outline" icon="i-lucide-send" :label="t('alert.test')" :disabled="!form.channel_id || !form.chat_id" :loading="saving" @click="save(true)" />
        <UButton icon="i-lucide-save" :label="t('common.save')" :loading="saving" @click="save()" />
      </div>
    </div>
  </UCard>
</template>
