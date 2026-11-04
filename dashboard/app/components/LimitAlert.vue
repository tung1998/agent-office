<script setup lang="ts">
// Where the AI connections' limit alerts go (ADR-069): a bot's chat, from how full;
// and prompts that keep failing, in a row or too many within a while (ADR-151).
interface Bot { id: string, name: string, kind: 'discord' | 'telegram', project: string }
interface ErrorRule { streak: number, count: number, window: number }
const { t } = useLang()
const toast = useToast()
const { data, refresh } = useLiveFetch<{ channel_id: string, chat_id: string, threshold: number, errors?: ErrorRule, bots: Bot[] }>('/api/limit-alert', { lazy: true })
const form = reactive({ channel_id: '', chat_id: '', threshold: 80, errors: { streak: 3, count: 5, window: 30 } as ErrorRule })
const { stale, reset: resync } = useDraft(data, form, d => Object.assign(form, { channel_id: d.channel_id, chat_id: d.chat_id, threshold: d.threshold, errors: { streak: 3, count: 5, window: 30, ...d.errors } })) // never over what is being typed
const NONE = '__none'
const bot = computed({ get: () => form.channel_id || NONE, set: (v: string) => { form.channel_id = v === NONE ? '' : v } })
const botItems = computed(() => [{ label: t('alert.off'), value: NONE }, ...(data.value?.bots ?? []).map(b => ({ label: `${b.name} · ${b.kind === 'discord' ? 'Discord' : 'Telegram'} · ${b.project}`, value: b.id }))])
const kind = computed(() => data.value?.bots.find(b => b.id === form.channel_id)?.kind)
const saving = ref(false)
// a test needs a bot and a chat: pressing it without them marks the field
const testCheck = useFormCheck(() => ({
  bot: !form.channel_id && t('common.pickOne'),
  chat: !form.chat_id.trim() && t('common.required')
}))
async function save(test = false) {
  if (saving.value || (test && !testCheck.check())) return
  if (!test) testCheck.reset()
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
      <InfoTip :text="t('alert.help')" icon-class="size-4 text-(--ui-text-muted)" />
    </div>
    <StaleNotice :show="stale" @reload="resync" />
    <USkeleton v-if="!data" class="h-10 w-full" />
    <div v-else class="grid gap-3 sm:grid-cols-[1fr_12rem_7rem_auto] sm:items-end">
      <UFormField :label="t('alert.bot')" data-field="bot" :error="testCheck.errors.value.bot">
        <USelect v-model="bot" :items="botItems" class="w-full" />
      </UFormField>
      <UFormField :label="kind === 'telegram' ? t('alert.chatTelegram') : t('alert.chatDiscord')" data-field="chat" :error="testCheck.errors.value.chat">
        <UInput v-model="form.chat_id" class="w-full font-mono text-xs" :disabled="!form.channel_id" placeholder="123456789012345678" />
      </UFormField>
      <UFormField :label="t('alert.threshold')">
        <UInputNumber v-model="form.threshold" :min="50" :max="95" :step="5" class="w-full" />
      </UFormField>
      <div class="flex gap-2">
        <UButton color="neutral" variant="outline" icon="i-lucide-send" :label="t('alert.test')" :loading="saving" @click="save(true)" />
        <UButton icon="i-lucide-save" :label="t('common.save')" :loading="saving" @click="save()" />
      </div>
    </div>
    <!-- failed prompts: N in a row, or N within M minutes (0 = that rule off) -->
    <div v-if="data" class="flex flex-wrap items-center gap-x-2 gap-y-2 text-sm">
      <UIcon name="i-lucide-triangle-alert" class="size-4 text-(--ui-text-muted)" />
      <span>{{ t('alert.errors') }}</span>
      <InfoTip :text="t('alert.errorsHelp')" icon-class="size-4 text-(--ui-text-muted)" />
      <UInputNumber v-model="form.errors.streak" :min="0" :max="100" size="sm" class="w-24" />
      <span>{{ t('alert.inARow') }}</span>
      <span class="text-(--ui-text-muted)">{{ t('alert.or') }}</span>
      <UInputNumber v-model="form.errors.count" :min="0" :max="1000" size="sm" class="w-24" />
      <span>{{ t('alert.errorsWithin') }}</span>
      <UInputNumber v-model="form.errors.window" :min="1" :max="1440" :step="5" size="sm" class="w-24" :disabled="!form.errors.count" />
      <span>{{ t('alert.minutes') }}</span>
    </div>
  </UCard>
</template>
