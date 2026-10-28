<script setup lang="ts">
// Passkeys of the signed-in account (ADR-226): a way to sign in besides the password.
interface Passkey { id: string, name: string, created_at: string, last_used_at?: string | null }
const { t, when: fmtWhen } = useLang()
const toast = useToast()
const { supported, register } = usePasskey()
const { data, refresh } = await useLiveFetch<{ passkeys: Passkey[] }>('/api/me/passkeys')
const name = ref('')
const when = (d?: string | null) => d ? fmtWhen(d) : t('passkey.never')

const adding = ref(false)
async function add() {
  if (adding.value) return
  adding.value = true
  try {
    await register(name.value)
    name.value = ''
    toast.add({ title: t('passkey.added'), color: 'success' })
    await refresh()
  } catch (e) {
    if (!passkeyCancelled(e)) toast.add({ title: apiError(e), color: 'error' })
  } finally {
    adding.value = false
  }
}
async function remove(p: Passkey) {
  if (!confirm(t('passkey.deleteConfirm', { name: p.name }))) return
  await $fetch(`/api/me/passkeys/${p.id}`, { method: 'DELETE' }).catch(e => toast.add({ title: apiError(e), color: 'error' }))
  await refresh()
}
</script>

<template>
  <UCard class="max-w-2xl" :ui="{ body: 'space-y-3 sm:p-4' }">
    <p class="text-sm font-medium"><InfoLabel :label="t('passkey.title')" :info="t('passkey.hint')" /></p>
    <form v-if="supported" class="flex gap-2" @submit.prevent="add">
      <UInput v-model="name" size="sm" class="flex-1" :placeholder="t('passkey.namePlaceholder')" />
      <UButton type="submit" size="sm" icon="i-lucide-fingerprint" :label="t('passkey.add')" :loading="adding" :disabled="adding" />
    </form>
    <p v-else class="text-xs text-(--ui-text-muted)">{{ t('passkey.unavailable') }}</p>
    <p v-if="!data?.passkeys?.length" class="text-xs text-(--ui-text-muted)">{{ t('passkey.none') }}</p>
    <div v-for="p in data?.passkeys ?? []" :key="p.id" class="flex items-center gap-2 border-t border-(--ui-border) pt-2 text-sm">
      <UIcon name="i-lucide-fingerprint" class="size-4 text-(--ui-text-muted)" />
      <span class="flex-1 truncate">{{ p.name }}</span>
      <span class="text-xs text-(--ui-text-muted)">{{ t('passkey.lastUsed', { when: when(p.last_used_at) }) }}</span>
      <UButton size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('passkey.delete')" @click="remove(p)" />
    </div>
  </UCard>
</template>
