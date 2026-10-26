<script setup lang="ts">
// The links the person wants to find again (ADR-218): docs, downloads a build
// made, sites. Kept in the project's collection "links"; agents save them too.
const props = defineProps<{ projectId: string }>()
const { t, when } = useLang()
const toast = useToast()
const copy = useCopy()
const { isAdmin } = useAuth()
const base = computed(() => `/api/projects/${props.projectId}/collections/links/records`)

const search = ref('')
const q = ref('')
let typing: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => { clearTimeout(typing); typing = setTimeout(() => q.value = v.trim(), 300) })
const { data, error, refresh } = useLiveFetch<{ records: DataRecord[], total: number }>(
  () => `${base.value}?limit=200${q.value ? `&search=${encodeURIComponent(q.value)}` : ''}`, { lazy: true })

const kinds = ['tai-lieu', 'tai-ve', 'web', 'khac'] as const
type Kind = typeof kinds[number]
const icons: Record<Kind, string> = { 'tai-lieu': 'i-lucide-file-text', 'tai-ve': 'i-lucide-download', 'web': 'i-lucide-globe', 'khac': 'i-lucide-link' }
const kindOf = (r: DataRecord): Kind => kinds.includes(r.data.kind as Kind) ? r.data.kind as Kind : 'khac'
const kindItems = computed(() => kinds.map(k => ({ label: t(`links.kind.${k}`), value: k })))
const str = (v: unknown) => typeof v === 'string' ? v : ''
const isWeb = (u: string) => /^https?:\/\//i.test(u)

const open = ref(false)
const editing = ref<DataRecord | null>(null)
const form = reactive({ title: '', url: '', kind: 'web' as Kind, note: '' })
function openForm(r?: DataRecord) {
  editing.value = r ?? null
  Object.assign(form, { title: str(r?.data.title), url: str(r?.data.url), kind: r ? kindOf(r) : 'web', note: str(r?.data.note) })
  open.value = true
}
const saving = ref(false)
async function save() {
  if (saving.value || !form.url.trim()) return
  saving.value = true
  try {
    await $fetch(base.value, { method: 'POST', body: { id: editing.value?.id, data: {
      title: form.title.trim() || form.url.trim(), url: form.url.trim(), kind: form.kind, note: form.note.trim() || null } } })
    open.value = false
    refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
async function remove() {
  if (!editing.value || !confirm(t('links.deleteConfirm'))) return
  try {
    await $fetch(`${base.value}/${editing.value.id}`, { method: 'DELETE' })
    open.value = false
    refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
</script>

<template>
  <UCard class="max-w-4xl" :ui="{ header: 'p-3 sm:px-4', body: 'p-0 sm:p-0' }">
    <template #header>
      <div class="flex flex-wrap items-center gap-2">
        <p class="flex items-center gap-2 font-medium">
          <UIcon name="i-lucide-link" class="size-4 text-(--ui-text-muted)" />{{ t('links.title') }}
          <InfoTip :text="t('links.hint')" />
        </p>
        <div class="ms-auto flex items-center gap-2">
          <UInput v-model="search" icon="i-lucide-search" size="xs" :placeholder="t('links.search')" class="w-36 sm:w-48" />
          <UButton v-if="isAdmin" size="xs" icon="i-lucide-plus" :label="t('links.add')" :disabled="!data" @click="openForm()" />
        </div>
      </div>
    </template>
    <UAlert v-if="error" color="error" variant="subtle" class="m-3" :title="apiError(error)">
      <template #actions><UButton size="xs" color="neutral" variant="outline" :label="t('common.refresh')" @click="refresh()" /></template>
    </UAlert>
    <LoadingRows v-else-if="!data" :n="3" />
    <p v-else-if="!data.records.length" class="p-4 text-sm text-(--ui-text-muted)">{{ q ? t('links.noMatch') : t('links.empty') }}</p>
    <ul v-else class="divide-y divide-(--ui-border)">
      <li v-for="r in data.records" :key="r.id" class="group flex items-start gap-2 px-3 py-2 text-sm sm:px-4">
        <UIcon :name="icons[kindOf(r)]" class="mt-0.5 size-4 shrink-0 text-(--ui-text-muted)" />
        <div class="min-w-0 flex-1">
          <a v-if="isWeb(str(r.data.url))" :href="str(r.data.url)" target="_blank" rel="noopener" class="font-medium hover:underline">{{ str(r.data.title) }}</a>
          <span v-else class="font-medium">{{ str(r.data.title) }}</span>
          <p class="truncate font-mono text-xs text-(--ui-text-muted)" :title="str(r.data.url)">{{ str(r.data.url) }}</p>
          <p v-if="r.data.note" class="text-xs text-(--ui-text-toned)">{{ str(r.data.note) }}</p>
        </div>
        <span class="hidden shrink-0 text-xs text-(--ui-text-muted) sm:inline" :title="r.updated_by">{{ when(r.updated_at) }}</span>
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-copy" :aria-label="t('links.copy')" @click="copy(str(r.data.url))" />
        <UButton v-if="isAdmin" size="xs" color="neutral" variant="ghost" icon="i-lucide-pencil" :aria-label="t('links.edit')" @click="openForm(r)" />
      </li>
    </ul>

    <UModal v-model:open="open" :title="editing ? t('links.edit') : t('links.add')">
      <template #body>
        <form id="project-link" class="space-y-3" @submit.prevent="save">
          <UFormField :label="t('links.url')" required><UInput v-model="form.url" placeholder="https://…" class="w-full font-mono" /></UFormField>
          <UFormField :label="t('links.name')"><UInput v-model="form.title" class="w-full" /></UFormField>
          <UFormField :label="t('links.kindLabel')"><USelect v-model="form.kind" :items="kindItems" class="w-full" /></UFormField>
          <UFormField :label="t('links.note')"><UTextarea v-model="form.note" autoresize :rows="2" class="w-full" /></UFormField>
        </form>
      </template>
      <template #footer>
        <UButton v-if="editing" color="error" variant="ghost" icon="i-lucide-trash-2" :label="t('links.delete')" @click="remove" />
        <UButton type="submit" form="project-link" :label="t('common.save')" :loading="saving" :disabled="!form.url.trim()" class="ms-auto" />
      </template>
    </UModal>
  </UCard>
</template>
