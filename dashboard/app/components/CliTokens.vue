<script setup lang="ts">
// Personal tokens for your own Claude Code CLI (ADR-047): the office tools
// over MCP, across projects; what it proposes waits for approval here.
interface Token { id: string, name: string, created_at: string, last_used_at?: string | null, expires_at?: string | null, expired?: boolean }
const { t, dateLocale } = useLang()
const toast = useToast()
const copy = useCopy()
const { data, refresh } = await useLiveFetch<{ tokens: Token[] }>('/api/me/tokens')
const name = ref('')
const days = ref(90)
const dayItems = computed(() => [30, 90, 365, 0].map(n => ({ value: n, label: n ? t('cli.days', { n }) : t('cli.noExpiry') })))
const created = ref<{ token: string, name: string } | null>(null)
const mcpURL = computed(() => typeof location === 'undefined' ? '' : `${location.protocol}//${location.hostname}:8787/mcp`)
const command = computed(() => created.value ? `claude mcp add --transport http agent-office ${mcpURL.value} --header "Authorization: Bearer ${created.value.token}"` : '')
const when = (d?: string | null) => d ? new Date(d).toLocaleString(dateLocale.value) : t('cli.never')

async function create() {
  try {
    created.value = await $fetch<{ token: string, name: string }>('/api/me/tokens', { method: 'POST', body: { name: name.value, days: days.value } })
    name.value = ''
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function revoke(tk: Token) {
  if (!confirm(t('cli.revokeConfirm', { name: tk.name }))) return
  await $fetch(`/api/me/tokens/${tk.id}`, { method: 'DELETE' }).catch(e => toast.add({ title: apiError(e), color: 'error' }))
  await refresh()
}
</script>

<template>
  <UCard class="max-w-2xl" :ui="{ body: 'space-y-3 sm:p-4' }">
    <div>
      <p class="text-sm font-medium">{{ t('cli.title') }}</p>
      <p class="text-xs text-(--ui-text-muted)">{{ t('cli.hint') }}</p>
    </div>
    <form class="flex gap-2" @submit.prevent="create">
      <UInput v-model="name" size="sm" class="flex-1" :placeholder="t('cli.namePlaceholder')" />
      <USelect v-model="days" :items="dayItems" size="sm" class="w-32" />
      <UButton type="submit" size="sm" icon="i-lucide-plus" :label="t('cli.create')" />
    </form>
    <div v-if="created" class="space-y-2 rounded-md border border-(--ui-warning)/40 bg-(--ui-warning)/5 p-3 text-xs">
      <p class="font-medium">{{ t('cli.onlyOnce') }}</p>
      <pre class="overflow-x-auto whitespace-pre-wrap break-all rounded bg-(--ui-bg-elevated) p-2 font-mono">{{ command }}</pre>
      <UButton size="xs" icon="i-lucide-copy" :label="t('cli.copyCommand')" @click="copy(command)" />
    </div>
    <div v-for="tk in data?.tokens ?? []" :key="tk.id" class="flex items-center gap-2 border-t border-(--ui-border) pt-2 text-sm">
      <UIcon name="i-lucide-key-round" class="size-4 text-(--ui-text-muted)" />
      <span class="flex-1 truncate">{{ tk.name }}</span>
      <div class="flex flex-col items-end text-xs text-(--ui-text-muted)">
        <UBadge v-if="tk.expired" color="error" variant="subtle" size="sm" :label="t('cli.expired')" />
        <span v-else>{{ tk.expires_at ? t('cli.expires', { when: when(tk.expires_at) }) : t('cli.noExpiry') }}</span>
        <span>{{ t('cli.lastUsed', { when: when(tk.last_used_at) }) }}</span>
      </div>
      <UButton size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('cli.revoke')" @click="revoke(tk)" />
    </div>
  </UCard>
</template>
