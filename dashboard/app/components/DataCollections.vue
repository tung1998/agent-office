<script setup lang="ts">
// A project's data collections (ADR-172): pick one, find its records, add or
// change one through a form made from its fields. Agents read the same data
// with the data_* tools.
const props = defineProps<{ projectId: string }>()
const { t, when } = useLang()
const toast = useToast()
const { isAdmin } = useAuth()
const base = computed(() => `/api/projects/${props.projectId}/collections`)
const { data: colls, error } = useLiveFetch<{ collections: Collection[] }>(() => base.value, { lazy: true })
const key = ref('')
watch(colls, (v) => { if (!key.value && v?.collections.length) key.value = v.collections[0]!.key }, { immediate: true })
const coll = computed(() => colls.value?.collections.find(c => c.key === key.value))
const columns = computed(() => (coll.value?.fields ?? []).slice(0, 5))
const label = (f: CollectionField) => f.label || f.name

const search = ref('')
const q = ref('')
let typing: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => { clearTimeout(typing); typing = setTimeout(() => q.value = v.trim(), 300) })
const { data: recs } = useLiveFetch<{ records: DataRecord[], total: number }>(
  () => `${base.value}/${key.value || '_'}/records?limit=100${q.value ? `&search=${encodeURIComponent(q.value)}` : ''}`, { lazy: true })
const records = computed(() => key.value ? recs.value?.records ?? [] : [])

