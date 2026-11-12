<script setup lang="ts">
// A project that came from another office before its repo could be cloned
// here (ADR-279): why, what was kept, and the four ways out.
interface Pending {
  pending: boolean
  remote?: string
  branch?: string
  reason?: 'auth' | 'network' | 'missing' | 'other'
  message?: string
  kept?: { commits: boolean, changes: boolean, untracked: number }
}
const props = defineProps<{ projectId: string }>()
const emit = defineEmits<{ done: [] }>()
const { t } = useLang()
const toast = useToast()
const { isOfficeAdmin: isAdmin } = useAuth() // the routes are the office admin's

const { data, refresh } = useLazyFetch<Pending>(() => `/api/projects/${props.projectId}/pending-code`)
const busy = ref('')
const mode = ref<'' | 'url' | 'dir'>('')
const url = ref('')
const dir = ref('')

const reasonText = computed(() => {
  switch (data.value?.reason) {
    case 'auth': return t('pendingCode.reasonAuth')
    case 'network': return t('pendingCode.reasonNetwork')
    case 'missing': return t('pendingCode.reasonMissing')
    default: return t('pendingCode.reasonOther')
  }
})
const keptText = computed(() => {
  const k = data.value?.kept
  if (!k) return ''
  const parts = [
    k.commits ? t('pendingCode.keptCommits') : '',
    k.changes ? t('pendingCode.keptChanges') : '',
    k.untracked ? t('pendingCode.keptUntracked', { n: k.untracked }) : ''
  ].filter(Boolean)
  return parts.length ? t('pendingCode.kept', { what: parts.join(', ') }) : ''
})

async function run(kind: string, req: () => Promise<{ ok?: boolean, note?: string }>) {
  busy.value = kind
  try {
    const r = await req()
    if (r.ok === false) {
      toast.add({ title: t('pendingCode.stillFailing'), description: r.note, color: 'warning' })
    } else {
      toast.add({ title: t('pendingCode.gotCode'), description: r.note, color: 'success' })
      mode.value = ''
      emit('done')
    }
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
const retry = (withUrl = false) => run(withUrl ? 'url' : 'retry', () => $fetch(`/api/projects/${props.projectId}/pending-code/retry`, { method: 'POST', body: { url: withUrl ? url.value : '' } }))
const attach = () => run('dir', () => $fetch(`/api/projects/${props.projectId}/pending-code/attach`, { method: 'POST', body: { dir: dir.value } }))
async function drop() {
  if (!confirm(t('pendingCode.dropConfirm'))) return
  busy.value = 'drop'
  try {
    await $fetch(`/api/projects/${props.projectId}/pending-code`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <UAlert v-if="data?.pending" color="warning" variant="subtle" icon="i-lucide-git-branch" :title="t('pendingCode.title')">
    <template #description>
      <div class="space-y-3">
        <p>
          <code class="break-all">{{ data.remote }}</code><span v-if="data.branch"> · {{ data.branch }}</span>
        </p>
        <p>{{ reasonText }}</p>
        <p v-if="data.message" class="text-xs opacity-80"><code class="break-all">{{ data.message }}</code></p>
        <p v-if="keptText" class="text-xs">{{ keptText }}</p>

        <div v-if="isAdmin" class="flex flex-wrap gap-2">
          <UButton size="sm" icon="i-lucide-refresh-cw" :label="t('pendingCode.retry')" :loading="busy === 'retry'" :disabled="!!busy" @click="retry()" />
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-link" :label="t('pendingCode.otherUrl')" :disabled="!!busy" @click="mode = mode === 'url' ? '' : 'url'" />
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-folder-open" :label="t('pendingCode.useFolder')" :disabled="!!busy" @click="mode = mode === 'dir' ? '' : 'dir'" />
          <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-x" :label="t('pendingCode.drop')" :loading="busy === 'drop'" :disabled="!!busy" @click="drop" />
        </div>
        <p v-else class="text-xs">{{ t('pendingCode.askAdmin') }}</p>

        <form v-if="mode === 'url'" class="flex flex-wrap items-end gap-2" @submit.prevent="retry(true)">
          <UFormField :label="t('pendingCode.urlLabel')" :hint="t('pendingCode.urlHint')" class="min-w-0 flex-1">
            <UInput v-model="url" class="w-full" placeholder="https://github.com/org/repo.git" />
          </UFormField>
          <UButton type="submit" size="sm" :label="t('pendingCode.clone')" :loading="busy === 'url'" :disabled="!url.trim() || !!busy" />
        </form>
        <form v-if="mode === 'dir'" class="flex flex-wrap items-end gap-2" @submit.prevent="attach">
          <UFormField :label="t('pendingCode.dirLabel')" :hint="t('pendingCode.dirHint')" class="min-w-0 flex-1">
            <UInput v-model="dir" class="w-full" placeholder="/Users/ban/code/repo" />
          </UFormField>
          <UButton type="submit" size="sm" :label="t('pendingCode.attach')" :loading="busy === 'dir'" :disabled="!dir.trim() || !!busy" />
        </form>
      </div>
    </template>
  </UAlert>
</template>
