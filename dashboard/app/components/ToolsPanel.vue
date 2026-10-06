<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'
import type { InstallSource } from './InstallModal.vue'

// Without `projectPath` the panel manages everything on the machine (Thư viện
// page). With it ('' = machine-wide helper project) it shows what that project
// gets and installs only there.
const props = defineProps<{ kind: ItemKind, projectPath?: string, projectId?: string }>()
const scoped = computed(() => props.projectPath !== undefined)
const fixedTarget = computed(() => (scoped.value ? (props.projectPath || '__user') : undefined))
const toast = useToast()
const { t, dateLocale } = useLang()
const { inv, loading, scan } = useInventory()
const meta = computed(() => kindMeta[props.kind])

// MCP health: the project folder, or machine-wide on the Thư viện page
const mcp = props.kind === 'mcp' ? useMcpStatus(() => props.projectPath ?? '') : null
const mcpBadge = computed(() => ({
  connected: { color: 'success' as const, label: t('tools.mcpConnected'), icon: 'i-lucide-circle-check', cls: 'text-success' },
  needs_auth: { color: 'warning' as const, label: t('tools.mcpNeedsAuth'), icon: 'i-lucide-key-round', cls: 'text-warning' },
  failed: { color: 'error' as const, label: t('tools.mcpFailed'), icon: 'i-lucide-circle-x', cls: 'text-error' },
  unknown: { color: 'neutral' as const, label: '?', icon: 'i-lucide-circle-help', cls: 'text-(--ui-text-muted)' }
}))
const mcpTip = (st: MCPState) => st.status === 'needs_auth' ? `${st.detail}. ${t('tools.mcpAuthHint')}` : st.detail
function countOf(list: MCPState[]) {
  const c = { connected: 0, needs_auth: 0, failed: 0 }
  for (const i of list) if (i.status in c) c[i.status as keyof typeof c]++
  return c
}
const mcpKnown = computed(() => new Set(items.value.map(i => i.name)))
const mcpCounts = computed(() => countOf((mcp?.check.value?.items ?? []).filter(i => mcpKnown.value.has(i.name))))
// servers claude sees that the inventory does not list (claude.ai connectors, plugins)
const mcpOthers = computed(() => (mcp?.check.value?.items ?? []).filter(i => !mcpKnown.value.has(i.name)))
const othersCounts = computed(() => countOf(mcpOthers.value))

const tabs = computed(() => [
  { label: scoped.value ? t('tools.tabInstalledScoped') : t('tools.tabInstalled'), value: 'installed', icon: 'i-lucide-hard-drive' },
  { label: scoped.value ? t('tools.tabLibraryScoped') : t('tools.tabLibrary'), value: 'library', icon: 'i-lucide-library' },
  ...(props.kind === 'mcp'
    ? [{ label: t('tools.tabCatalog'), value: 'catalog', icon: 'i-lucide-star' }, { label: t('tools.tabSearch'), value: 'search', icon: 'i-lucide-search' }]
    : [])
])
const tab = ref('installed')

// ---- installed ----
const q = ref('')
const where = ref('__all')
const items = computed(() => (inv.value?.items ?? []).filter((i) => {
  if (i.kind !== props.kind) return false
  if (!scoped.value) return true
  // a project sees its own items, its private (local) MCP, and machine-wide ones
  return i.location.type === 'user' || (!!props.projectPath && i.location.project_path === props.projectPath && (i.location.type === 'project' || i.location.type === 'local'))
}))
const whereOptions = computed(() => {
  const labels = new Map<string, string>()
  for (const i of items.value) labels.set(locKey(i.location), i.location.label)
  return [{ label: t('tools.everywhere'), value: '__all' }, ...[...labels].map(([value, label]) => ({ label, value }))]
})
function locKey(l: AutoLocation) {
  return l.type === 'project' || l.type === 'local' ? `${l.type}:${l.project_path}` : l.type === 'plugin' ? `plugin:${l.label}` : l.type
}
const groups = computed(() => {
  const needle = q.value.trim().toLowerCase()
  const map = new Map<string, { location: AutoLocation, items: AutoItem[] }>()
  for (const i of items.value) {
    if (where.value !== '__all' && locKey(i.location) !== where.value) continue
    if (needle && !`${i.name} ${i.description}`.toLowerCase().includes(needle)) continue
    const k = locKey(i.location)
    if (!map.has(k)) {
      const scopedLabel = { user: t('tools.locUser'), project: t('tools.locProject'), local: t('tools.locLocal') }[i.location.type as 'user' | 'project' | 'local']
      map.set(k, { location: scoped.value && scopedLabel ? { ...i.location, label: scopedLabel } : i.location, items: [] })
    }
    map.get(k)!.items.push(i)
  }
  const order: AutoLocation['type'][] = ['user', 'project', 'local', 'plugin', 'cursor', 'claude_desktop', 'codex']
  return [...map.values()].sort((a, b) => order.indexOf(a.location.type) - order.indexOf(b.location.type) || a.location.label.localeCompare(b.location.label))
})

