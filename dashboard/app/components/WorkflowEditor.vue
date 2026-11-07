<script setup lang="ts">
// Writing a workflow (ADR-098): its file on one side, checked as it is
// written, its own chat in the corner; the chat's ```workflow blocks replace
// the file and nothing is saved until Save.
// chatProjectId: where the chat lives (the office assistant's project for the
// library). libKey: a library workflow to edit; projectId (+ workflowId): a
// project's own copy. Neither key nor id: a new one.
const props = defineProps<{ chatProjectId: string, projectId?: string, workflowId?: string, libKey?: string }>()
const toast = useToast()
const saveError = useSaveError()
const { t } = useLang()
const { isAdmin } = useAuth()
const library = computed(() => !props.projectId)
const editing = computed(() => !!(library.value ? props.libKey : props.workflowId))
const back = computed(() => library.value ? { path: '/workflows' } : { path: `/projects/${props.projectId}`, query: { tab: 'workflows' } })

const source = ref('')
const loaded = ref(!editing.value)
// a project's workflow: which agent fills each role (the canvas may add some)
const bindings = ref<Record<string, string>>({})
// ADR-072: the version read with the data; sent back on save so a stale save 409s instead of overwriting
const version = ref('')
onMounted(async () => {
  if (!editing.value) return
  try {
    if (library.value) {
      const w = (await $fetch<{ workflow: LibraryWorkflow }>(`/api/workflow-library/${props.libKey}`)).workflow
      if (w.builtin) {
        toast.add({ title: t('wf.builtinReadOnly'), color: 'info' })
        await navigateTo('/workflows')
        return
      }
      source.value = w.source ?? ''
      version.value = w.version
    } else {
      const w = (await $fetch<{ workflow: ProjectWorkflow }>(`/api/workflows/${props.workflowId}`)).workflow
      bindings.value = { ...(w.bindings ?? {}) }
      source.value = w.source
      version.value = w.version
    }
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    loaded.value = true
    markSaved()
  }
})
// what Save last wrote (or what was opened): leaving with more asks first
const snap = () => JSON.stringify([source.value, bindings.value])
const savedSnap = ref(snap())
function markSaved() { savedSnap.value = snap() }
useLeaveGuard(() => loaded.value && (fmtDirty || snap() !== savedSnap.value))

// checked as it is written: the preview, or what is wrong
const def = ref<WorkflowDef | null>(null)
const error = ref('')
// Canvas or text (ADR-108): both edit the one file. The canvas works on a
// def (draft); its changes are written back as the file (format), and the
// file's changes (typed, or the chat's) give the canvas its def again.
const mode = ref<'canvas' | 'text'>(editing.value ? 'text' : 'canvas')
let modeChosen = !editing.value
watch(mode, (m) => {
  modeChosen = true
  if (m !== 'canvas' && fmtDirty) void formatNow() // flush the pending canvas edit into the file before leaving it
})
const modes = computed(() => [
  { value: 'canvas', label: t('wf.canvas.tab'), icon: 'i-lucide-git-fork' },
  { value: 'text', label: t('wf.canvas.textTab'), icon: 'i-lucide-file-code' }
])
const draft = ref<WorkflowDef | null>(null)
const blank = (): WorkflowDef => ({
  key: '', name: '', description: '', roles: [], body: '', inputs: [], outputs: [],
  limits: { rounds: 0, turns: 0, timeout: '', budget_usd: 0, depth: 0, concurrency: 0 },
  steps: [{ id: 'end', type: 'end', position: { x: 280, y: 0 } }]
})
const canvasDef = computed(() => draft.value ?? (source.value.trim() ? null : blank()))

let timer: ReturnType<typeof setTimeout> | undefined
let fromCanvas: string | null = null // the file the canvas last wrote: its def is the draft already
watch(source, (s) => {
  clearTimeout(timer)
  if (s === fromCanvas) return
  timer = setTimeout(async () => {
    if (!s.trim()) { def.value = null; draft.value = null; error.value = ''; return }
    try {
      const res = await $fetch<{ ok: boolean, def?: WorkflowDef, draft?: WorkflowDef, error?: string }>('/api/workflow-library/validate', { method: 'POST', body: { source: s } })
      if (s !== source.value) return // written again since
      def.value = res.ok ? res.def ?? null : null
      error.value = res.ok ? '' : res.error ?? ''
      if (res.ok && res.def) draft.value = res.def
      else if (res.draft) draft.value = res.draft // it reads but does not check: the canvas still shows it, with the error
      if (!modeChosen) { // an existing one opens on the canvas once it reads
        if (res.def ?? res.draft) mode.value = 'canvas' // every workflow is a graph (ADR-111)
        modeChosen = true
      }
    } catch { /* checked again on Save */ }
  }, 400)
})

