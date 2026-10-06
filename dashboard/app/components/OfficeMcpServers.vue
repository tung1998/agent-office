<script setup lang="ts">
// The MCP servers office manages (ADR-091, ADR-092): agent runs reach them
// through the office gateway /mcp/s/<name>, with office's own token. HTTP
// servers may log in with OAuth (office keeps and refreshes the token);
// stdio servers are processes office runs itself.
interface GwTool { name: string, description?: string, read_only?: boolean }
interface GwOAuth { required: boolean, logged_in: boolean, expired: boolean, expires_at: string | null, client_manual: boolean, client_id: string, has_secret: boolean, dynamic: boolean }
type GwStatus = '' | 'ok' | 'error' | 'needs_login'
interface GwServer {
  id: string, name: string, kind: 'http' | 'stdio', url: string, headers: Record<string, string>,
  command: string, args: string[], env: Record<string, string>, oauth: GwOAuth | null, scope: string, origin: string,
  enabled: boolean, path: string, last_check_at: string | null, last_check_status: GwStatus, last_check_error: string, tools: GwTool[],
  agents: string[], trusted_tools: string[], moved_from: { type: string, label: string, name: string, moved_at: string, backup: boolean } | null
}
interface GwStat { calls: number, errors: number, proposed: number, last_at: string | null }
interface GwAgent { id: string, name: string, project: string, ai: string }
interface GwCall { id: string, tool: string, caller: string, caller_kind: string, status: string, error: string, duration_ms: number, created_at: string }

const { t, dateLocale } = useLang()
const toast = useToast()
// no top-level await: nested async setup under ToolsPanel's :key remount looped
// Suspense (Maximum call stack on the MCP tab); the template copes with no data yet
const { data, refresh } = useLiveFetch<{ servers: GwServer[], stats: Record<string, GwStat> }>('/api/mcp/servers')
const list = ref<GwServer[]>([])
// changed: the machine MCP list shows which servers office has
const emit = defineEmits<{ changed: [] }>()
watch(data, (d) => { list.value = d?.servers ?? []; emit('changed') }, { immediate: true })
defineExpose({ refresh, servers: list, openClientForm: (id: string, msg: string) => openClientForm(id, msg) })
const stat = (s: GwServer) => data.value?.stats?.[s.id]
const fmt = (at: string) => new Date(at).toLocaleString(dateLocale.value, { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })

