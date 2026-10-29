<script setup lang="ts">
// The files sent with messages (ADR-233): copied into the project's
// .office-files/tam and listed in its collection "files"; one the person
// wants kept is moved to .office-files/giu (here, or by an agent's
// file_keep). Temporary ones are cleaned on the data page.
const props = defineProps<{ projectId: string }>()
const { t, when } = useLang()
const toast = useToast()
const copy = useCopy()
const { isAdmin } = useAuth()

const search = ref('')
const q = ref('')
let typing: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => { clearTimeout(typing); typing = setTimeout(() => q.value = v.trim(), 300) })
const { data, error, refresh } = useLiveFetch<{ records: DataRecord[], total: number }>(
  () => `/api/projects/${props.projectId}/collections/files/records?limit=200${q.value ? `&search=${encodeURIComponent(q.value)}` : ''}`, { lazy: true })

const str = (v: unknown) => typeof v === 'string' ? v : ''
const kept = (r: DataRecord) => r.data.status === 'giu'

const keeping = ref('')
async function keep(r: DataRecord) {
  keeping.value = r.id
  try {
    await $fetch(`/api/projects/${props.projectId}/files/${r.id}/keep`, { method: 'POST' })
    refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    keeping.value = ''
  }
}
</script>

<template>
  <UCard class="max-w-4xl" :ui="{ header: 'p-3 sm:px-4', body: 'p-0 sm:p-0' }">
    <template #header>
      <div class="flex flex-wrap items-center gap-2">
        <p class="flex items-center gap-2 font-medium">
          <UIcon name="i-lucide-folder-open" class="size-4 text-(--ui-text-muted)" />{{ t('files.title') }}
          <InfoTip :text="t('files.hint')" />
        </p>
        <UInput v-model="search" icon="i-lucide-search" size="xs" :placeholder="t('files.search')" class="ms-auto w-36 sm:w-48" />
      </div>
    </template>
    <UAlert v-if="error" color="error" variant="subtle" class="m-3" :title="apiError(error)">
      <template #actions><UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="refresh()" /></template>
    </UAlert>
    <LoadingRows v-else-if="!data" :n="3" />
    <p v-else-if="!data.records.length" class="p-4 text-sm text-(--ui-text-muted)">{{ q ? t('files.noMatch') : t('files.empty') }}</p>
    <ul v-else class="divide-y divide-(--ui-border)">
      <li v-for="r in data.records" :key="r.id" class="flex items-start gap-2 px-3 py-2 text-sm sm:px-4">
        <UIcon :name="kept(r) ? 'i-lucide-archive' : 'i-lucide-file-clock'" class="mt-0.5 size-4 shrink-0 text-(--ui-text-muted)" />
        <div class="min-w-0 flex-1">
          <p class="flex flex-wrap items-center gap-2">
            <span class="font-medium">{{ str(r.data.name) }}</span>
            <UBadge size="sm" variant="subtle" :color="kept(r) ? 'success' : 'neutral'" :label="kept(r) ? t('files.kept') : t('files.temp')" />
            <span v-if="typeof r.data.size === 'number'" class="text-xs text-(--ui-text-muted) tabular-nums">{{ fmtBytes(r.data.size) }}</span>
          </p>
          <p class="truncate font-mono text-xs text-(--ui-text-muted)" :title="str(r.data.path)">{{ str(r.data.path) }}</p>
          <p v-if="r.data.note" class="text-xs text-(--ui-text-toned)">{{ str(r.data.note) }}</p>
        </div>
        <span class="hidden shrink-0 text-xs text-(--ui-text-muted) sm:inline" :title="r.updated_by">{{ when(r.updated_at) }}</span>
        <UButton v-if="isAdmin && !kept(r)" size="xs" color="neutral" variant="ghost" icon="i-lucide-archive" :label="t('files.keep')" :loading="keeping === r.id" @click="keep(r)" />
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-copy" :aria-label="t('files.copy')" @click="copy(str(r.data.path))" />
      </li>
    </ul>
  </UCard>
</template>