function itemMenu(i: AutoItem): DropdownMenuItem[][] {
  const main: DropdownMenuItem[] = [
    { label: t('tools.menuView'), icon: 'i-lucide-eye', onSelect: () => view(i) },
    // a skill of this project or of the machine: edited in office's editor (with its AI)
    ...(i.kind === 'skill' && props.projectId && i.location.editable && (i.location.type === 'project' || i.location.type === 'user')
      ? [{ label: t('skill.edit'), icon: 'i-lucide-pencil', to: `/projects/${props.projectId}/skills/edit?name=${encodeURIComponent(i.name)}&scope=${i.location.type}` }]
      : []),
    { label: scoped.value ? t('tools.menuCopyToProject') : t('tools.menuCopyElsewhere'), icon: 'i-lucide-copy', disabled: !canCopy(i), onSelect: () => startInstall({ kind: i.kind, name: i.name, from: i }) },
    { label: t('tools.menuSaveToLibrary'), icon: 'i-lucide-library', disabled: i.location.type === 'codex', onSelect: () => saveToLibrary(i) }
  ]
  // in a project, machine-wide items are removed from the library page
  const removable = i.location.editable && !(scoped.value && i.location.type === 'user')
  return removable ? [main, [{ label: t('tools.menuRemove'), icon: 'i-lucide-trash-2', color: 'error', onSelect: () => remove(i) }]] : [main]
}

