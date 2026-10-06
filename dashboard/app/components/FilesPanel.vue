<script setup lang="ts">
// The project's files: a tree loaded folder by folder, and a plain editor that
// saves right into the project folder. Ignored and secret files stay hidden
// until "show hidden"; a save carries the sha it was based on (409 = changed meanwhile).
interface Flags { ignored: boolean, secret: boolean, protected: boolean }
interface Entry extends Flags { name: string, path: string, dir: boolean, size: number }
interface Listing { entries: Entry[], hidden: number, more: number }
interface OpenFile { path: string, size: number, flags: Flags, sha?: string, content?: string, binary?: boolean, too_large?: boolean, isNew?: boolean }

const props = defineProps<{ projectId: string }>()
const toast = useToast()
const { t } = useLang()
const base = computed(() => `/api/projects/${props.projectId}`)

const showHidden = useCookie<boolean>('files-hidden', { default: () => false })
const dirs = ref<Record<string, Listing>>({})
const expanded = ref<Set<string>>(new Set(['']))
const loadingDir = ref<Set<string>>(new Set())
const rootError = ref('') // the root folder failed: said in the tree, not an endless skeleton

async function loadDir(path: string) {
  loadingDir.value.add(path)
  if (path === '') rootError.value = ''
  try {
    dirs.value[path] = await $fetch<Listing>(`${base.value}/files`, { query: { path, hidden: showHidden.value ? '1' : undefined } })
  } catch (e) {
    if (path === '') rootError.value = apiError(e)
    else toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loadingDir.value.delete(path)
  }
}
function reloadTree() {
  const open = [...expanded.value]
  dirs.value = {}
  open.forEach(p => loadDir(p))
}
watch(showHidden, reloadTree)

function toggleDir(e: Entry) {
  if (expanded.value.has(e.path)) {
    expanded.value.delete(e.path)
  } else {
    expanded.value.add(e.path)
    if (!dirs.value[e.path]) loadDir(e.path)
  }
}

// the tree flattened into rows, folders first as the server sorts them
type Row = { kind: 'entry', entry: Entry, depth: number } | { kind: 'note', text: string, depth: number }
const rows = computed(() => {
  const out: Row[] = []
  const walk = (path: string, depth: number) => {
    const l = dirs.value[path]
    if (!l) return
    for (const e of l.entries) {
      out.push({ kind: 'entry', entry: e, depth })
      if (e.dir && expanded.value.has(e.path)) walk(e.path, depth + 1)
    }
    if (!l.entries.length && depth > 0) out.push({ kind: 'note', text: t('files.empty'), depth })
    if (l.more) out.push({ kind: 'note', text: t('files.more', { n: l.more }), depth })
    if (l.hidden && !showHidden.value) out.push({ kind: 'note', text: t('files.hiddenCount', { n: l.hidden }), depth })
  }
  walk('', 0)
  return out
})

// ---- the open file ----
const file = ref<OpenFile | null>(null)
const draft = ref('')
const saving = ref(false)
const dirty = computed(() => !!file.value && file.value.content !== undefined && draft.value !== file.value.content)
// after `file` exists: an immediate watcher runs during setup
watch(() => props.projectId, () => { expanded.value = new Set(['']); file.value = null; reloadTree() }, { immediate: true })
const showDiff = ref(false)
const diff = ref<string | null>(null)