// the form: each field as text, its value put back in its type on save
const open = ref(false)
const editing = ref<DataRecord | null>(null)
const form = reactive<Record<string, unknown>>({})
const history = ref<RecordChange[]>([])
function openForm(r?: DataRecord) {
  editing.value = r ?? null
  history.value = []
  for (const k of Object.keys(form)) delete form[k]
  for (const f of coll.value?.fields ?? []) {
    const v = r?.data[f.name]
    form[f.name] = f.type === 'tags' ? (Array.isArray(v) ? v.join(', ') : '') : f.type === 'bool' ? !!v : v ?? ''
  }
  open.value = true
  if (r) {
    $fetch<{ history: RecordChange[] }>(`${base.value}/${key.value}/records/${r.id}/history`).then(h => history.value = h.history).catch(() => {})
  }
}
const saving = ref(false)
async function save() {
  if (saving.value || !coll.value) return
  const data: Record<string, unknown> = {}
  for (const f of coll.value.fields) {
    const v = form[f.name]
    data[f.name] = v === '' ? null : f.type === 'number' ? Number(v) : v
  }
  saving.value = true
  try {
    await $fetch(`${base.value}/${key.value}/records`, { method: 'POST', body: { id: editing.value?.id, data } })
    open.value = false
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
async function remove() {
  if (!editing.value || !confirm(t('coll.deleteConfirm'))) return
  try {
    await $fetch(`${base.value}/${key.value}/records/${editing.value.id}`, { method: 'DELETE' })
    open.value = false
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
// playbooks from files and links: their text read again (also hourly)
const isPlaybooks = computed(() => key.value === 'playbooks' && coll.value?.pack === 'support')
const syncing = ref(false)
async function sync() {
  syncing.value = true
  try {
    const r = await $fetch<{ updated: number }>(`/api/projects/${props.projectId}/support/playbooks/sync`, { method: 'POST' })
    toast.add({ title: t('coll.synced', { n: r.updated }), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    syncing.value = false
  }
}
const long = (f: CollectionField) => f.type === 'text' && ['content', 'notes', 'body', 'description'].includes(f.name)
</script>

<template>
  <div class="max-w-6xl space-y-3">
    <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)" />
    <p v-else-if="colls && !colls.collections.length" class="rounded-lg border border-dashed border-(--ui-border) p-4 text-sm text-(--ui-text-muted)">{{ t('coll.none') }}</p>
    <template v-else>
      <div class="flex flex-wrap items-center gap-2">
        <USelect v-model="key" :items="(colls?.collections ?? []).map(c => ({ label: c.name, value: c.key }))" size="sm" class="w-48" />
        <UInput v-model="search" icon="i-lucide-search" size="sm" :placeholder="t('coll.search')" class="w-56" />
        <span class="text-xs text-(--ui-text-muted)">{{ t('coll.count', { n: recs?.total ?? 0 }) }}</span>
        <UButton v-if="isAdmin && isPlaybooks" size="sm" color="neutral" variant="outline" icon="i-lucide-refresh-cw" :label="t('coll.sync')" :loading="syncing" class="ms-auto" @click="sync" />
        <UButton v-if="isAdmin && coll" size="sm" icon="i-lucide-plus" :label="t('coll.add')" :class="isPlaybooks ? '' : 'ms-auto'" @click="openForm()" />
      </div>
      <p v-if="coll?.description" class="text-xs text-(--ui-text-muted)">{{ coll.description }}</p>
      <div class="overflow-x-auto rounded-lg border border-(--ui-border)">
        <table class="w-full text-sm">
          <thead class="bg-(--ui-bg-elevated) text-left text-xs text-(--ui-text-muted)">
            <tr>
              <th v-for="f in columns" :key="f.name" class="px-3 py-2 font-medium">{{ label(f) }}</th>
              <th class="px-3 py-2 text-right font-medium">{{ t('coll.updated') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-(--ui-border)">
            <tr v-for="r in records" :key="r.id" class="cursor-pointer hover:bg-(--ui-bg-elevated)" @click="openForm(r)">
              <td v-for="f in columns" :key="f.name" class="max-w-64 truncate px-3 py-2">{{ fieldText(r.data[f.name]) }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-right text-xs text-(--ui-text-muted)">{{ when(r.updated_at) }}</td>
            </tr>
            <tr v-if="recs && !records.length">
              <td :colspan="columns.length + 1" class="px-3 py-4 text-center text-(--ui-text-muted)">{{ t('coll.empty') }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <UModal v-model:open="open" :title="editing ? t('coll.edit') : t('coll.add')" :description="coll?.name">
      <template #body>
        <form id="coll-record" class="space-y-3" @submit.prevent="save">
          <UFormField v-for="f in coll?.fields ?? []" :key="f.name" :label="label(f)" :required="f.required" :hint="f.type === 'tags' ? t('coll.tagsHint') : f.unique ? t('coll.unique') : undefined">
            <USwitch v-if="f.type === 'bool'" :model-value="!!form[f.name]" :disabled="!isAdmin" @update:model-value="v => form[f.name] = v" />
            <USelect v-else-if="f.type === 'enum'" :model-value="String(form[f.name] ?? '')" :items="f.options ?? []" class="w-full" :disabled="!isAdmin" @update:model-value="v => form[f.name] = v" />
            <UTextarea v-else-if="long(f)" :model-value="String(form[f.name] ?? '')" autoresize :rows="3" class="w-full" :disabled="!isAdmin" @update:model-value="v => form[f.name] = v" />
            <UInput v-else :model-value="String(form[f.name] ?? '')" :type="f.type === 'number' ? 'number' : f.type === 'date' ? 'date' : 'text'" class="w-full" :disabled="!isAdmin" @update:model-value="v => form[f.name] = v" />
          </UFormField>
          <details v-if="history.length" class="text-xs text-(--ui-text-muted)">
            <summary class="cursor-pointer">{{ t('coll.history', { n: history.length }) }}</summary>
            <ul class="mt-1 space-y-0.5">
              <li v-for="h in history" :key="h.id">{{ when(h.at) }} · {{ t(`coll.op.${h.op}` as 'coll.op.create') }} · {{ h.by }}</li>
            </ul>
          </details>
        </form>
      </template>
      <template v-if="isAdmin" #footer>
        <div class="flex w-full items-center gap-2">
          <UButton v-if="editing" color="error" variant="ghost" icon="i-lucide-trash-2" :label="t('coll.delete')" class="me-auto" @click="remove" />
          <div class="ms-auto flex gap-2">
            <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="open = false" />
            <UButton type="submit" form="coll-record" :label="t('common.save')" :loading="saving" />
          </div>
        </div>
      </template>
    </UModal>
  </div>
</template>