// ---- viewer ----
const viewOpen = ref(false)
const viewing = ref<LibraryItem | null>(null)
async function view(i: AutoItem) {
  try {
    viewing.value = await $fetch<LibraryItem>('/api/automation/content', { method: 'POST', body: refOf(i) })
    viewOpen.value = true
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const viewFiles = computed(() => {
  const v = viewing.value
  if (!v) return []
  if (v.template) return [{ name: 'config.json', content: JSON.stringify(v.template.config, null, 2) }]
  return Object.entries(v.files ?? {}).sort(([a], [b]) => (a === 'SKILL.md' ? -1 : b === 'SKILL.md' ? 1 : a.localeCompare(b))).map(([name, content]) => ({ name, content }))
})

async function saveToLibrary(i: AutoItem) {
  try {
    await $fetch('/api/automation/save-to-library', { method: 'POST', body: { ref: refOf(i), name: '' } })
    toast.add({ title: t('tools.savedToLibrary', { name: i.name }), description: i.kind === 'mcp' ? t('tools.savedToLibraryMcpDesc') : undefined, color: 'success' })
    refreshLib()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

async function remove(i: AutoItem) {
  const msg = i.kind === 'mcp'
    ? t('tools.confirmRemoveMcp', { name: i.name, location: i.location.label })
    : t('tools.confirmRemoveOther', { name: i.name, location: i.location.label })
  if (!confirm(msg)) return
  try {
    await $fetch('/api/automation/remove', { method: 'POST', body: refOf(i) })
    toast.add({ title: t('tools.removed', { name: i.name }), color: 'success' })
    scan()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// ---- an MCP row and office's gateway: add it there (the import flow), off/on ----
interface McpCandidate { ref: ReturnType<typeof refOf>, name: string, suggested: string, movable: boolean, taken: boolean, moved_as?: string, problem?: string }
const candKey = (r: { type: string, path: string, project_path?: string, name: string }) => `${r.type}|${r.path}|${r.project_path ?? ''}|${r.name}`
const candidates = ref(new Map<string, McpCandidate>())
async function loadCandidates() {
  if (props.kind !== 'mcp') return
  try {
    const r = await $fetch<{ candidates: McpCandidate[] }>('/api/mcp/import')
    candidates.value = new Map(r.candidates.map(c => [candKey(c.ref), c]))
  } catch {
    candidates.value = new Map()
  }
}
watch(inv, loadCandidates, { immediate: true })
const candOf = (i: AutoItem) => candidates.value.get(candKey(refOf(i)))
const inOffice = (c?: McpCandidate) => !!c && (!!c.moved_as || c.taken)
const canAdd = (c: McpCandidate) => c.movable && !c.problem
const addTip = (c: McpCandidate) => c.problem === 'sse' ? t('tools.gwImportSse') : !c.movable ? t('tools.mcpAddNotMovable') : t('tools.mcpAddInfo')
interface OfficeServer { id: string, name: string, last_check_status: string, tools: unknown[] }
const officeMcp = ref<{ refresh: () => Promise<void>, servers: OfficeServer[], openClientForm: (id: string, msg: string) => void } | null>(null)
const busy = ref<Record<string, boolean>>({})
const officeNameOf = (c: McpCandidate) => c.moved_as || (c.taken ? c.suggested : '')
const officeServerOf = (c?: McpCandidate) => c ? officeMcp.value?.servers.find(s => s.name === officeNameOf(c)) : undefined

// ---- "Đăng nhập qua office": a row claude cannot use until someone logs in.
// Office cannot log in for Claude Code (its tokens are in its own keychain),
// so the server is copied into office (the row stays) and office logs in.
const { connect: startLogin, connecting } = useMcpLogin()
const canLoginViaOffice = (i: AutoItem) => {
  const c = candOf(i)
  if (i.kind !== 'mcp' || !i.meta?.url || mcp?.byName.value.get(i.name)?.status !== 'needs_auth' || !c || c.problem) return false
  return inOffice(c) ? officeServerOf(c)?.last_check_status !== 'ok' : c.movable
}
const awaitingLogin = new Map<string, AutoItem>() // office server id → its row
async function officeIdFor(i: AutoItem) {
  const c = candOf(i)!
  const have = officeServerOf(c)
  if (have) return have.id
  const r = await $fetch<{ results: { ok: boolean, name: string, error?: string, server?: { id: string } }[] }>('/api/mcp/import', {
    method: 'POST', body: { take_out: false, items: [{ ref: c.ref, name: c.suggested }] }
  })
  const res = r.results[0]
  if (!res?.ok || !res.server) {
    toast.add({ title: res?.error ?? t('tools.mcpAddFailed'), color: 'error' })
    return null
  }
  await officeMcp.value?.refresh()
  await loadCandidates()
  return res.server.id
}
async function loginViaOffice(i: AutoItem) {
  let id = ''
  const ok = await startLogin('login|' + candKey(refOf(i)), async () => (id = (await officeIdFor(i)) ?? ''),
    (sid, msg) => officeMcp.value?.openClientForm(sid, msg))
  if (ok) awaitingLogin.set(id, i)
}
// office checks the server once the login is back: ok = it works
onLiveEvent<OfficeServer>('mcp.gateway.status', (s) => {
  const i = awaitingLogin.get(s.id)
  if (!i || s.last_check_status !== 'ok') return
  awaitingLogin.delete(s.id)
  const off = canToggle(i) && !i.disabled
  toast.add({
    title: t('tools.mcpLoginDone', { name: s.name, n: s.tools?.length ?? 0 }),
    description: off ? t('tools.mcpLoginDoneHint') : undefined,
    color: 'success',
    actions: off ? [{ label: t('tools.mcpTurnOffSource'), onClick: () => { setEnabled(i, false) } }] : undefined
  })
})
async function addToOffice(i: AutoItem) {
  const c = candOf(i)
  if (!c) return
  const k = candKey(c.ref)
  busy.value = { ...busy.value, [k]: true }
  try {
    const r = await $fetch<{ results: { ok: boolean, name: string, error?: string, missing?: string[] }[] }>('/api/mcp/import', {
      method: 'POST', body: { items: [{ ref: c.ref, name: c.suggested }] }
    })
    const res = r.results[0]
    if (res?.ok) {
      toast.add({ title: t('tools.mcpAdded', { name: res.name }), description: res.missing?.length ? t('tools.gwMissingVars', { vars: res.missing.join(', ') }) : t('tools.mcpAddedHint'), color: 'success' })
    } else {
      toast.add({ title: res?.error ?? t('tools.mcpAddFailed'), color: 'error' })
    }
    await officeMcp.value?.refresh()
    await loadCandidates()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = { ...busy.value, [k]: false }
  }
}
// in a project, machine-wide servers are switched from the library page
const canToggle = (i: AutoItem) => i.kind === 'mcp' && mcpToggleable(i.location) && !(scoped.value && i.location.type === 'user')
async function setEnabled(i: AutoItem, on: boolean) {
  const k = 'on|' + candKey(refOf(i))
  busy.value = { ...busy.value, [k]: true }
  try {
    await $fetch('/api/automation/mcp/enabled', { method: 'POST', body: { ref: refOf(i), enabled: on } })
    toast.add({ title: on ? t('tools.mcpTurnedOn', { name: i.name }) : t('tools.mcpTurnedOff', { name: i.name }), color: 'success' })
    await scan()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = { ...busy.value, [k]: false }
  }
}

// ---- install ----
const installOpen = ref(false)
const installSource = ref<InstallSource | null>(null)
function startInstall(s: InstallSource) {
  installSource.value = s
  installOpen.value = true
}

// ---- library ----
// no top-level await here or in OfficeMcpServers: nested async setup under the
// library page's :key remount looped Suspense (Maximum call stack on the MCP tab)
const { data: libData, refresh: refreshLib, error: libError } = useLiveFetch<{ items: LibraryItem[] }>(() => `/api/automation/library/${props.kind}`)
const library = computed(() => libData.value?.items ?? [])

const editOpen = ref(false)
const editing = ref<{ isNew: boolean, name: string, content: string, description: string, files: Record<string, string> }>({ isNew: true, name: '', content: '', description: '', files: {} })
const editError = ref('')
const editFindings = ref<Finding[]>([])

// Starter templates for a new library entry; kept as-is (not translated: they
// are boilerplate content the user edits, not UI copy).
const starter: Record<ItemKind, string> = {
  skill: '---\nname: ten-skill\ndescription: Dùng khi ... (mô tả ngắn để Claude biết lúc nào nên dùng)\n---\n\n# Hướng dẫn\n\n1. ...\n', // i18n-ignore
  agent: '---\nname: ten-agent\ndescription: Agent chuyên ... Dùng khi ...\ntools: Read, Grep, Glob\n---\n\nBạn là ...\n', // i18n-ignore
  mcp: '{\n  "type": "stdio",\n  "command": "npx",\n  "args": ["-y", "ten-goi-mcp"],\n  "env": { "API_KEY": "{{API_KEY}}" }\n}\n'
}

async function openEdit(item?: LibraryItem) {
  editError.value = ''
  editFindings.value = []
  if (!item) {
    editing.value = { isNew: true, name: '', content: starter[props.kind], description: '', files: {} }
  } else {
    const full = await $fetch<LibraryItem>(`/api/automation/library/${props.kind}/${item.name}`)
    const files = full.files ?? {}
    editing.value = {
      isNew: false,
      name: full.name,
      description: full.template?.description ?? '',
      files,
      content: props.kind === 'mcp' ? JSON.stringify(full.template?.config ?? {}, null, 2) : props.kind === 'skill' ? files['SKILL.md'] ?? '' : Object.values(files)[0] ?? ''
    }
  }
  editOpen.value = true
}

// {{KEY}} placeholders in an MCP config become inputs
function inputsFrom(cfg: Record<string, unknown>): TemplateInput[] {
  const out: TemplateInput[] = []
  const walk = (v: unknown, kind: TemplateInput['kind']) => {
    if (typeof v === 'string') {
      for (const m of v.matchAll(/\{\{([A-Za-z0-9_]+)\}\}/g)) {
        if (!out.some(i => i.key === m[1])) out.push({ key: m[1]!, label: m[1]!, kind, secret: /key|token|secret|password|auth/i.test(m[1]!), required: true })
      }
    } else if (Array.isArray(v)) v.forEach(e => walk(e, kind))
    else if (v && typeof v === 'object') for (const [k, e] of Object.entries(v)) walk(e, k === 'env' ? 'env' : k === 'headers' ? 'header' : kind)
  }
  walk(cfg, 'arg')
  return out
}

async function saveEdit() {
  const e = editing.value
  editError.value = ''
  let body: Record<string, unknown>
  if (props.kind === 'mcp') {
    let cfg: Record<string, unknown>
    try {
      cfg = JSON.parse(e.content)
    } catch {
      editError.value = t('tools.invalidJson')
      return
    }
    body = { template: { name: e.name, title: e.name, description: e.description, config: cfg, inputs: inputsFrom(cfg) } }
  } else if (props.kind === 'skill') {
    body = { files: { ...e.files, 'SKILL.md': e.content } }
  } else {
    body = { files: { [`${e.name}.md`]: e.content } }
  }
  try {
    await $fetch(`/api/automation/library/${props.kind}/${e.name.trim()}`, { method: 'PUT', body })
    editOpen.value = false
    toast.add({ title: t('tools.saved', { name: e.name }), color: 'success' })
    refreshLib()
  } catch (err) {
    editError.value = apiError(err)
    editFindings.value = (err as { data?: { findings?: Finding[] } }).data?.findings ?? []
  }
}

async function removeLib(item: LibraryItem) {
  if (!confirm(t('tools.confirmRemoveLibrary', { name: item.name }))) return
  try {
    await $fetch(`/api/automation/library/${props.kind}/${item.name}`, { method: 'DELETE' })
    refreshLib()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

async function installLib(item: LibraryItem) {
  if (props.kind === 'mcp') {
    const full = await $fetch<LibraryItem>(`/api/automation/library/mcp/${item.name}`)
    startInstall({ kind: 'mcp', name: item.name, library: item.name, template: full.template })
  } else {
    startInstall({ kind: props.kind, name: item.name, library: item.name })
  }
}

// ---- MCP catalog & registry ----
const installedNames = computed(() => new Set(items.value.map(i => i.name)))
// in a project, copying from the machine means "install here"
const canCopy = (i: AutoItem) => i.location.type !== 'codex' && !(scoped.value && i.location.type !== 'user')
const { data: catalogData } = useLiveFetch<{ items: MCPTemplate[] }>('/api/automation/mcp/catalog', { immediate: props.kind === 'mcp' })
const catalog = computed(() => catalogData.value?.items ?? [])
const regQ = ref('')
const regItems = ref<MCPTemplate[]>([])
const regBusy = ref(false)
const regError = ref('')
const regSearched = ref(false)
async function searchRegistry() {
  if (!regQ.value.trim()) return
  regBusy.value = true
  regError.value = ''
  try {
    regItems.value = (await $fetch<{ items: MCPTemplate[] }>('/api/automation/mcp/registry', { query: { q: regQ.value } })).items
    regSearched.value = true
  } catch (e) {
    regError.value = apiError(e)
  } finally {
    regBusy.value = false
  }
}
async function saveTemplate(tpl: MCPTemplate) {
  try {
    await $fetch(`/api/automation/library/mcp/${tpl.name}`, { method: 'PUT', body: { template: { ...tpl, source: undefined, id: undefined } } })
    toast.add({ title: t('tools.savedToLibrary', { name: tpl.title }), color: 'success' })
    refreshLib()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const summary = (tpl: MCPTemplate) => {
  const c = tpl.config as { url?: string, command?: string, args?: string[] }
  return c.url ?? [c.command, ...(c.args ?? [])].join(' ')
}
</script>

<template>
  <div>
    <div class="space-y-4">
      <div class="flex flex-wrap items-center gap-2">
        <UTabs v-model="tab" :items="tabs" :content="false" size="sm" variant="link" :ui="{ root: 'min-w-0', list: 'overflow-x-auto', trigger: 'shrink-0' }" />
        <div class="ms-auto flex gap-2">
          <UButton v-if="tab === 'library' && !scoped" size="sm" icon="i-lucide-plus" :label="t('tools.createNew')" @click="openEdit()" />
          <UButton v-if="kind === 'skill' && projectId && tab === 'installed'" size="sm" icon="i-lucide-plus" :label="t('skill.new')" :to="`/projects/${projectId}/skills/edit`" />
          <UButton size="sm" color="neutral" variant="outline" icon="i-lucide-scan-search" :label="t('tools.rescan')" :loading="loading" @click="scan" />
        </div>
      </div>

      <!-- installed -->
      <template v-if="tab === 'installed'">
        <div class="flex flex-wrap gap-2">
          <UInput v-model="q" icon="i-lucide-search" :placeholder="t('tools.searchPlaceholder')" class="w-64" />
          <USelect v-if="!scoped" v-model="where" :items="whereOptions" class="w-64" />
          <span class="self-center text-sm text-(--ui-text-muted)">{{ t('tools.itemCount', { n: groups.reduce((a, g) => a + g.items.length, 0) }) }}</span>
        </div>
        <div v-if="mcp" class="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-(--ui-border) px-3 py-2 text-sm">
          <template v-if="mcp.checked.value">
            <span v-for="k in (['connected', 'needs_auth', 'failed'] as const)" v-show="mcpCounts[k]" :key="k" class="inline-flex items-center gap-1">
              <UIcon :name="mcpBadge[k].icon" :class="mcpBadge[k].cls" />{{ mcpCounts[k] }} {{ mcpBadge[k].label.toLowerCase() }}
            </span>
          </template>
          <span class="text-(--ui-text-muted)">
            <template v-if="mcp.check.value?.running">{{ t('tools.mcpChecking') }}</template>
            <template v-else-if="mcp.checked.value">{{ t('tools.mcpCheckedAt', { time: new Date(mcp.check.value!.checked_at).toLocaleTimeString(dateLocale, { hour: '2-digit', minute: '2-digit' }) }) }}</template>
            <template v-else>{{ t('tools.mcpNeverChecked') }}</template>
          </span>
          <span v-if="mcp.check.value?.error" class="text-(--ui-error)">{{ mcp.check.value.error }}</span>
          <UTooltip :text="t('tools.mcpCheckInfo')">
            <UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" />
          </UTooltip>
          <UButton class="ms-auto" size="xs" color="neutral" variant="outline" icon="i-lucide-refresh-cw" :label="t('tools.mcpCheck')" :loading="mcp.check.value?.running" @click="mcp.recheck()" />
        </div>
        <OfficeMcpServers v-if="kind === 'mcp'" ref="officeMcp" @changed="loadCandidates" />
        <div v-if="loading && !inv" class="py-10 text-center text-(--ui-text-muted)">{{ t('tools.scanning') }}</div>
        <div v-else-if="!groups.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
          <UIcon :name="meta.icon" class="mx-auto size-8 text-(--ui-text-dimmed)" />
          <p class="mt-2 font-medium">{{ t('tools.emptyInstalledTitle', { label: meta.label.toLowerCase() }) }}</p>
          <p class="text-sm text-(--ui-text-muted)">
            {{ kind === 'mcp' ? t('tools.emptyInstalledMcpHint') : t('tools.emptyInstalledOtherHint') }}
          </p>
        </div>
        <section v-for="g in groups" :key="locKey(g.location)" class="space-y-2">
          <h3 class="flex items-center gap-2 text-sm font-semibold">
            <UIcon :name="locationIcon[g.location.type]" class="text-(--ui-text-muted)" />
            {{ g.location.label }}
            <span class="font-normal text-(--ui-text-muted)">· {{ g.items.length }}</span>
            <UBadge v-if="!g.location.editable" color="neutral" variant="subtle" size="sm" :label="t('tools.readOnly')" />
            <NuxtLink v-if="g.location.project_id && !scoped" :to="`/projects/${g.location.project_id}`" class="text-xs font-normal text-(--ui-primary)">{{ t('tools.openProject') }}</NuxtLink>
          </h3>
          <div class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
            <div v-for="i in g.items" :key="i.location.path + i.name" class="flex items-center gap-3 px-4 py-2.5">
              <UIcon :name="meta.icon" class="size-4 shrink-0 text-primary" :class="{ 'opacity-50': i.disabled }" />
              <div class="min-w-0 flex-1" :class="{ 'opacity-50': i.disabled }">
                <p class="truncate font-mono text-sm font-medium">{{ i.name }}</p>
                <p v-if="i.description" class="line-clamp-1 text-xs text-(--ui-text-muted)">{{ i.description }}</p>
                <p v-else-if="i.meta?.url || i.meta?.command" class="truncate font-mono text-xs text-(--ui-text-muted)">
                  {{ i.meta?.transport }} · {{ i.meta?.url ?? i.meta?.command }}
                </p>
              </div>
              <UBadge v-if="i.meta?.tools" color="neutral" variant="subtle" size="sm" :label="i.meta.tools" class="hidden max-w-48 truncate md:inline-flex" />
              <UBadge v-if="i.disabled" color="neutral" variant="outline" size="sm" icon="i-lucide-pause" :label="t('tools.mcpOff')" />
              <UTooltip v-else-if="mcp?.byName.value.get(i.name)" :text="mcpTip(mcp.byName.value.get(i.name)!)">
                <UBadge :color="mcpBadge[mcp.byName.value.get(i.name)!.status].color" variant="subtle" size="sm" :icon="mcpBadge[mcp.byName.value.get(i.name)!.status].icon" :label="mcpBadge[mcp.byName.value.get(i.name)!.status].label" />
              </UTooltip>
              <UTooltip v-if="!i.disabled && canLoginViaOffice(i)" :text="t('tools.mcpLoginViaOfficeInfo')">
                <UButton size="xs" icon="i-lucide-log-in" :label="t('tools.mcpLoginViaOffice')" :loading="connecting['login|' + candKey(refOf(i))]" @click="loginViaOffice(i)" />
              </UTooltip>
              <template v-if="kind === 'mcp' && candOf(i)">
                <UTooltip v-if="inOffice(candOf(i))" :text="t('tools.mcpInOfficeInfo', { name: candOf(i)!.moved_as || candOf(i)!.suggested })">
                  <NuxtLink to="/library?kind=mcp#office-mcp">
                    <UBadge color="primary" variant="subtle" size="sm" icon="i-lucide-network" :label="t('tools.mcpInOffice')" />
                  </NuxtLink>
                </UTooltip>
                <UTooltip v-else :text="addTip(candOf(i)!)">
                  <span>
                    <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-arrow-right-to-line" :label="t('tools.mcpAddToOffice')"
                             :disabled="!canAdd(candOf(i)!)" :loading="busy[candKey(candOf(i)!.ref)]" @click="addToOffice(i)" />
                  </span>
                </UTooltip>
              </template>
              <UTooltip v-if="canToggle(i)" :text="i.disabled ? t('tools.mcpOffInfo') : t('tools.mcpOnInfo')">
                <USwitch :model-value="!i.disabled" size="sm" :loading="busy['on|' + candKey(refOf(i))]" :aria-label="t('tools.mcpSwitch')"
                         @update:model-value="(v: boolean) => setEnabled(i, v)" />
              </UTooltip>
              <UDropdownMenu :items="itemMenu(i)" :content="{ align: 'end' }">
                <UButton color="neutral" variant="ghost" icon="i-lucide-ellipsis" :aria-label="t('common.actions')" />
              </UDropdownMenu>
            </div>
          </div>
        </section>
        <details v-if="mcpOthers.length" class="group space-y-2">
          <summary class="flex cursor-pointer list-none items-center gap-2 text-sm font-semibold">
            <UIcon name="i-lucide-chevron-right" class="text-(--ui-text-muted) transition group-open:rotate-90" />
            {{ t('tools.mcpOthers', { n: mcpOthers.length }) }}
            <span v-for="k in (['connected', 'needs_auth', 'failed'] as const)" v-show="othersCounts[k]" :key="k" class="inline-flex items-center gap-1 text-xs font-normal text-(--ui-text-muted)">
              <UIcon :name="mcpBadge[k].icon" :class="mcpBadge[k].cls" />{{ othersCounts[k] }}
            </span>
          </summary>
          <div class="mt-2 divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
            <div v-for="o in mcpOthers" :key="o.name" class="flex items-center gap-3 px-4 py-2">
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium">{{ o.name }}</p>
                <p class="truncate font-mono text-xs text-(--ui-text-muted)">{{ o.target }}</p>
              </div>
              <UTooltip :text="mcpTip(o)">
                <UBadge :color="mcpBadge[o.status].color" variant="subtle" size="sm" :icon="mcpBadge[o.status].icon" :label="mcpBadge[o.status].label" />
              </UTooltip>
            </div>
          </div>
        </details>
      </template>

      <!-- library -->
      <template v-else-if="tab === 'library'">
        <p v-if="!scoped" class="text-sm text-(--ui-text-muted)">
          {{ t('tools.libraryIntro') }}
        </p>
        <UAlert v-if="libError" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('common.loadError')" :actions="[{ label: t('common.refresh'), onClick: () => refreshLib() }]" />
        <div v-else-if="!library.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
          <UIcon name="i-lucide-library" class="mx-auto size-8 text-(--ui-text-dimmed)" />
          <p class="mt-2 font-medium">{{ t('tools.libraryEmptyTitle') }}</p>
          <p class="text-sm text-(--ui-text-muted)">
            <template v-if="scoped">
              {{ t('tools.libraryEmptyScopedHintPrefix') }} <NuxtLink to="/library" class="text-(--ui-primary)">{{ t('tools.libraryPageTitle') }}</NuxtLink>.
            </template>
            <template v-else>{{ t('tools.libraryEmptyHint') }}</template>
          </p>
        </div>
        <div v-else class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
          <div v-for="l in library" :key="l.name" class="flex items-center gap-3 px-4 py-2.5">
            <UIcon :name="meta.icon" class="size-4 shrink-0 text-primary" />
            <div class="min-w-0 flex-1">
              <p class="truncate font-mono text-sm font-medium">{{ l.name }}</p>
              <p v-if="l.description" class="line-clamp-1 text-xs text-(--ui-text-muted)">{{ l.description }}</p>
            </div>
            <UButton size="sm" icon="i-lucide-download" :label="t('tools.install')" @click="installLib(l)" />
            <template v-if="!scoped">
              <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-pencil" :aria-label="t('common.edit')" @click="openEdit(l)" />
              <UButton size="sm" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('common.delete')" @click="removeLib(l)" />
            </template>
          </div>
        </div>
      </template>

      <!-- catalog -->
      <template v-else-if="tab === 'catalog'">
        <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          <div v-for="tpl in catalog" :key="tpl.id" class="flex flex-col gap-2 rounded-lg border border-(--ui-border) p-4">
            <div class="flex items-center gap-2">
              <p class="font-medium">{{ tpl.title }}</p>
              <UBadge v-if="tpl.category" color="neutral" variant="subtle" size="sm" :label="tpl.category" />
              <UBadge v-if="installedNames.has(tpl.name)" color="success" variant="subtle" size="sm" icon="i-lucide-check" :label="t('tools.installedBadge')" class="ms-auto" />
            </div>
            <p class="text-sm text-(--ui-text-muted)">{{ tpl.description }}</p>
            <code class="truncate text-xs text-(--ui-text-dimmed)">{{ summary(tpl) }}</code>
            <div class="mt-auto flex items-center gap-2 pt-1">
              <UButton size="sm" icon="i-lucide-download" :label="t('tools.install')" @click="startInstall({ kind: 'mcp', name: tpl.name, template: tpl })" />
              <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-library" :label="t('common.save')" @click="saveTemplate(tpl)" />
              <UButton v-if="tpl.homepage" size="sm" color="neutral" variant="link" :to="tpl.homepage" target="_blank" icon="i-lucide-external-link" :aria-label="t('tools.homepageAria')" class="ms-auto" />
            </div>
          </div>
        </div>
      </template>

      <!-- registry search -->
      <template v-else-if="tab === 'search'">
        <form class="flex gap-2" @submit.prevent="searchRegistry">
          <UInput v-model="regQ" icon="i-lucide-search" :placeholder="t('tools.registrySearchPlaceholder')" class="flex-1" autofocus />
          <UButton type="submit" :label="t('tools.registrySearchBtn')" :loading="regBusy" />
        </form>
        <p class="text-xs text-(--ui-text-muted)">
          {{ t('tools.registrySourceNote') }}
        </p>
        <UAlert v-if="regError" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="regError" />
        <p v-if="regSearched && !regItems.length" class="py-6 text-center text-(--ui-text-muted)">{{ t('tools.registryNoResults') }}</p>
        <div class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)" :class="{ hidden: !regItems.length }">
          <div v-for="tpl in regItems" :key="tpl.id" class="flex items-start gap-3 px-4 py-3">
            <div class="min-w-0 flex-1">
              <p class="font-medium">{{ tpl.title }}</p>
              <p class="line-clamp-2 text-sm text-(--ui-text-muted)">{{ tpl.description }}</p>
              <code class="block truncate text-xs text-(--ui-text-dimmed)">{{ summary(tpl) }}</code>
            </div>
            <UButton v-if="tpl.homepage" size="sm" color="neutral" variant="ghost" :to="tpl.homepage" target="_blank" icon="i-lucide-external-link" :aria-label="t('tools.homepageAria')" />
            <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-library" :aria-label="t('tools.saveToLibraryAria')" @click="saveTemplate(tpl)" />
            <UButton size="sm" icon="i-lucide-download" :label="t('tools.install')" @click="startInstall({ kind: 'mcp', name: tpl.name, template: tpl })" />
          </div>
        </div>
      </template>
    </div>

    <InstallModal v-model:open="installOpen" :source="installSource" :fixed-target="fixedTarget" @done="scan" />

    <UModal v-model:open="viewOpen" :title="viewing?.name ?? ''" :ui="{ content: 'max-w-3xl' }">
      <template #body>
        <div class="space-y-3">
          <p v-if="viewing?.kind === 'mcp'" class="text-xs text-(--ui-text-muted)">{{ t('tools.secretMasked') }}</p>
          <div v-for="f in viewFiles" :key="f.name">
            <p class="mb-1 font-mono text-xs text-(--ui-text-muted)">{{ f.name }}</p>
            <pre class="max-h-96 overflow-auto rounded-md bg-(--ui-bg-elevated) p-3 text-xs whitespace-pre-wrap">{{ f.content }}</pre>
          </div>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="editOpen" :title="editing.isNew ? t('tools.createTitle', { label: meta.label.toLowerCase() }) : t('tools.editTitle', { name: editing.name })" :ui="{ content: 'max-w-3xl' }">
      <template #body>
        <form id="lib-form" class="space-y-4" @submit.prevent="saveEdit">
          <UFormField :label="t('common.name')" :help="t('tools.nameHelp')">
            <UInput v-model="editing.name" :disabled="!editing.isNew" class="w-full font-mono" required />
          </UFormField>
          <UFormField v-if="kind === 'mcp'" :label="t('common.description')">
            <UInput v-model="editing.description" class="w-full" />
          </UFormField>
          <UFormField
            :label="kind === 'skill' ? t('tools.contentSkill') : kind === 'agent' ? t('tools.contentAgent') : t('tools.contentConfig')"
            :help="kind === 'mcp' ? t('tools.contentMcpHelp') : undefined"
          >
            <UTextarea v-model="editing.content" :rows="16" autoresize class="w-full font-mono text-xs" />
          </UFormField>
          <p v-if="kind === 'skill' && Object.keys(editing.files).length > 1" class="text-xs text-(--ui-text-muted)">
            {{ t('tools.keepOtherFiles', { n: Object.keys(editing.files).length - 1 }) }}
          </p>
          <UAlert v-if="editError" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="editError">
            <template v-if="editFindings.length" #description>
              <p v-for="(f, i) in editFindings" :key="i">{{ f.message }} · {{ t('tools.lineNum', { n: f.line }) }}</p>
            </template>
          </UAlert>
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="editOpen = false" />
          <UButton type="submit" form="lib-form" :label="t('common.save')" />
        </div>
      </template>
    </UModal>
  </div>
</template>