// the canvas changed the def: written as the file (one call per pause)
let fmtTimer: ReturnType<typeof setTimeout> | undefined
let fmtSeq = 0
let fmtDirty = false
function onCanvasDef(d: WorkflowDef) {
  draft.value = d
  fmtDirty = true
  clearTimeout(fmtTimer)
  fmtTimer = setTimeout(() => { void formatNow() }, 400)
}
async function formatNow() {
  clearTimeout(fmtTimer)
  if (!fmtDirty || !draft.value) return
  fmtDirty = false
  const seq = ++fmtSeq
  const before = source.value // written over only if nothing else changed source while this call was in flight
  try {
    const res = await $fetch<{ source: string, error?: string }>('/api/workflow-library/format', { method: 'POST', body: { def: draft.value } })
    if (seq !== fmtSeq || source.value !== before) return
    fromCanvas = res.source
    source.value = res.source
    error.value = res.error ?? ''
    def.value = res.error ? null : draft.value
  } catch (e) {
    if (seq === fmtSeq) error.value = apiError(e)
  }
}

// the chat in the corner fills the editor; back on its chat (?c=) it opens
const chatOpen = ref(!!useRoute().query.c)
// the text on a desktop: the chat beside it, always open
const docked = computed(() => mode.value === 'text')
const highlight = ref(false)
function applyPatch(p: Record<string, unknown>) {
  if (typeof p.source !== 'string') return
  source.value = p.source
  highlight.value = true
  setTimeout(() => { highlight.value = false }, 3000)
  toast.add({ title: t('wf.filled'), color: 'info' })
}
// back on its chat (?c=): a new workflow takes up the chat's last draft; a
// saved one keeps what it says
function replay(blocks: Record<string, unknown>[]) {
  const last = [...blocks].reverse().find(b => typeof b.source === 'string')
  if (editing.value || !last) return
  source.value = last.source as string
}
// its chat: found again by the workflow (a new one is tied to it once saved)
const conversationId = ref('')
const subject = computed(() => !editing.value ? undefined : library.value ? `lib:${props.libKey}` : `wf:${props.workflowId}`)
async function tieChat(s: string) {
  if (conversationId.value) await $fetch(`/api/conversations/${conversationId.value}/subject`, { method: 'PUT', body: { subject: s } }).catch(() => {})
}
const pageContext = () => JSON.stringify({ page: editing.value ? 'workflow.edit' : 'workflow.new', scope: library.value ? 'library' : 'project', draft: source.value, error: error.value })

