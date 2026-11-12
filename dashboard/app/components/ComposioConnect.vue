<script setup lang="ts">
// The office's Composio connection for an "app events" webhook (ADR-282):
// its API key, the events to pick from the apps' trigger types, and one press
// that points the Composio project's webhook at this automation (signed).
const props = defineProps<{ automationId?: string }>()
const events = defineModel<string[]>({ default: () => [] })
const { t } = useLang()
const toast = useToast()

type Conn = { connected: boolean, key_hint: string, automation_id: string, automation_name?: string, signed: boolean }
type Trigger = { slug: string, name: string, description: string, toolkit: string }
const { data: conn, error, refresh } = useLiveFetch<Conn>('/api/composio', { lazy: true })

const key = ref('')
const busy = ref<'' | 'connect' | 'disconnect' | 'load' | 'subscribe'>('')
async function connect() {
  busy.value = 'connect'
  try {
    await $fetch('/api/composio', { method: 'PUT', body: { api_key: key.value } })
    key.value = ''
    await refresh()
    toast.add({ title: t('composio.connected'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
async function disconnect() {
  if (!confirm(t('composio.disconnectConfirm'))) return
  busy.value = 'disconnect'
  try {
    await $fetch('/api/composio', { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}

// the apps' trigger types: picked ones go into the list of events
const toolkits = ref('gmail, slack, github')
const triggers = ref<Trigger[] | null>(null)
async function load() {
  busy.value = 'load'
  try {
    const res = await $fetch<{ triggers: Trigger[] }>('/api/composio/triggers', { query: { toolkit: toolkits.value.replace(/\s+/g, '') } })
    triggers.value = res.triggers
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
const picked = (slug: string) => events.value.includes(slug)
function toggle(slug: string) {
  const rest = events.value.filter(e => e !== slug && e !== '*')
  events.value = picked(slug) ? (rest.length ? rest : ['*']) : [...rest, slug]
}

// where Composio sends: this office as the internet sees it (a tunnel)
const baseURL = ref(typeof location === 'undefined' ? '' : location.origin)
const isHere = computed(() => !!props.automationId && conn.value?.automation_id === props.automationId)
async function subscribe() {
  if (conn.value?.automation_id && !isHere.value && !confirm(t('composio.moveConfirm', { name: conn.value.automation_name ?? conn.value.automation_id }))) return
  busy.value = 'subscribe'
  try {
    await $fetch(`/api/automations/${props.automationId}/composio`, { method: 'POST', body: { base_url: baseURL.value } })
    await refresh()
    toast.add({ title: t('composio.subscribed'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <div class="space-y-3 rounded-lg border border-(--ui-border) p-3">
    <USkeleton v-if="!conn && !error" class="h-8 w-full" />
    <UAlert v-else-if="error" color="error" variant="subtle" :title="apiError(error)">
      <template #actions><UButton size="xs" :label="t('common.refresh')" @click="refresh()" /></template>
    </UAlert>
    <template v-else-if="conn && !conn.connected">
      <p class="text-sm text-(--ui-text-muted)">{{ t('composio.intro') }}</p>
      <div class="flex gap-2">
        <UInput v-model="key" type="password" class="flex-1 font-mono" placeholder="ak_…" autocomplete="off" />
        <UButton :label="t('composio.connect')" icon="i-lucide-plug" :loading="busy === 'connect'" :disabled="!key.trim() || !!busy" @click="connect" />
      </div>
    </template>
    <template v-else-if="conn">
      <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
        <span class="flex items-center gap-1.5"><UIcon name="i-lucide-plug-zap" class="text-(--ui-success)" />{{ t('composio.connectedAs', { key: conn.key_hint }) }}</span>
        <UButton size="xs" color="neutral" variant="ghost" :label="t('composio.disconnect')" :loading="busy === 'disconnect'" :disabled="!!busy" @click="disconnect" />
      </div>
      <UFormField :label="t('composio.toolkits')">
        <div class="flex gap-2">
          <UInput v-model="toolkits" class="flex-1 font-mono" placeholder="gmail, slack" />
          <UButton color="neutral" variant="outline" :label="t('composio.loadEvents')" icon="i-lucide-list" :loading="busy === 'load'" :disabled="!!busy" @click="load" />
        </div>
      </UFormField>
      <LoadingRows v-if="busy === 'load' && !triggers" />
      <div v-else-if="triggers" class="max-h-56 space-y-1 overflow-y-auto">
        <p v-if="!triggers.length" class="text-xs text-(--ui-text-muted)">{{ t('composio.noEvents') }}</p>
        <button
          v-for="tr in triggers" :key="tr.slug" type="button"
          class="flex w-full items-start gap-2 rounded-md px-2 py-1 text-start text-sm hover:bg-(--ui-bg-elevated)"
          @click="toggle(tr.slug)"
        >
          <UIcon :name="picked(tr.slug) ? 'i-lucide-square-check' : 'i-lucide-square'" class="mt-0.5 size-4 shrink-0" />
          <span class="min-w-0"><span class="font-mono text-xs">{{ tr.slug }}</span><span class="block truncate text-xs text-(--ui-text-muted)">{{ tr.name }}</span></span>
        </button>
      </div>
      <div v-if="automationId" class="space-y-1.5 border-t border-(--ui-border) pt-3">
        <UFormField :label="t('composio.baseURL')" :hint="t('composio.baseURLHint')">
          <div class="flex gap-2">
            <UInput v-model="baseURL" class="flex-1 font-mono" placeholder="https://office.example.com" />
            <UButton :label="isHere ? t('composio.resubscribe') : t('composio.subscribe')" icon="i-lucide-send" :loading="busy === 'subscribe'" :disabled="!baseURL.startsWith('https://') || !!busy" @click="subscribe" />
          </div>
        </UFormField>
        <p v-if="isHere" class="flex items-center gap-1.5 text-xs text-(--ui-success)"><UIcon name="i-lucide-shield-check" />{{ conn.signed ? t('composio.hereSigned') : t('composio.here') }}</p>
        <p v-else-if="conn.automation_id" class="text-xs text-(--ui-text-muted)">{{ t('composio.elsewhere', { name: conn.automation_name ?? conn.automation_id }) }}</p>
      </div>
      <p v-else class="text-xs text-(--ui-text-muted)">{{ t('composio.saveFirst') }}</p>
    </template>
  </div>
</template>