// ---- which agents get a server, and its tools that write without asking (ADR-093) ----
const { data: agentData } = useFetch<{ agents: GwAgent[] }>('/api/mcp/agents')
const agentItems = computed(() => (agentData.value?.agents ?? []).map(a => ({
  value: a.id,
  label: a.project ? `${a.name} · ${a.project}` : t('tools.gwAssistant')
})))
async function patch(s: GwServer, body: Record<string, unknown>) {
  try {
    put((await $fetch<{ server: GwServer }>(`/api/mcp/servers/${s.id}`, { method: 'PATCH', body })).server)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const setAgents = (s: GwServer, ids: string[]) => patch(s, { agents: ids })
const trust = (s: GwServer, tool: string, on: boolean) =>
  patch(s, { trusted_tools: on ? [...s.trusted_tools, tool] : s.trusted_tools.filter(x => x !== tool) })

// ---- call log ----
const calls = ref<Record<string, GwCall[]>>({})
const keepDays = ref(30)
async function loadCalls(s: GwServer) {
  try {
    const r = await $fetch<{ calls: GwCall[], keep_days: number }>(`/api/mcp/servers/${s.id}/calls`, { query: { limit: 50 } })
    calls.value = { ...calls.value, [s.id]: r.calls }
    keepDays.value = r.keep_days
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const callColor = (st: string) => ({ ok: 'success', error: 'error', proposed: 'warning', denied: 'error' } as const)[st as 'ok'] ?? 'neutral'
const callLabel = (st: string) => ({
  ok: t('tools.gwCallOk'), error: t('tools.gwCallError'), proposed: t('tools.gwCallProposed'), denied: t('tools.gwCallDenied')
})[st as 'ok'] ?? st

// ---- moving in and back (ADR-093) ----
const importOpen = ref(false)
async function putBack(s: GwServer) {
  if (!s.moved_from || !confirm(t('tools.gwConfirmPutBack', { name: s.name, to: s.moved_from.label }))) return
  const del = confirm(t('tools.gwPutBackDelete', { name: s.name }))
  try {
    await $fetch(`/api/mcp/servers/${s.id}/put-back`, { method: 'POST', body: { delete: del } })
    toast.add({ title: t('tools.gwPutBackDone', { name: s.name, to: s.moved_from.label }), color: 'success' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const put = (s: GwServer) => { list.value = list.value.map(x => (x.id === s.id ? s : x)) }
onLiveEvent<GwServer>('mcp.gateway.status', put)

const badge = computed(() => ({
  ok: { color: 'success' as const, icon: 'i-lucide-circle-check', label: t('tools.gwOk') },
  error: { color: 'error' as const, icon: 'i-lucide-circle-x', label: t('tools.gwError') },
  needs_login: { color: 'warning' as const, icon: 'i-lucide-key-round', label: t('tools.gwNeedsLogin') },
  '': { color: 'neutral' as const, icon: 'i-lucide-circle-help', label: t('tools.gwNever') }
}))
const tip = (s: GwServer) => s.last_check_status === 'error' || s.last_check_status === 'needs_login'
  ? s.last_check_error
  : s.last_check_at ? t('tools.mcpCheckedAt', { time: fmt(s.last_check_at) }) : ''
const target = (s: GwServer) => s.kind === 'stdio' ? [s.command, ...s.args].join(' ') : s.url
// a login is offered once office found the server's authorization server
const canLogin = (s: GwServer) => s.kind === 'http' && (s.last_check_status === 'needs_login' || !!s.oauth?.required)
const loggedIn = (s: GwServer) => !!s.oauth?.logged_in && !s.oauth.expired

const checking = ref<Record<string, boolean>>({})
const open = ref<Record<string, boolean>>({})
async function check(s: GwServer) {
  checking.value = { ...checking.value, [s.id]: true }
  try {
    const r = await $fetch<{ server: GwServer }>(`/api/mcp/servers/${s.id}/check`, { method: 'POST' })
    put(r.server)
    if (r.server.last_check_status === 'ok') open.value = { ...open.value, [s.id]: true }
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    checking.value = { ...checking.value, [s.id]: false }
  }
}
async function toggle(s: GwServer, enabled: boolean) {
  try {
    put((await $fetch<{ server: GwServer }>(`/api/mcp/servers/${s.id}`, { method: 'PATCH', body: { enabled } })).server)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function remove(s: GwServer) {
  if (!confirm(t('tools.gwConfirmDelete', { name: s.name }))) return
  try {
    await $fetch(`/api/mcp/servers/${s.id}`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// ---- OAuth login ----
const { connect: startLogin, connecting, pasteFor } = useMcpLogin()
const connect = (s: GwServer) => startLogin(s.id, s.id, openClientForm)
// no dynamic registration: the form, with its client fields open
function openClientForm(id: string, msg: string) {
  const s = list.value.find(x => x.id === id)
  if (!s) return
  openForm(s)
  form.value.clientOpen = true
  formError.value = msg
}
async function logout(s: GwServer) {
  if (!confirm(t('tools.gwConfirmLogout', { name: s.name }))) return
  try {
    put((await $fetch<{ server: GwServer }>(`/api/mcp/servers/${s.id}/oauth/logout`, { method: 'POST' })).server)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

// ---- form ----
interface Row { key: string, value: string, saved?: string }
const formOpen = ref(false)
const editing = ref<GwServer | null>(null)
const blank = () => ({ name: '', kind: 'http' as string, url: '', rows: [] as Row[], command: '', args: '', env: [] as Row[], clientOpen: false, clientId: '', clientSecret: '', hasSecret: false })
const form = ref(blank())
const formError = ref('')
const saving = ref(false)
const kinds = computed(() => [{ label: 'HTTP', value: 'http' }, { label: t('tools.gwStdio'), value: 'stdio' }])
const rowsOf = (m: Record<string, string>) => Object.entries(m).map(([key, saved]) => ({ key, value: '', saved }))
function openForm(s?: GwServer) {
  editing.value = s ?? null
  formError.value = ''
  form.value = s
    ? { ...blank(), name: s.name, kind: s.kind, url: s.url, rows: rowsOf(s.headers), command: s.command, args: s.args.join('\n'), env: rowsOf(s.env),
        clientOpen: !!s.oauth?.client_manual, clientId: s.oauth?.client_id ?? '', hasSecret: !!s.oauth?.has_secret }
    : { ...blank(), rows: [{ key: 'Authorization', value: '' }] }
  formOpen.value = true
}
const sealed = (rows: Row[]) => {
  const out: Record<string, string> = {}
  for (const r of rows) if (r.key.trim() && (r.value.trim() || r.saved)) out[r.key.trim()] = r.value.trim()
  return out
}
async function save() {
  if (saving.value) return
  formError.value = ''
  saving.value = true
  const f = form.value
  const body: Record<string, unknown> = f.kind === 'stdio'
    ? { command: f.command, args: f.args.split('\n').map(a => a.trim()).filter(Boolean), env: sealed(f.env) }
    : { url: f.url, headers: sealed(f.rows), oauth_client_id: f.clientOpen ? f.clientId : '', oauth_client_secret: f.clientOpen ? f.clientSecret : '' }
  try {
    if (editing.value) {
      await $fetch(`/api/mcp/servers/${editing.value.id}`, { method: 'PATCH', body })
    } else {
      await $fetch('/api/mcp/servers', { method: 'POST', body: { ...body, name: f.name, kind: f.kind } })
    }
    formOpen.value = false
    toast.add({ title: t('tools.saved', { name: f.name }), description: t('tools.gwSavedDesc'), color: 'success' })
    await refresh()
  } catch (e) {
    formError.value = apiError(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section id="office-mcp" class="scroll-mt-20 space-y-2">
    <h3 class="flex items-center gap-2 text-sm font-semibold">
      <UIcon name="i-lucide-network" class="text-(--ui-text-muted)" />
      {{ t('tools.gwTitle') }}
      <span class="font-normal text-(--ui-text-muted)">· {{ list.length }}</span>
      <UTooltip :text="t('tools.gwInfo')">
        <UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" />
      </UTooltip>
      <UButton class="ms-auto" size="xs" color="neutral" variant="outline" icon="i-lucide-arrow-right-to-line" :label="t('tools.gwImport')" @click="importOpen = true" />
      <UButton size="xs" icon="i-lucide-plus" :label="t('tools.gwAdd')" @click="openForm()" />
    </h3>
    <p v-if="!list.length" class="rounded-lg border border-dashed border-(--ui-border) px-4 py-3 text-sm text-(--ui-text-muted)">{{ t('tools.gwEmpty') }}</p>
    <div v-else class="divide-y divide-(--ui-border) rounded-lg border border-(--ui-border)">
      <div v-for="s in list" :key="s.id" class="px-4 py-2.5">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <UIcon :name="s.kind === 'stdio' ? 'i-lucide-terminal' : 'i-lucide-plug'" class="size-4 shrink-0 text-primary" />
          <div class="min-w-0 flex-1">
            <p class="flex items-center gap-2 truncate font-mono text-sm font-medium">
              {{ s.name }}
              <UTooltip v-if="s.moved_from" :text="t('tools.gwMovedFromInfo', { name: s.moved_from.name, time: fmt(s.moved_from.moved_at) })">
                <UBadge color="neutral" variant="subtle" size="sm" icon="i-lucide-arrow-right-to-line" :label="s.moved_from.label" class="font-sans" />
              </UTooltip>
              <UBadge v-if="s.agents.length" color="primary" variant="subtle" size="sm" icon="i-lucide-users" :label="t('tools.gwAgentsN', { n: s.agents.length })" class="font-sans" />
            </p>
            <p class="truncate font-mono text-xs text-(--ui-text-muted)">{{ target(s) }}</p>
          </div>
          <div class="flex items-center gap-2">
            <UTooltip v-if="stat(s)?.calls" :text="t('tools.gwStatInfo', { errors: stat(s)!.errors, proposed: stat(s)!.proposed })">
              <UBadge :color="stat(s)!.errors ? 'warning' : 'neutral'" variant="outline" size="sm" icon="i-lucide-activity" :label="t('tools.gwCalls24h', { n: stat(s)!.calls })" />
            </UTooltip>
            <UButton size="xs" color="neutral" variant="subtle" :label="s.tools.length ? t('tools.gwTools', { n: s.tools.length }) : t('tools.gwDetails')"
                     :trailing-icon="open[s.id] ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" @click="open = { ...open, [s.id]: !open[s.id] }" />
            <UButton v-if="canLogin(s) && !loggedIn(s)" size="xs" icon="i-lucide-log-in" :loading="connecting[s.id]"
                     :label="s.oauth?.logged_in ? t('tools.gwReconnect') : t('tools.gwConnect')" @click="connect(s)" />
            <UTooltip :text="tip(s)" :disabled="!tip(s)">
              <UBadge :color="badge[s.last_check_status].color" variant="subtle" size="sm" :icon="badge[s.last_check_status].icon" :label="badge[s.last_check_status].label" />
            </UTooltip>
            <USwitch :model-value="s.enabled" size="sm" :aria-label="t('tools.gwEnabled')" @update:model-value="v => toggle(s, v)" />
            <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-refresh-cw" :aria-label="t('tools.mcpCheck')" :loading="checking[s.id]" @click="check(s)" />
            <UButton v-if="loggedIn(s)" size="xs" color="neutral" variant="ghost" icon="i-lucide-log-out" :aria-label="t('tools.gwLogout')" @click="logout(s)" />
            <UButton v-if="s.moved_from" size="xs" color="neutral" variant="ghost" icon="i-lucide-undo-2" :aria-label="t('tools.gwPutBack')" @click="putBack(s)" />
            <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-pencil" :aria-label="t('common.edit')" @click="openForm(s)" />
            <UButton size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('common.delete')" @click="remove(s)" />
          </div>
        </div>
        <p v-if="s.last_check_status === 'error' || s.last_check_status === 'needs_login'" class="mt-1 text-xs"
           :class="s.last_check_status === 'error' ? 'text-(--ui-error)' : 'text-(--ui-warning)'">
          {{ s.last_check_error }}
        </p>
        <div v-if="open[s.id]" class="mt-2 space-y-3 ps-7">
          <UFormField :label="t('tools.gwAgents')">
            <template #hint>
              <UTooltip :text="t('tools.gwAgentsInfo')"><UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" /></UTooltip>
            </template>
            <USelectMenu :model-value="s.agents" :items="agentItems" value-key="value" multiple size="sm" class="w-full max-w-md"
                         :placeholder="t('tools.gwAllAgents')" @update:model-value="(v: string[]) => setAgents(s, v)" />
          </UFormField>
          <ul v-if="s.tools.length" class="space-y-1">
            <li v-for="tl in s.tools" :key="tl.name" class="flex items-center gap-2 text-xs">
              <code class="shrink-0">mcp__{{ s.name }}__{{ tl.name }}</code>
              <UBadge v-if="tl.read_only" color="neutral" variant="subtle" size="sm" :label="t('tools.gwReadOnly')" />
              <UTooltip v-else :text="t('tools.gwTrustInfo')">
                <USwitch :model-value="s.trusted_tools.includes(tl.name)" size="xs" :label="t('tools.gwTrust')"
                         @update:model-value="v => trust(s, tl.name, v)" />
              </UTooltip>
              <span class="truncate text-(--ui-text-muted)">{{ tl.description }}</span>
            </li>
          </ul>
          <div>
            <UButton size="xs" color="neutral" variant="link" icon="i-lucide-history" class="px-0" :label="t('tools.gwCallLog')" @click="loadCalls(s)" />
            <template v-if="calls[s.id]">
              <p v-if="!calls[s.id]!.length" class="text-xs text-(--ui-text-muted)">{{ t('tools.gwNoCalls', { days: keepDays }) }}</p>
              <ul v-else class="mt-1 space-y-0.5 text-xs">
                <li v-for="c in calls[s.id]" :key="c.id" class="flex items-baseline gap-2">
                  <span class="shrink-0 text-(--ui-text-muted)">{{ fmt(c.created_at) }}</span>
                  <UBadge :color="callColor(c.status)" variant="subtle" size="sm" :label="callLabel(c.status)" />
                  <code class="shrink-0">{{ c.tool }}</code>
                  <span class="shrink-0 text-(--ui-text-muted)">{{ c.caller || t('tools.gwPerson') }}<template v-if="c.caller_kind"> · {{ c.caller_kind }}</template> · {{ c.duration_ms }}ms</span>
                  <span v-if="c.error" class="truncate text-(--ui-error)">{{ c.error }}</span>
                </li>
              </ul>
            </template>
          </div>
        </div>
      </div>
    </div>

    <OfficeMcpImport v-model:open="importOpen" @done="refresh()" />
    <McpLoginPaste v-if="pasteFor" />

    <UModal v-model:open="formOpen" :title="editing ? t('tools.editTitle', { name: editing.name }) : t('tools.gwAddTitle')">
      <template #body>
        <form id="gw-form" class="space-y-4" @submit.prevent="save">
          <UFormField v-if="!editing" :label="t('tools.gwKind')">
            <URadioGroup v-model="form.kind" :items="kinds" orientation="horizontal" />
          </UFormField>
          <UFormField :label="t('common.name')">
            <template #hint>
              <UTooltip :text="t('tools.gwNameInfo')"><UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" /></UTooltip>
            </template>
            <UInput v-model="form.name" :disabled="!!editing" :placeholder="form.kind === 'stdio' ? 'filesystem' : 'context7'" class="w-full font-mono" required />
          </UFormField>

          <template v-if="form.kind === 'stdio'">
            <UFormField :label="t('tools.gwCommand')">
              <template #hint>
                <UTooltip :text="t('tools.gwCommandInfo')"><UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" /></UTooltip>
              </template>
              <UInput v-model="form.command" placeholder="npx" class="w-full font-mono" required />
            </UFormField>
            <UFormField :label="t('tools.gwArgs')" :hint="t('tools.gwArgsHint')">
              <UTextarea v-model="form.args" :rows="3" autoresize placeholder="-y&#10;@modelcontextprotocol/server-filesystem&#10;/Users/me/docs" class="w-full font-mono" />
            </UFormField>
            <UFormField :label="t('tools.gwEnv')">
              <template #hint>
                <UTooltip :text="t('tools.gwEnvInfo')"><UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" /></UTooltip>
              </template>
              <div class="space-y-2">
                <div v-for="(r, i) in form.env" :key="i" class="flex gap-2">
                  <UInput v-model="r.key" placeholder="API_KEY" class="w-40 font-mono" />
                  <UInput v-model="r.value" type="password" autocomplete="off" :placeholder="r.saved ? t('tools.gwKeepSaved', { hint: r.saved }) : '…'" class="flex-1 font-mono" />
                  <UButton color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('common.delete')" @click="form.env.splice(i, 1)" />
                </div>
                <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-plus" :label="t('tools.gwAddEnv')" @click="form.env.push({ key: '', value: '' })" />
              </div>
            </UFormField>
          </template>

          <template v-else>
            <UFormField label="URL">
              <UInput v-model="form.url" placeholder="https://mcp.context7.com/mcp" class="w-full font-mono" required />
            </UFormField>
            <UFormField :label="t('tools.gwHeaders')">
              <template #hint>
                <UTooltip :text="t('tools.gwHeadersInfo')"><UIcon name="i-lucide-info" class="text-(--ui-text-dimmed)" /></UTooltip>
              </template>
              <div class="space-y-2">
                <div v-for="(r, i) in form.rows" :key="i" class="flex gap-2">
                  <UInput v-model="r.key" placeholder="Authorization" class="w-40 font-mono" />
                  <UInput v-model="r.value" type="password" autocomplete="off" :placeholder="r.saved ? t('tools.gwKeepSaved', { hint: r.saved }) : 'Bearer …'" class="flex-1 font-mono" />
                  <UButton color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('common.delete')" @click="form.rows.splice(i, 1)" />
                </div>
                <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-plus" :label="t('tools.gwAddHeader')" @click="form.rows.push({ key: '', value: '' })" />
              </div>
            </UFormField>
            <div class="space-y-2">
              <UCheckbox v-model="form.clientOpen" :label="t('tools.gwOwnClient')">
                <template #description>
                  <span class="text-xs text-(--ui-text-muted)">{{ t('tools.gwOwnClientInfo') }}</span>
                </template>
              </UCheckbox>
              <div v-if="form.clientOpen" class="flex gap-2">
                <UInput v-model="form.clientId" placeholder="client_id" class="flex-1 font-mono" />
                <UInput v-model="form.clientSecret" type="password" autocomplete="off" :placeholder="form.hasSecret ? t('tools.gwKeepSecret') : 'client_secret'" class="flex-1 font-mono" />
              </div>
            </div>
          </template>
          <UAlert v-if="formError" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="formError" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="formOpen = false" />
          <UButton type="submit" form="gw-form" :label="t('common.save')" :loading="saving" :disabled="saving" />
        </div>
      </template>
    </UModal>
  </section>
</template>