const saving = ref(false)
async function save() {
  saving.value = true
  try {
    await formatNow() // what the canvas has not written yet
    if (error.value) return toast.add({ title: t('wf.invalid'), description: error.value, color: 'error' })
    if (library.value) {
      const res = await $fetch<{ ok: boolean, def?: WorkflowDef, error?: string }>('/api/workflow-library/validate', { method: 'POST', body: { source: source.value, bindings: bindings.value } })
      if (!res.ok || !res.def) {
        error.value = res.error ?? ''
        return toast.add({ title: t('wf.invalid'), description: res.error, color: 'error' })
      }
      const key = res.def.key
      if (editing.value && key !== props.libKey) return toast.add({ title: t('wf.keyChanged', { key: props.libKey ?? '' }), color: 'error' })
      let existing: LibraryWorkflow | null = null
      if (!editing.value) { // never overwrite a code-backed template from the editor
        existing = await $fetch<{ workflow: LibraryWorkflow }>(`/api/workflow-library/${key}`).then(r => r.workflow, () => null)
        if (existing?.builtin) return toast.add({ title: t('wf.builtinReadOnly'), color: 'info' })
        if (existing && !confirm(t('wf.overwrite', { key }))) return
      }
      const res2 = await $fetch<{ workflow: LibraryWorkflow }>(`/api/workflow-library/${key}`, { method: 'PUT', body: { source: source.value, version: existing?.version ?? version.value } })
      version.value = res2.workflow.version
      if (!editing.value) await tieChat(`lib:${key}`)
    } else if (editing.value) {
      const res = await $fetch<{ workflow: ProjectWorkflow }>(`/api/workflows/${props.workflowId}`, { method: 'PATCH', body: { source: source.value, bindings: bindings.value, version: version.value } })
      version.value = res.workflow.version
    } else {
      const res = await $fetch<{ workflow: ProjectWorkflow }>(`/api/projects/${props.projectId}/workflows`, { method: 'POST', body: { source: source.value, bindings: bindings.value } })
      await tieChat(`wf:${res.workflow.id}`)
    }
    toast.add({ title: t('wf.saved'), color: 'success' })
    markSaved()
    await navigateTo(back.value)
  } catch (e) {
    saveError(e, () => reloadNuxtApp())
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <!-- the chat sits in the corner like the office's (ADR-042), kept mounted
       so it fills the draft while closed; the text on a desktop: side by side -->
  <div class="flex gap-4 lg:h-[calc(100vh-9rem)] lg:min-h-[36rem]">
    <div class="flex min-w-0 flex-1 flex-col gap-2">
      <div class="flex items-center gap-2">
        <SegmentedNav v-model="mode" :items="modes" />
        <InfoTip :text="t('wf.canvas.modeInfo')" icon-class="size-4 text-(--ui-text-muted)" />
      </div>
      <div v-if="!loaded" class="p-4 text-sm text-(--ui-text-muted)">{{ t('common.loading') }}</div>
      <template v-else-if="mode === 'canvas'">
        <div class="flex min-h-[32rem] flex-1 flex-col *:flex-1 lg:min-h-0" :class="highlight && 'rounded-lg ring-2 ring-primary/60 ring-offset-2 ring-offset-(--ui-bg) transition'">
          <WorkflowCanvas
            v-if="canvasDef" :def="canvasDef" :project-id="projectId" :bindings="bindings"
            @update:def="onCanvasDef" @update:bindings="b => bindings = b"
          />
          <UAlert v-else color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('wf.canvas.cannotDraw')" :description="error" />
        </div>
        <UAlert v-if="error && canvasDef" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('wf.invalid')" :description="error" :ui="{ description: 'max-h-24 overflow-y-auto whitespace-pre-line' }" />
      </template>
      <div v-else class="space-y-4 lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pe-1">
        <UFormField :label="t('wf.source')" :class="highlight && 'rounded-lg ring-2 ring-primary/60 ring-offset-2 ring-offset-(--ui-bg) transition'">
          <template #hint>
            <InfoTip :text="t('wf.sourceInfo')" icon-class="size-4 text-(--ui-text-muted)" />
          </template>
          <UTextarea v-model="source" :rows="18" autoresize :maxrows="40" class="w-full font-mono text-xs" placeholder="---&#10;key: …&#10;name: …&#10;roles: …&#10;---" />
        </UFormField>
        <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('wf.invalid')" :description="error" />
        <div v-else-if="def" class="rounded-lg border border-(--ui-border) p-3">
          <WorkflowPreview :def="def" />
        </div>
      </div>
      <div class="flex justify-end gap-2 border-t border-(--ui-border) pt-3" :class="mode === 'canvas' ? 'pe-16' : 'max-lg:pe-16'">
        <UButton color="neutral" variant="ghost" :label="t('common.close')" :to="back" />
        <UButton icon="i-lucide-save" :loading="saving" :disabled="!isAdmin || !source.trim() || !!error" :label="t('auto.save')" @click="save()" />
      </div>
    </div>

    <UButton
      icon="i-lucide-sparkles" size="xl" class="fixed bottom-5 end-5 z-40 rounded-full shadow-lg"
      :class="[chatOpen && 'hidden', docked && 'lg:hidden']"
      :aria-label="t('wf.askAI')" :title="t('wf.askAI')" @click="chatOpen = true"
    />
    <!-- not a modal: the canvas stays in view while the agent fills it -->
    <aside
      class="fixed inset-y-0 end-0 z-50 w-full max-w-lg flex-col border-s border-(--ui-border) bg-(--ui-bg) shadow-xl"
      :class="[chatOpen ? 'flex' : 'hidden', docked && 'lg:static lg:z-auto lg:flex lg:w-1/2 lg:max-w-none lg:border-0 lg:shadow-none']"
    >
      <div class="flex items-center gap-1 border-b border-(--ui-border) p-2" :class="docked && 'lg:hidden'">
        <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-arrow-right" :aria-label="t('common.close')" @click="chatOpen = false" />
        <p class="min-w-0 flex-1 truncate font-semibold">{{ t('wf.askAI') }}</p>
      </div>
      <div class="min-h-0 flex-1 p-2" :class="docked && 'lg:p-0'">
        <ChatPanel
          :project-id="chatProjectId" purpose="workflow" :subject="subject" :page-context="pageContext"
          @workflow-patch="applyPatch" @history="replay" @conversation="(id) => { conversationId = id }"
        />
      </div>
    </aside>
  </div>
</template>
