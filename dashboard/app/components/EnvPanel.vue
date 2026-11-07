<script setup lang="ts">
// "Môi trường" section of Vận hành: the project's config files (.env,
// properties, ini, toml, yaml, json, xml) as a list of keys and values. A
// switch turns a key off (comments the line out), another shows the notes
// (comments above a key) to read or write. Saved through the file editor's
// /file, so a file changed meanwhile is never overwritten.
import { envChanged, type EnvVar } from '~/utils/dotenv'
import { parseConfig, writeConfig, newConfigVar, configCaps, type ConfigDoc, type ConfigFormat } from '~/utils/configfile'

interface EnvFile { path: string, format: ConfigFormat, size: number, example: boolean }
interface OpenFile { path: string, sha?: string, content?: string, too_large?: boolean, binary?: boolean }

const props = defineProps<{ projectId: string }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const base = computed(() => `/api/projects/${props.projectId}`)

const files = ref<EnvFile[] | null>(null)
const listError = ref('')
const path = ref('')
async function loadList() {
  try {
    files.value = (await $fetch<{ files: EnvFile[] }>(`${base.value}/ops/envfiles`)).files
    listError.value = ''
    if (!files.value.some(f => f.path === path.value)) {
      const first = files.value.find(f => !f.example) ?? files.value[0]
      if (first) openFile(first.path, true)
    }
  } catch (e) {
    listError.value = apiError(e)
  }
}
onMounted(loadList)
onActivated(() => { if (files.value && !dirty.value) loadList() })

// the open file: what was loaded (sha, text) and the variables being edited
const file = ref<OpenFile | null>(null)
const doc = ref<ConfigDoc | null>(null)
// the format of the open file: as found, or by its name for a new one
const format = computed<ConfigFormat>(() => files.value?.find(f => f.path === path.value)?.format ?? formatOf(path.value))
const caps = computed(() => configCaps[format.value])
const parse = (text: string) => parseConfig(text, format.value)
const raw = ref(false) // edit the text as is
const draft = ref('')
const loadingFile = ref(false)
const content = computed(() => (raw.value ? draft.value : doc.value ? writeConfig(doc.value) : ''))
const dirty = computed(() => !!file.value && file.value.content !== undefined && content.value !== file.value.content)
const isNew = computed(() => !!file.value && !file.value.sha)

const leaveOk = () => !dirty.value || confirm(t('envfile.leave'))
let openReq = ''
async function openFile(p: string, force = false) {
  if (!force && !leaveOk()) return
  openReq = p
  path.value = p
  loadingFile.value = true
  try {
    const f = await $fetch<OpenFile>(`${base.value}/file`, { query: { path: p } })
    if (openReq !== p) return
    file.value = f
    doc.value = f.content !== undefined ? parse(f.content) : null
    draft.value = f.content ?? ''
  } catch (e) {
    if (openReq === p) toast.add({ title: apiError(e), color: 'error' })
  } finally {
    if (openReq === p) loadingFile.value = false
  }
}
const pick = (p: string) => { if (p !== path.value) openFile(p) }

function setRaw(on: boolean) {
  if (on) draft.value = content.value
  else doc.value = parse(draft.value)
  raw.value = on
}