function leaveOk() {
  return !dirty.value || confirm(t('files.leave'))
}
async function openFile(path: string, force = false) {
  if (!force && !leaveOk()) return
  try {
    const f = await $fetch<OpenFile>(`${base.value}/file`, { query: { path } })
    file.value = f
    draft.value = f.content ?? ''
    diff.value = null
    if (showDiff.value) loadDiff()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
function newFile() {
  if (!leaveOk()) return
  const dir = file.value?.path.includes('/') ? file.value.path.slice(0, file.value.path.lastIndexOf('/') + 1) : ''
  const path = prompt(t('files.newPrompt'), dir)?.trim()
  if (!path || path.endsWith('/')) return
  file.value = { path, size: 0, flags: { ignored: false, secret: false, protected: false }, sha: '', content: '', isNew: true }
  draft.value = ''
  diff.value = null
}

async function save(confirmed = false) {
  const f = file.value
  if (!f || saving.value) return
  if (!confirmed && (f.flags.secret || f.flags.protected)) {
    if (!confirm(t('files.confirmSensitive', { path: f.path }))) return
    confirmed = true
  }
  saving.value = true
  try {
    const res = await $fetch<{ sha: string }>(`${base.value}/file`, { method: 'PUT', body: { path: f.path, content: draft.value, sha: f.sha ?? '', confirm: confirmed } })
    file.value = { ...f, sha: res.sha, content: draft.value, isNew: false }
    toast.add({ title: t('files.saved', { path: f.path }), color: 'success' })
    if (f.isNew) refreshParent(f.path)
    if (showDiff.value) loadDiff()
  } catch (e) {
    const status = (e as { status?: number }).status
    if (status === 428 && confirm(t('files.confirmSensitive', { path: f.path }))) {
      saving.value = false
      return save(true)
    }
    if (status === 409) {
      toast.add({ title: t('files.conflict'), description: apiError(e), color: 'warning', actions: [{ label: t('files.reload'), onClick: () => { openFile(f.path, true) } }] })
    } else {
      toast.add({ title: apiError(e), color: 'error' })
    }
  } finally {
    saving.value = false
  }
}
function refreshParent(path: string) {
  const i = path.lastIndexOf('/')
  const parent = i < 0 ? '' : path.slice(0, i)
  if (dirs.value[parent] || parent === '') loadDir(parent)
}
function revert() {
  if (file.value?.content !== undefined) draft.value = file.value.content
}

async function loadDiff() {
  if (!file.value || file.value.isNew) {
    diff.value = ''
    return
  }
  try {
    diff.value = (await $fetch<{ diff: string }>(`${base.value}/file/diff`, { query: { path: file.value.path } })).diff
  } catch (e) {
    diff.value = ''
    toast.add({ title: apiError(e), color: 'error' })
  }
}
watch(showDiff, (on) => { if (on && diff.value === null) loadDiff() })

function onKey(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && e.key === 's') {
    e.preventDefault()
    if (dirty.value || file.value?.isNew) save()
  }
}
// Tab inserts two spaces instead of leaving the editor
function onTab(e: KeyboardEvent) {
  const el = e.target as HTMLTextAreaElement
  e.preventDefault()
  const { selectionStart: s, selectionEnd: end } = el
  draft.value = draft.value.slice(0, s) + '  ' + draft.value.slice(end)
  nextTick(() => { el.selectionStart = el.selectionEnd = s + 2 })
}
onBeforeRouteLeave(() => leaveOk())

const lineCount = computed(() => draft.value.split('\n').length)
function fmtSize(n: number) {
  return n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`
}
// ---- what changed since the last commit (the git bar's "n thay đổi" opens it) ----
const route = useRoute()
const view = computed<'files' | 'changes'>({
  get: () => route.query.view === 'changes' ? 'changes' : 'files',
  set: v => navigateTo({ query: { ...route.query, view: v === 'changes' ? 'changes' : undefined } }, { replace: true })
})
interface Change { path: string, status: string }
const changes = ref<Change[] | null>(null)
const picked = ref('')
const changeDiff = ref<string | null>(null)
async function loadChanges() {
  try {
    const res = await $fetch<{ repo: boolean, status?: { changes: Change[] } }>(`${base.value}/git`)
    changes.value = res.status?.changes ?? []
    if (picked.value && !changes.value.some(c => c.path === picked.value)) { picked.value = ''; changeDiff.value = null }
  } catch (e) {
    changes.value = []
    toast.add({ title: apiError(e), color: 'error' })
  }
}
// one file's diff at a time: light however much changed
async function pickChange(c: Change) {
  picked.value = c.path
  changeDiff.value = null
  try {
    changeDiff.value = (await $fetch<{ diff: string }>(`${base.value}/file/diff`, { query: { path: c.path } })).diff
  } catch (e) {
    changeDiff.value = ''
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const pickedChange = computed(() => changes.value?.find(c => c.path === picked.value))
function openChanged(path: string) {
  view.value = 'files'
  openFile(path)
}
watch(view, (v) => { if (v === 'changes') loadChanges() }, { immediate: true })
watch(() => props.projectId, () => { picked.value = ''; changeDiff.value = null; changes.value = null; if (view.value === 'changes') loadChanges() })
// another branch checked out: the tree, the open file and the changes are of it now
function onBranch() {
  reloadTree()
  if (file.value && !file.value.isNew && !dirty.value) openFile(file.value.path, true)
  loadChanges()
  if (picked.value) pickChange({ path: picked.value, status: '' })
}
const statusColor = (s: string) => s.includes('D') ? 'text-(--ui-error)' : s === '??' || s.includes('A') ? 'text-(--ui-success)' : 'text-(--ui-warning)'

function badges(f: Flags) {
  return [
    f.secret && { label: t('files.secret'), color: 'error' as const },
    f.protected && { label: t('files.protected'), color: 'warning' as const },
    f.ignored && { label: t('files.ignored'), color: 'neutral' as const }
  ].filter(Boolean) as { label: string, color: 'error' | 'warning' | 'neutral' }[]
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-3">
  <!-- files or what changed, on which branch -->
  <div class="flex flex-wrap items-center gap-2">
    <div class="flex rounded-md border border-(--ui-border) p-0.5">
      <UButton size="xs" :color="view === 'files' ? 'primary' : 'neutral'" :variant="view === 'files' ? 'soft' : 'ghost'" icon="i-lucide-folder-tree" :label="t('files.viewFiles')" @click="view = 'files'" />
      <UButton size="xs" :color="view === 'changes' ? 'primary' : 'neutral'" :variant="view === 'changes' ? 'soft' : 'ghost'" icon="i-lucide-file-diff" @click="view = 'changes'">
        {{ t('files.viewChanges') }}<UBadge v-if="changes?.length" :label="String(changes.length)" size="sm" color="neutral" variant="subtle" />
      </UButton>
    </div>
    <BranchMenu :project-id="projectId" @changed="onBranch" />
  </div>

  <!-- changes: the list, one file's diff -->
  <div v-if="view === 'changes'" class="grid min-h-0 flex-1 gap-4 md:grid-cols-[18rem_1fr]">
    <div class="flex max-h-[40vh] min-h-0 flex-col overflow-hidden rounded-lg border border-(--ui-border) md:max-h-none">
      <div class="flex items-center gap-1 border-b border-(--ui-border) px-2 py-1.5">
        <span class="flex-1 text-xs text-(--ui-text-muted)">{{ t('git.changes', { n: changes?.length ?? 0 }) }}</span>
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :aria-label="t('files.refresh')" :title="t('files.refresh')" @click="loadChanges" />
      </div>
      <div class="min-h-0 flex-1 overflow-auto py-1 text-sm">
        <LoadingRows v-if="!changes" :n="4" />
        <p v-else-if="!changes.length" class="p-3 text-xs text-(--ui-text-muted)">{{ t('files.noChangesAll') }}</p>
        <button
          v-for="c in changes ?? []" :key="c.path" type="button"
          class="flex w-full items-center gap-2 px-2 py-0.5 text-left hover:bg-(--ui-bg-elevated)" :class="picked === c.path && 'bg-(--ui-bg-elevated) font-medium'"
          :title="c.path" @click="pickChange(c)"
        >
          <span class="w-5 shrink-0 font-mono text-xs" :class="statusColor(c.status)">{{ c.status }}</span>
          <span class="min-w-0 flex-1 truncate font-mono text-xs">{{ c.path }}</span>
        </button>
      </div>
    </div>
    <div class="flex min-h-[60vh] min-w-0 flex-col overflow-hidden rounded-lg border border-(--ui-border)">
      <div v-if="!picked" class="flex flex-1 items-center justify-center p-6 text-sm text-(--ui-text-muted)">{{ t('files.pickChange') }}</div>
      <template v-else>
        <div class="flex items-center gap-2 border-b border-(--ui-border) bg-(--ui-bg-muted) px-3 py-1.5">
          <span class="min-w-0 flex-1 truncate font-mono text-xs">{{ picked }}</span>
          <UBadge v-if="pickedChange?.status.includes('D')" :label="t('files.deletedFile')" color="error" variant="subtle" size="sm" />
          <UButton v-else size="xs" color="neutral" variant="outline" icon="i-lucide-pencil" :label="t('files.openFile')" @click="openChanged(picked)" />
        </div>
        <LoadingRows v-if="changeDiff === null" :n="4" />
        <p v-else-if="!changeDiff" class="px-3 py-2 text-xs text-(--ui-text-muted)">{{ t('files.noChanges') }}</p>
        <DiffView v-else :diff="changeDiff" class="min-h-0 flex-1" />
      </template>
    </div>
  </div>

  <div v-else class="grid min-h-0 flex-1 gap-4 md:grid-cols-[18rem_1fr]">
    <!-- tree -->
    <div class="flex max-h-[40vh] min-h-0 flex-col overflow-hidden rounded-lg border border-(--ui-border) md:max-h-none">
      <div class="flex items-center gap-1 border-b border-(--ui-border) px-2 py-1.5">
        <USwitch v-model="showHidden" size="xs" :label="t('files.showHidden')" class="flex-1" />
        <UTooltip :text="t('files.hiddenInfo')"><UIcon name="i-lucide-info" class="size-3.5 text-(--ui-text-muted)" /></UTooltip>
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-file-plus" :aria-label="t('files.newFile')" :title="t('files.newFile')" @click="newFile" />
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :aria-label="t('files.refresh')" :title="t('files.refresh')" @click="reloadTree" />
      </div>
      <div class="min-h-0 flex-1 overflow-auto py-1 text-sm">
        <div v-if="rootError" class="space-y-2 p-3 text-sm">
          <p class="text-(--ui-error)">{{ rootError }}</p>
          <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-refresh-cw" :label="t('files.refresh')" @click="reloadTree" />
        </div>
        <LoadingRows v-else-if="!dirs['']" :n="6" />
        <template v-for="(r, i) in rows" :key="r.kind === 'entry' ? r.entry.path : `n${i}`">
          <button
            v-if="r.kind === 'entry'" type="button"
            class="flex w-full items-center gap-1.5 truncate px-2 py-0.5 text-left hover:bg-(--ui-bg-elevated)"
            :class="[file?.path === r.entry.path && 'bg-(--ui-bg-elevated) font-medium', (r.entry.ignored || r.entry.secret) && 'opacity-60']"
            :style="{ paddingLeft: `${0.5 + r.depth * 0.9}rem` }"
            @click="r.entry.dir ? toggleDir(r.entry) : openFile(r.entry.path)"
          >
            <UIcon
              :name="r.entry.dir ? (loadingDir.has(r.entry.path) ? 'i-lucide-loader-circle' : expanded.has(r.entry.path) ? 'i-lucide-folder-open' : 'i-lucide-folder') : r.entry.secret ? 'i-lucide-key-round' : 'i-lucide-file'"
              class="size-4 shrink-0" :class="[r.entry.dir ? 'text-primary' : 'text-(--ui-text-muted)', loadingDir.has(r.entry.path) && 'animate-spin']"
            />
            <span class="truncate">{{ r.entry.name }}</span>
            <UIcon v-if="r.entry.protected" name="i-lucide-lock" class="size-3 shrink-0 text-(--ui-warning)" />
          </button>
          <p v-else class="px-2 py-0.5 text-xs text-(--ui-text-muted)" :style="{ paddingLeft: `${0.5 + r.depth * 0.9}rem` }">{{ r.text }}</p>
        </template>
      </div>
    </div>

    <!-- editor -->
    <div class="flex min-h-[60vh] min-w-0 flex-col overflow-hidden rounded-lg border border-(--ui-border)">
      <div v-if="!file" class="flex flex-1 items-center justify-center p-6 text-sm text-(--ui-text-muted)">{{ t('files.pick') }}</div>
      <template v-else>
        <div class="flex flex-wrap items-center gap-2 border-b border-(--ui-border) bg-(--ui-bg-muted) px-3 py-1.5">
          <span class="min-w-0 flex-1 truncate font-mono text-xs">{{ file.path }}</span>
          <UBadge v-for="b in badges(file.flags)" :key="b.label" :label="b.label" :color="b.color" variant="subtle" size="sm" />
          <UBadge v-if="dirty" :label="t('files.unsaved')" color="warning" variant="outline" size="sm" />
          <template v-if="file.content !== undefined">
            <USwitch v-model="showDiff" size="xs" :label="t('files.changes')" />
            <UButton v-if="dirty" size="xs" color="neutral" variant="ghost" icon="i-lucide-undo-2" :label="t('files.revert')" @click="revert" />
            <UButton size="xs" icon="i-lucide-save" :label="t('files.save')" :loading="saving" :disabled="!dirty && !file.isNew" @click="save()" />
          </template>
        </div>
        <p v-if="file.binary" class="p-4 text-sm text-(--ui-text-muted)">{{ t('files.binary') }}</p>
        <p v-else-if="file.too_large" class="p-4 text-sm text-(--ui-text-muted)">{{ t('files.tooLarge', { size: fmtSize(file.size) }) }}</p>
        <div v-else class="flex min-h-0 flex-1 flex-col">
          <div v-if="showDiff" class="max-h-[40%] shrink-0 overflow-auto border-b border-(--ui-border)">
            <LoadingRows v-if="diff === null" :n="3" />
            <p v-else-if="!diff" class="px-3 py-2 text-xs text-(--ui-text-muted)">{{ t('files.noChanges') }}</p>
            <DiffView v-else :diff="diff" />
          </div>
          <div class="flex min-h-0 flex-1 overflow-auto font-mono text-xs leading-5">
            <pre aria-hidden="true" class="select-none border-r border-(--ui-border) px-2 py-2 text-right text-(--ui-text-dimmed)">{{ Array.from({ length: lineCount }, (_, i) => i + 1).join('\n') }}</pre>
            <textarea
              v-model="draft" spellcheck="false" wrap="off"
              class="min-w-0 flex-1 resize-none bg-transparent px-3 py-2 outline-none"
              :style="{ height: `calc(${lineCount * 1.25}rem + 1rem)` }"
              @keydown="onKey" @keydown.tab.exact="onTab"
            />
          </div>
        </div>
      </template>
    </div>
  </div>
  </div>
</template>
