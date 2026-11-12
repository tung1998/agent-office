<script setup lang="ts">
// Where the account is signed in (ADR-272): every device, the last used
// first, and a way to sign a lost one out at once.
interface Session { id: string, created_at: string, last_seen_at: string, user_agent: string, ip: string, current: boolean, impersonated: boolean }
const { t, when } = useLang()
const toast = useToast()
const { data, error, refresh } = await useLiveFetch<{ sessions: Session[] }>('/api/me/sessions')

// "Chrome · macOS" from a user agent: enough to tell the devices apart
function device(ua: string) {
  if (!ua) return t('sessions.unknownDevice')
  const os = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl|Go-http/.test(ua) ? 'CLI' : ''
  return [browser, os].filter(Boolean).join(' · ') || ua.slice(0, 40)
}

const busy = ref('')
async function revoke(s: Session) {
  if (busy.value || !confirm(t('sessions.revokeConfirm', { device: device(s.user_agent) }))) return
  busy.value = s.id
  try {
    await $fetch(`/api/me/sessions/${s.id}`, { method: 'DELETE' })
    toast.add({ title: t('sessions.revoked'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
    await refresh()
  }
}
</script>

<template>
  <UCard class="max-w-2xl" :ui="{ body: 'space-y-3 sm:p-4' }">
    <p class="text-sm font-medium"><InfoLabel :label="t('sessions.title')" :info="t('sessions.hint')" /></p>
    <UAlert v-if="error" color="error" variant="subtle" :description="apiError(error)">
      <template #actions>
        <UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="refresh()" />
      </template>
    </UAlert>
    <LoadingRows v-else-if="!data" :n="2" />
    <div v-for="s in data?.sessions ?? []" :key="s.id" class="flex items-center gap-2 border-t border-(--ui-border) pt-2 text-sm first:border-t-0 first:pt-0">
      <UIcon :name="/iPhone|Android/.test(s.user_agent) ? 'i-lucide-smartphone' : 'i-lucide-monitor'" class="size-4 shrink-0 text-(--ui-text-muted)" />
      <div class="min-w-0 flex-1">
        <p class="truncate">
          {{ device(s.user_agent) }}
          <UBadge v-if="s.current" size="sm" variant="subtle" color="success" :label="t('sessions.current')" class="ml-1" />
          <UBadge v-if="s.impersonated" size="sm" variant="subtle" color="warning" :label="t('sessions.impersonated')" class="ml-1" />
        </p>
        <p class="truncate text-xs text-(--ui-text-muted)">{{ s.ip || '—' }} · {{ t('sessions.lastSeen', { when: when(s.last_seen_at) }) }}</p>
      </div>
      <UButton v-if="!s.current" size="xs" color="error" variant="ghost" icon="i-lucide-log-out" :aria-label="t('sessions.revoke')" :loading="busy === s.id" :disabled="!!busy" @click="revoke(s)" />
    </div>
  </UCard>
</template>