// ---- variables ----
const showNotes = ref(false)
const showValues = ref(false)
const filter = ref('')
const vars = computed(() => (doc.value?.entries ?? []).filter((e): e is EnvVar => e.kind === 'var'))
const shownVars = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return q ? vars.value.filter(v => v.key.toLowerCase().includes(q) || v.note.toLowerCase().includes(q)) : vars.value
})
// comment lines not above a variable: shown with the notes, edited in the text
const freeNotes = computed(() => (doc.value?.entries ?? []).filter(e => e.kind === 'line' && /^\s*([#;!]|<!--)/.test(e.raw)).map(e => (e as { raw: string }).raw))
// only where keys are typed: a path may repeat in yaml lists, xml…
const dupKeys = computed(() => {
  const seen = new Set<string>(), dup = new Set<string>()
  if (!caps.value.rename) return dup
  for (const v of vars.value) {
    if (!v.on) continue
    if (seen.has(v.key)) dup.add(v.key)
    seen.add(v.key)
  }
  return dup
})
// a stable row key: rows come and go, and the list is filtered
const ids = new WeakMap<EnvVar, number>()
let nextId = 0
const rowId = (v: EnvVar) => ids.get(v) ?? (ids.set(v, ++nextId), nextId)
const keyOk = (v: EnvVar) => /^[A-Za-z_][A-Za-z0-9_.-]*$/.test(v.key.trim())
const invalid = computed(() => !raw.value && caps.value.rename && vars.value.some(v => !keyOk(v)))

function addVar() {
  if (!doc.value) return
  doc.value.entries.push(newConfigVar(format.value))
  filter.value = ''
  showValues.value = true
}
function removeVar(v: EnvVar) {
  if (!doc.value) return
  doc.value.entries = doc.value.entries.filter(e => e !== v)
}
function revert() {
  const c = file.value?.content ?? ''
  doc.value = parse(c)
  draft.value = c
}

// ---- save / create ----
const saving = ref(false)
async function save() {
  const f = file.value
  if (!f || saving.value || invalid.value) return
  const savingPath = f.path
  saving.value = true
  const text = content.value
  try {
    // a config file may hold secrets: choosing to save it here is the confirm
    const res = await $fetch<{ sha: string }>(`${base.value}/file`, { method: 'PUT', body: { path: f.path, content: text, sha: f.sha ?? '', confirm: true } })
    const created = !f.sha
    if (path.value === savingPath) {
      file.value = { ...f, sha: res.sha, content: text }
      if (!raw.value) doc.value = parse(text)
      if (created) loadList()
    }
    toast.add({ title: t('envfile.saved', { path: f.path }), color: 'success' })
  } catch (e) {
    if ((e as { status?: number }).status === 409) {
      toast.add({
        title: t('envfile.conflict'),
        description: apiError(e),
        color: 'warning',
        actions: path.value === savingPath ? [{ label: t('envfile.reload'), onClick: () => { openFile(f.path, true) } }] : undefined,
      })
    } else {
      toast.add({ title: apiError(e), color: 'error' })
    }
  } finally {
    saving.value = false
  }
}

const createOpen = ref(false)
const blank = '-' // a select item cannot be ''
const createForm = reactive({ path: '.env', from: blank })
function openCreate() {
  if (!leaveOk()) return
  const example = files.value?.find(f => f.example)
  const dir = example?.path.includes('/') ? example.path.slice(0, example.path.lastIndexOf('/') + 1) : ''
  Object.assign(createForm, { path: `${dir}.env`, from: example?.path ?? blank })
  createCheck.reset()
  createOpen.value = true
}
const createPath = computed(() => createForm.path.trim().replace(/^\/+/, ''))
const createCheck = useFormCheck(() => {
  const p = createPath.value
  return { path: !p ? t('common.required') : p.endsWith('/') ? t('envfile.needFile') : files.value?.some(f => f.path === p) && t('envfile.exists', { path: p }) }
})
async function create() {
  if (!createCheck.check()) return
  const p = createPath.value
  let text = ''
  if (createForm.from !== blank) {
    try {
      text = (await $fetch<OpenFile>(`${base.value}/file`, { query: { path: createForm.from } })).content ?? ''
    } catch (e) {
      toast.add({ title: apiError(e), color: 'error' })
      return
    }
  }
  // not on disk until saved: the person reviews the copy first
  openReq = p
  path.value = p
  file.value = { path: p, sha: '', content: '' }
  doc.value = parse(text)
  draft.value = text
  createOpen.value = false
}

// a new file's format, by its name
function formatOf(p: string): ConfigFormat {
  const ext = p.toLowerCase().replace(/\.(example|sample|template|dist)$/, '').split('.').pop() ?? ''
  const by: Record<string, ConfigFormat> = { properties: 'properties', ini: 'ini', cfg: 'ini', conf: 'ini', toml: 'toml', yaml: 'yaml', yml: 'yaml', json: 'json', xml: 'xml', config: 'xml' }
  return by[ext] ?? 'env'
}
const fileItems = computed(() => {
  const list = (files.value ?? []).map(f => ({ label: f.path, value: f.path, suffix: [f.format, f.example ? t('envfile.example') : ''].filter(Boolean).join(' · ') }))
  if (file.value && isNew.value && !list.some(i => i.value === file.value!.path)) list.push({ label: file.value.path, value: file.value.path, suffix: t('envfile.new') })
  return list
})
const current = computed(() => files.value?.find(f => f.path === path.value))
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <slot name="nav" />
      <USelectMenu
        v-if="fileItems.length" :model-value="path" :items="fileItems" value-key="value" size="sm" class="w-64" :search-input="fileItems.length > 8 ? { placeholder: t('envfile.searchFile') } : false"
        @update:model-value="(v: string) => pick(v)"
      >
        <template #item-trailing="{ item }"><span class="text-xs text-(--ui-text-dimmed)">{{ item.suffix }}</span></template>
      </USelectMenu>
      <UBadge v-if="current?.example" color="neutral" variant="subtle" size="sm" :label="t('envfile.exampleBadge')" />
      <div v-if="isAdmin && files" class="ms-auto flex flex-wrap items-center gap-2">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :aria-label="t('envfile.rescan')" :title="t('envfile.rescan')" @click="loadList" />
        <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-file-plus" :label="t('envfile.create')" @click="openCreate" />
      </div>
    </div>

    <UAlert v-if="listError" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="listError" :actions="[{ label: t('envfile.retry'), onClick: loadList }]" />
    <LoadingRows v-else-if="!files" :icon="false" />

    <div v-else-if="!files.length && !file" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
      <UIcon name="i-lucide-file-key" class="mx-auto size-8 text-(--ui-text-dimmed)" />
      <p class="mt-2 font-medium">{{ t('envfile.empty.title') }}</p>
      <p class="text-sm text-(--ui-text-muted)">{{ t('envfile.empty.desc') }}</p>
      <UButton v-if="isAdmin" class="mt-4" icon="i-lucide-file-plus" :label="t('envfile.create')" @click="openCreate" />
    </div>

    <template v-else-if="file">
      <UAlert v-if="file.too_large || file.binary" color="warning" variant="subtle" icon="i-lucide-file-warning" :title="file.too_large ? t('envfile.tooLarge') : t('envfile.binary')" />
      <div v-else class="rounded-lg border border-(--ui-border)">
        <!-- toolbar: what is shown, how it is edited, save -->
        <div class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-(--ui-border) px-3 py-2">
          <UInput v-if="!raw" v-model="filter" size="sm" icon="i-lucide-search" :placeholder="t('envfile.filter')" class="w-44" />
          <USwitch v-if="!raw && caps.notes" v-model="showNotes" size="sm" :label="t('envfile.notes')" />
          <USwitch v-if="!raw" v-model="showValues" size="sm" :label="t('envfile.showValues')" />
          <USwitch :model-value="raw" size="sm" :label="t('envfile.raw')" @update:model-value="setRaw" />
          <div v-if="isAdmin" class="ms-auto flex items-center gap-2">
            <span v-if="dirty" class="text-xs text-(--ui-warning)">{{ isNew ? t('envfile.notCreated') : t('envfile.unsaved') }}</span>
            <UButton v-if="dirty && !isNew" size="sm" color="neutral" variant="ghost" icon="i-lucide-undo-2" :label="t('envfile.revert')" @click="revert" />
            <UButton size="sm" icon="i-lucide-save" :label="t('envfile.save')" :loading="saving" :disabled="(!dirty && !isNew) || invalid || loadingFile" @click="save" />
          </div>
        </div>

        <div v-if="loadingFile" class="p-3"><LoadingRows :icon="false" /></div>
        <UTextarea v-else-if="raw" v-model="draft" :rows="16" autoresize :maxrows="40" class="w-full" variant="none" :ui="{ base: 'font-mono text-sm p-3' }" :disabled="!isAdmin" />
        <div v-else class="divide-y divide-(--ui-border)">
          <div v-if="showNotes && caps.notes && freeNotes.length" class="bg-(--ui-bg-elevated)/40 px-3 py-2">
            <pre class="whitespace-pre-wrap font-mono text-xs text-(--ui-text-dimmed)">{{ freeNotes.join('\n') }}</pre>
          </div>
          <p v-if="!shownVars.length" class="px-3 py-6 text-center text-sm text-(--ui-text-muted)">{{ filter ? t('envfile.noMatch') : t('envfile.noVars') }}</p>
          <div v-for="v in shownVars" :key="rowId(v)" class="space-y-1.5 px-3 py-2" :class="{ 'bg-(--ui-primary)/5': envChanged(v) }">
            <div class="flex items-center gap-2">
              <USwitch v-if="caps.toggle" v-model="v.on" size="sm" :disabled="!isAdmin" :aria-label="v.on ? t('envfile.turnOff') : t('envfile.turnOn')" :title="v.on ? t('envfile.turnOff') : t('envfile.turnOn')" />
              <code v-if="!caps.rename" class="w-2/5 shrink-0 truncate text-sm sm:w-64" :class="{ 'opacity-50': !v.on }" :title="v.key">{{ v.key }}</code>
              <UInput
                v-else v-model="v.key" size="sm" class="w-2/5 shrink-0 font-mono sm:w-64" :class="{ 'opacity-50': !v.on }" placeholder="KEY" :disabled="!isAdmin"
                :color="!keyOk(v) || dupKeys.has(v.key) ? 'error' : undefined" :highlight="!keyOk(v) || dupKeys.has(v.key)" :aria-label="t('envfile.key')"
              />
              <UInput
                v-model="v.value" size="sm" class="min-w-0 flex-1 font-mono" :class="{ 'opacity-50': !v.on }" :type="showValues ? 'text' : 'password'"
                autocomplete="off" :disabled="!isAdmin" :aria-label="t('envfile.value')"
              />
              <UButton v-if="isAdmin && caps.remove" size="sm" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('envfile.remove')" :title="t('envfile.remove')" @click="removeVar(v)" />
            </div>
            <p v-if="dupKeys.has(v.key) && v.on" :class="caps.toggle ? 'ps-11' : ''" class=" text-xs text-(--ui-error)">{{ t('envfile.duplicate') }}</p>
            <UTextarea
              v-if="showNotes && caps.notes" v-model="v.note" :rows="1" autoresize size="sm" class="w-full" :class="caps.toggle ? 'ps-11' : ''" :disabled="!isAdmin"
              :placeholder="t('envfile.notePlaceholder')" :ui="{ base: 'font-mono text-xs text-(--ui-text-muted)' }"
            />
          </div>
          <div v-if="isAdmin && caps.add" class="px-3 py-2">
            <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-plus" :label="t('envfile.add')" @click="addVar" />
          </div>
        </div>
      </div>
    </template>

    <UModal v-model:open="createOpen" :title="t('envfile.createTitle')" :ui="{ content: 'max-w-md' }">
      <template #body>
        <form id="envfile-create" class="space-y-4" @submit.prevent="create">
          <UFormField :label="t('envfile.path')" required data-field="path" :error="createCheck.errors.value.path">
            <UInput v-model="createForm.path" class="w-full font-mono" placeholder=".env" />
          </UFormField>
          <UFormField v-if="files?.length" :label="t('envfile.copyFrom')">
            <USelect v-model="createForm.from" :items="[{ label: t('envfile.blank'), value: blank }, ...files.map(f => ({ label: f.path, value: f.path }))]" class="w-full" />
          </UFormField>
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('envfile.cancel')" @click="createOpen = false" />
          <UButton type="submit" form="envfile-create" :label="t('envfile.createOk')" />
        </div>
      </template>
    </UModal>
  </div>
</template>
