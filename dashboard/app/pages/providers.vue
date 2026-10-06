<script setup lang="ts">
const toast = useToast()
const route = useRoute()
const { isAdmin } = useAuth()
const { t } = useLang()
// Set when another page (project setup) sent the user here to connect AI first.
const next = computed(() => {
  const n = route.query.next
  return typeof n === 'string' && n.startsWith('/') && !n.startsWith('//') ? n : ''
})

const _f1 = useLiveFetch<{ providers: Provider[] }>('/api/providers')
const { data, refresh } = _f1
const _f2 = useLiveFetch<{ kinds: ProviderKind[], presets: ProviderPreset[] }>('/api/provider-kinds')
const { data: kindsData } = _f2
await Promise.all([_f1, _f2]) // started together: one round trip, not 2 (a phone over a VPN)
const { data: statsData, refresh: refreshStats } = useLiveFetch<{ days: number, providers: ProviderStat[], today: { calls: number, errors: number, tokens: number, cost_usd: number } }>('/api/providers/stats', { query: { days: 7 }, lazy: true })
const providers = computed(() => data.value?.providers ?? [])
const kinds = computed(() => kindsData.value?.kinds ?? [])
const presets = computed(() => kindsData.value?.presets ?? [])
const kindOf = (k: string) => kinds.value.find(x => x.kind === k)
const presetOf = (id: string) => presets.value.find(p => p.id === id)
const statOf = (id: string) => statsData.value?.providers.find(s => s.provider_id === id)
const week = computed(() => (statsData.value?.providers ?? []).reduce((a, s) => ({ calls: a.calls + s.calls, cost: a.cost + s.cost_usd }), { calls: 0, cost: 0 }))
const groupLabel = computed<Record<ProviderPreset['group'], string>>(() => ({ gateway: t('prov.groupGateway'), global: t('prov.groupGlobal'), china: t('prov.groupChina'), local: t('prov.groupLocal') }))
const presetGroups = computed(() => (['gateway', 'global', 'china', 'local'] as const)
  .map(g => ({ group: g, items: presets.value.filter(p => p.group === g) })).filter(g => g.items.length))

// ---- form ----
const formOpen = ref(false)
const editing = ref<Provider | null>(null)
const form = reactive({
  name: '', kind: 'anthropic', preset: '', base_url: '', api_key: '', api_key_env: '', keyMode: 'paste' as 'paste' | 'env',
  tier_models: { strong: '', balanced: '', fast: '' } as Record<ModelTier, string>
})
const formError = ref('')
const saving = ref(false)
const selectedKind = computed(() => kindOf(form.kind))
const selectedPreset = computed(() => presetOf(form.preset))
const needsKey = computed(() => selectedPreset.value ? selectedPreset.value.need_key : !!selectedKind.value?.needs_key)
const commonKinds = computed(() => kinds.value.filter(k => k.common))
const advancedOpen = ref(false)
const cliTool = computed(() => ({ claude_cli: 'claude', codex_cli: 'codex' } as Record<string, 'claude' | 'codex'>)[form.kind])
const cliReady = ref(true)
watch(cliTool, (v) => { if (!v) cliReady.value = true })
const norm = (u: string) => u.trim().replace(/\/+$/, '')
const urlChangedWithStoredKey = computed(() => !!editing.value?.has_api_key && form.keyMode === 'paste' && !form.api_key
  && norm(form.base_url) !== norm(editing.value.base_url))
const keyUrl = computed(() => selectedPreset.value?.key_url ?? ({ anthropic: 'https://console.anthropic.com', openai: 'https://platform.openai.com/api-keys' } as Record<string, string>)[form.kind] ?? '')
const kindIconOf = (k: string) => ({
  claude_cli: 'i-lucide-terminal', codex_cli: 'i-lucide-terminal', anthropic: 'i-lucide-key-round',
  openai: 'i-lucide-key-round', openai_compatible: 'i-lucide-server'
} as Record<string, string>)[k] ?? 'i-lucide-plug'

// Pick sensible defaults for a kind: its label as name, and the env var when one is already set.
function applyKindDefaults(k: string) {
  const info = kindOf(k)
  form.name = info?.label ?? ''
  form.base_url = ''
  form.api_key = ''
  form.api_key_env = info?.detected?.env_key ?? ''
  form.keyMode = info?.detected?.env_key ? 'env' : 'paste'
  form.tier_models = { strong: '', balanced: '', fast: '', ...info?.tier_models }
}

// choose a connection type: a kind, or a third-party preset (an OpenAI-compatible API)
function choose(kind: string, preset = '') {
  form.kind = kind
  applyKindDefaults(kind)
  form.preset = preset
  const pr = presetOf(preset)
  if (pr) {
    form.name = pr.name
    form.base_url = pr.base_url
  }
}

function openCreate() {
  editing.value = null
  advancedOpen.value = false
  // Prefer what already works on this machine: an installed Claude Code, then a key in the environment.
  const preferred = commonKinds.value.find(k => k.detected?.installed) ?? commonKinds.value.find(k => k.detected?.env_key) ?? commonKinds.value[0]
  choose(preferred?.kind ?? 'claude_cli')
  formError.value = ''
  formOpen.value = true
}

function openEdit(p: Provider) {
  editing.value = p
  Object.assign(form, {
    name: p.name, kind: p.kind, preset: p.preset, base_url: p.base_url, api_key: '', api_key_env: p.api_key_env,
    keyMode: p.api_key_env ? 'env' : 'paste'
  })
  form.tier_models = { strong: '', balanced: '', fast: '', ...p.tier_models }
  formError.value = ''
  advancedOpen.value = false
  formOpen.value = true
}

async function save() {
  formError.value = ''
  saving.value = true
  const body: Record<string, unknown> = {
    name: form.name,
    kind: form.kind,
    preset: form.kind === 'openai_compatible' ? form.preset : '',
    base_url: form.base_url,
    api_key_env: form.keyMode === 'env' ? form.api_key_env : '',
    tier_models: Object.fromEntries(Object.entries(form.tier_models).filter(([, v]) => v))
  }
  // Only send the key when typed: omitting it keeps the stored one.
  if (form.keyMode === 'paste' && form.api_key) body.api_key = form.api_key
  if (form.keyMode === 'env' && editing.value?.has_api_key) body.api_key = ''
  try {
    const res = editing.value
      ? await $fetch<{ provider: Provider }>(`/api/providers/${editing.value.id}`, { method: 'PATCH', body })
      : await $fetch<{ provider: Provider }>('/api/providers', { method: 'POST', body })
    formOpen.value = false
    await refresh()
    toast.add({ title: editing.value ? t('prov.savedConnection') : t('prov.addedConnection'), description: t('prov.checkingConnection'), color: 'success' })
    await runTest(res.provider)
  } catch (e) {
    formError.value = apiError(e)
  } finally {
    saving.value = false
  }
}

// ---- test ----
const testing = ref<string | null>(null)
const testOpen = ref(false)
const testTarget = ref<Provider | null>(null)
const testPrompt = ref(t('prov.defaultTestPrompt'))
const testModel = ref('')
const testResult = ref<{ ok: boolean, detail: string, models: string[], needs_login?: boolean, cli_tool?: 'claude' | 'codex', response?: { text: string, model: string, input_tokens: number, output_tokens: number, cost_usd?: number, duration_ms: number } } | null>(null)

async function runTest(p: Provider, prompt = '') {
  testing.value = p.id
  try {
    const res = await $fetch<typeof testResult.value>(`/api/providers/${p.id}/test`, {
      method: 'POST', body: { prompt, model: prompt ? testModel.value : '' }
    })
    if (!prompt && res!.ok && next.value) {
      toast.add({ title: t('prov.savedGood', { name: p.name }), description: t('prov.savedGoodDesc'), color: 'success' })
      await navigateTo(next.value)
      return
    }
    if (prompt) {
      testResult.value = res
    } else if (res!.needs_login) {
      // the tool is installed, the account is signed out: sign in from here
      testTarget.value = p
      testModel.value = p.tier_models.balanced || ''
      testResult.value = res
      testOpen.value = true
    } else {
      toast.add({ title: res!.ok ? t('prov.savedGood', { name: p.name }) : t('prov.testError', { name: p.name }), description: res!.detail, color: res!.ok ? 'success' : 'error' })
    }
    await refresh()
    refreshStats()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    testing.value = null
  }
}

function openPromptTest(p: Provider) {
  testTarget.value = p
  testModel.value = p.tier_models.balanced || ''
  testResult.value = null
  testOpen.value = true
}

// After signing a CLI back in, run the test again so the provider turns green
// without the person hunting for the button.
function onCliReady(ok: boolean) {
  if (!ok || !testResult.value?.needs_login || !testTarget.value) return
  testResult.value = null
  runTest(testTarget.value, testPrompt.value)
}

const settingDefault = ref<string | null>(null)
async function setDefault(p: Provider) {
  if (settingDefault.value) return
  settingDefault.value = p.id
  try {
    await $fetch(`/api/providers/${p.id}/default`, { method: 'POST' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    settingDefault.value = null
  }
}

async function remove(p: Provider) {
  if (!confirm(t('prov.confirmDelete', { name: p.name }))) return
  try {
    await $fetch(`/api/providers/${p.id}`, { method: 'DELETE' })
    await refresh()
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}

function menu(p: Provider) {
  return [
    [
      { label: t('prov.sendTestPrompt'), icon: 'i-lucide-message-square', onSelect: () => openPromptTest(p) },
      { label: t('prov.edit'), icon: 'i-lucide-pencil', onSelect: () => openEdit(p) },
      { label: t('prov.setDefault'), icon: 'i-lucide-star', disabled: p.is_default || !!settingDefault.value, onSelect: () => setDefault(p) }
    ],
    [{ label: t('prov.delete'), icon: 'i-lucide-trash', color: 'error' as const, onSelect: () => remove(p) }]
  ]
}

// ---- display ----
const avatarOf = (p: Provider) => presetOf(p.preset)?.name ?? kindOf(p.kind)?.label ?? p.name
const palette = ['bg-violet-500', 'bg-sky-500', 'bg-emerald-500', 'bg-amber-500', 'bg-rose-500', 'bg-indigo-500', 'bg-teal-500', 'bg-orange-500']
const avatarColor = (key: string) => palette[[...key].reduce((a, c) => a + c.charCodeAt(0), 0) % palette.length]
const num = (n: number) => n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e3 ? `${(n / 1e3).toFixed(1)}K` : String(n)
const usd = (v: number) => v >= 100 ? `$${v.toFixed(0)}` : v >= 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(3)}`
function ago(last: string | null) {
  if (!last) return t('prov.neverUsed')
  const s = (Date.now() - new Date(last).getTime()) / 1000
  return s < 60 ? t('prov.justNow') : s < 3600 ? t('prov.minutesAgo', { n: Math.floor(s / 60) }) : s < 86400 ? t('prov.hoursAgo', { n: Math.floor(s / 3600) }) : t('prov.daysAgo', { n: Math.floor(s / 86400) })
}
const maxDay = (st?: ProviderStat) => Math.max(1, ...(st?.days.map(d => d.calls) ?? [0]))

const statusColor = (s: string) => (s === 'ok' ? 'success' : s === 'error' ? 'error' : 'neutral')
const statusText = (s: string) => (s === 'ok' ? t('prov.statusOk') : s === 'error' ? t('prov.statusError') : t('prov.statusUnknown'))
</script>

<template>
  <PageShell :title="t('prov.title')">
    <template #actions>
      <UButton v-if="isAdmin" icon="i-lucide-plus" :label="t('prov.add')" @click="openCreate" />
    </template>

    <div class="space-y-4">
      <UAlert
        v-if="next" color="primary" variant="subtle" icon="i-lucide-sparkles"
        :title="t('prov.connectBannerTitle')"
        :description="t('prov.connectBannerDesc')"
        :actions="providers.some(p => p.status === 'ok') ? [{ label: t('prov.continueSetup'), to: next, icon: 'i-lucide-arrow-right' }] : []"
      />


      <div v-if="!providers.length" class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
        <UIcon name="i-lucide-plug" class="mx-auto size-8 text-(--ui-text-dimmed)" />
        <p class="mt-2 font-medium">{{ t('prov.emptyTitle') }}</p>
        <p class="text-sm text-(--ui-text-muted)">{{ t('prov.emptyDesc') }}</p>
        <UButton v-if="isAdmin" class="mt-4" icon="i-lucide-plus" :label="t('prov.add')" @click="openCreate" />
      </div>

      <!-- today / 7 days -->
      <!-- the office-wide limit and the model prices (each project's budget is on the project) -->
      <OfficeBudget v-if="isAdmin" />
      <LimitAlert v-if="isAdmin" />
      <div v-if="providers.length" class="grid grid-cols-2 overflow-hidden rounded-lg border border-(--ui-border) md:grid-cols-4">
        <div class="p-4">
          <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('prov.callsToday') }}</p>
          <p class="mt-1 font-mono text-2xl font-semibold">{{ num(statsData?.today.calls ?? 0) }}</p>
        </div>
        <div class="border-s border-(--ui-border) p-4">
          <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('prov.tokensToday') }}</p>
          <p class="mt-1 font-mono text-2xl font-semibold">{{ num(statsData?.today.tokens ?? 0) }}</p>
        </div>
        <div class="border-t border-(--ui-border) p-4 md:border-s md:border-t-0">
          <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('prov.costToday') }}</p>
          <p class="mt-1 font-mono text-2xl font-semibold">{{ usd(statsData?.today.cost_usd ?? 0) }}</p>
        </div>
        <NuxtLink to="/" class="border-s border-t border-(--ui-border) p-4 transition hover:bg-(--ui-bg-elevated) md:border-t-0">
          <p class="text-xs font-medium tracking-wide text-(--ui-text-muted) uppercase">{{ t('prov.sevenDays') }}</p>
          <p class="mt-1 font-mono text-2xl font-semibold">{{ usd(week.cost) }} <span class="text-sm font-normal text-(--ui-text-muted)">· {{ num(week.calls) }} {{ t('prov.callsUnit') }}</span></p>
        </NuxtLink>
      </div>

      <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <div v-for="p in providers" :key="p.id" class="rounded-lg border border-(--ui-border) p-4" :class="{ 'opacity-60': !p.enabled }">
          <div class="flex items-start gap-3">
            <span class="grid size-9 shrink-0 place-items-center rounded-lg text-sm font-semibold text-white" :class="avatarColor(avatarOf(p))">
              <UIcon v-if="kindOf(p.kind)?.is_cli" name="i-lucide-terminal" class="size-4" />
              <template v-else>{{ avatarOf(p).slice(0, 1).toUpperCase() }}</template>
            </span>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <p class="min-w-0 truncate font-semibold">{{ p.name }}</p>
                <UBadge v-if="p.is_default" :label="t('prov.default')" icon="i-lucide-star" size="sm" variant="subtle" />
              </div>
              <p class="truncate text-xs text-(--ui-text-muted)">
                {{ presetOf(p.preset)?.name ?? kindOf(p.kind)?.label ?? p.kind }}
                <template v-if="p.api_key_env"> · {{ t('prov.keyFromEnvPrefix') }} <code>{{ p.api_key_env }}</code></template>
                <template v-else-if="p.has_api_key"> · {{ t('prov.keyHintPrefix') }} <code>{{ p.api_key_hint }}</code></template>
              </p>
            </div>
            <div class="flex shrink-0 items-center gap-1">
              <UBadge :label="statusText(p.status)" :color="statusColor(p.status)" variant="subtle" size="sm" />
              <UButton
                v-if="isAdmin" icon="i-lucide-refresh-cw" color="neutral" variant="ghost" size="xs"
                :loading="testing === p.id" :aria-label="t('prov.testConnection')" @click="runTest(p)"
              />
              <UDropdownMenu v-if="isAdmin" :items="menu(p)">
                <UButton icon="i-lucide-ellipsis" color="neutral" variant="ghost" size="xs" :aria-label="t('prov.actions')" />
              </UDropdownMenu>
            </div>
          </div>

          <!-- 7-day stats -->
          <div class="mt-3 flex items-end gap-4">
            <div class="grid flex-1 grid-cols-2 gap-2 text-xs sm:grid-cols-4">
              <div>
                <p class="text-(--ui-text-muted)">{{ t('prov.calls') }}</p>
                <p class="font-mono text-sm font-medium tabular-nums">{{ num(statOf(p.id)?.calls ?? 0) }}</p>
              </div>
              <div>
                <p class="text-(--ui-text-muted)">{{ t('prov.tokens') }}</p>
                <p class="font-mono text-sm font-medium tabular-nums">{{ num((statOf(p.id)?.input_tokens ?? 0) + (statOf(p.id)?.output_tokens ?? 0)) }}</p>
              </div>
              <div>
                <p class="text-(--ui-text-muted)">{{ t('prov.cost') }}</p>
                <p class="font-mono text-sm font-medium tabular-nums">{{ usd(statOf(p.id)?.cost_usd ?? 0) }}</p>
              </div>
              <div>
                <p class="text-(--ui-text-muted)">{{ t('prov.errorLatency') }}</p>
                <p class="font-mono text-sm font-medium tabular-nums">
                  <span :class="statOf(p.id)?.errors ? 'text-(--ui-error)' : ''">{{ statOf(p.id)?.calls ? Math.round(((statOf(p.id)?.errors ?? 0) / statOf(p.id)!.calls) * 100) : 0 }}%</span>
                  <span class="text-(--ui-text-muted)"> · {{ statOf(p.id)?.avg_ms ? `${(statOf(p.id)!.avg_ms / 1000).toFixed(1)}s` : '—' }}</span>
                </p>
              </div>
            </div>
            <div class="flex h-8 items-end gap-0.5" :title="t('prov.callsSevenDaysTitle')">
              <span
                v-for="d in statOf(p.id)?.days ?? []" :key="d.day" class="w-2 rounded-sm bg-(--ui-primary)/70"
                :style="{ height: `${Math.max(8, (d.calls / maxDay(statOf(p.id))) * 100)}%` }" :class="{ 'bg-(--ui-bg-elevated)!': !d.calls }"
                :title="t('prov.dayTooltip', { day: d.day, calls: d.calls, cost: usd(d.cost_usd) })"
              />
            </div>
          </div>
          <!-- subscription usage windows (Claude Code), as of its latest run -->
          <UsageLimits :provider-id="p.id" class="mt-3 border-t border-(--ui-border) pt-3" />

          <p class="mt-2 flex flex-wrap gap-x-3 text-xs text-(--ui-text-muted)">
            <span>{{ ago(statOf(p.id)?.last_used_at ?? null) }}</span>
            <span v-if="statOf(p.id)?.top_model" class="truncate">{{ t('prov.topModelPrefix') }} <code>{{ statOf(p.id)?.top_model }}</code></span>
            <span v-else-if="p.tier_models.balanced" class="truncate">{{ t('prov.modelPrefix') }} <code>{{ p.tier_models.balanced }}</code></span>
          </p>
          <p v-if="p.status === 'error' && p.status_detail" class="mt-1 truncate text-xs text-(--ui-error)" :title="p.status_detail">{{ p.status_detail }}</p>
          <!-- p.status_detail is a backend-supplied message, kept as-is -->

        </div>
      </div>
    </div>

    <!-- create / edit -->
    <UModal v-model:open="formOpen" :title="editing ? t('prov.editTitle', { name: editing.name }) : t('prov.addTitle')" :ui="{ content: 'max-w-2xl' }">
      <template #body>
        <form id="provider-form" class="space-y-5" @submit.prevent="save">
          <!-- kind: main accounts, then third-party APIs -->
          <div v-if="!editing" class="space-y-3">
            <div class="flex flex-wrap items-center gap-2">
              <button
                v-for="k in kinds.filter(k => k.kind !== 'openai_compatible')" :key="k.kind" type="button"
                class="flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm transition"
                :class="form.kind === k.kind && !form.preset ? 'border-(--ui-primary) bg-(--ui-primary)/10 text-(--ui-primary)' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
                @click="choose(k.kind)"
              >
                <UIcon :name="kindIconOf(k.kind)" class="size-4" />
                {{ k.label }}
                <span v-if="k.detected?.installed || k.detected?.env_key" class="size-1.5 rounded-full bg-(--ui-success)" :title="t('prov.detectedOnMachine')" />
              </button>
            </div>
            <details class="rounded-lg border border-(--ui-border)" :open="form.kind === 'openai_compatible'">
              <summary class="cursor-pointer px-3 py-2 text-sm font-medium">{{ t('prov.otherProviders') }} <span class="font-normal text-(--ui-text-muted)">· {{ t('prov.otherProvidersHint') }}</span></summary>
              <div class="space-y-3 border-t border-(--ui-border) p-3">
                <div v-for="g in presetGroups" :key="g.group">
                  <p class="mb-1.5 text-xs text-(--ui-text-muted)">{{ groupLabel[g.group] }}</p>
                  <div class="flex flex-wrap gap-1.5">
                    <button
                      v-for="pr in g.items" :key="pr.id" type="button" :title="pr.note"
                      class="flex items-center gap-1.5 rounded-md border px-2 py-1 text-sm transition"
                      :class="form.preset === pr.id ? 'border-(--ui-primary) bg-(--ui-primary)/10 text-(--ui-primary)' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
                      @click="choose('openai_compatible', pr.id)"
                    >
                      <span class="grid size-4 place-items-center rounded text-[10px] font-bold text-white" :class="avatarColor(pr.name)">{{ pr.name.slice(0, 1) }}</span>
                      {{ pr.name }}
                    </button>
                  </div>
                </div>
                <button
                  type="button" class="flex items-center gap-1.5 rounded-md border px-2 py-1 text-sm transition"
                  :class="form.kind === 'openai_compatible' && !form.preset ? 'border-(--ui-primary) bg-(--ui-primary)/10 text-(--ui-primary)' : 'border-dashed border-(--ui-border) hover:border-(--ui-border-accented)'"
                  @click="choose('openai_compatible')"
                >
                  <UIcon name="i-lucide-server" class="size-4" /> {{ t('prov.customEndpoint') }}
                </button>
              </div>
            </details>
            <p v-if="selectedPreset?.note" class="text-xs text-(--ui-text-muted)">{{ t('prov.presetNote', { name: selectedPreset.name, note: selectedPreset.note }) }}</p>
          </div>
          <div v-else class="flex items-center gap-2 text-sm">
            <UIcon :name="kindIconOf(form.kind)" class="size-5 text-primary" />
            <span class="font-medium">{{ selectedPreset?.name ?? selectedKind?.label }}</span>
          </div>

          <CliSetup v-if="cliTool" :key="cliTool" :tool="cliTool" @ready="(v: boolean) => cliReady = v" />

          <UFormField :label="t('prov.displayName')" required>
            <UInput v-model="form.name" class="w-full" />
          </UFormField>

          <!-- endpoint for compatible APIs is not optional, so it is not hidden -->
          <UFormField v-if="form.kind === 'openai_compatible' && !form.preset" :label="t('prov.apiUrl')" required :help="t('prov.apiUrlHelp')">
            <UInput v-model="form.base_url" :placeholder="selectedKind?.base_url_hint" class="w-full font-mono" />
          </UFormField>

          <!-- API key -->
          <template v-if="!selectedKind?.is_cli && (needsKey || !form.preset)">
            <UFormField :label="t('prov.apiKey')" :required="needsKey">
              <UTabs
                v-model="form.keyMode"
                :items="[{ label: t('prov.keyModePaste'), value: 'paste' }, { label: t('prov.keyModeEnv'), value: 'env' }]"
                :content="false" size="xs" class="mb-2"
              />
              <UInput
                v-if="form.keyMode === 'paste'" v-model="form.api_key" type="password" autocomplete="off" class="w-full"
                :placeholder="editing?.has_api_key ? t('prov.keyPlaceholderSaved', { hint: editing.api_key_hint }) : t('prov.keyPlaceholder')"
              />
              <UInput v-else v-model="form.api_key_env" :placeholder="selectedPreset?.key_env ?? 'ANTHROPIC_API_KEY'" class="w-full font-mono" />
              <p v-if="urlChangedWithStoredKey" class="mt-1 text-xs text-(--ui-warning)">
                {{ t('prov.urlChangedWarning') }}
              </p>
              <template #help>
                <span v-if="form.keyMode === 'paste'">{{ t('prov.keyHelpPaste') }}</span>
                <span v-else>{{ t('prov.keyHelpEnv') }}</span>
                <template v-if="keyUrl"> {{ t('prov.getKeyAtPrefix') }} <a :href="keyUrl" target="_blank" class="underline">{{ keyUrl.replace('https://', '') }}</a>.</template>
              </template>
            </UFormField>
          </template>

          <!-- advanced -->
          <UCollapsible v-model:open="advancedOpen">
            <UButton
              color="neutral" variant="link" size="sm" class="px-0"
              :icon="advancedOpen ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" :label="t('prov.advanced')"
            />
            <template #content>
              <div class="mt-3 space-y-4 rounded-lg border border-(--ui-border) p-4">
                <UFormField
                  v-if="selectedKind?.is_cli" :label="t('prov.cliPathLabel')"
                  :help="t('prov.cliPathHelp', { cmd: selectedKind.base_url_hint })"
                >
                  <UInput v-model="form.base_url" :placeholder="selectedKind?.base_url_hint" class="w-full font-mono" />
                </UFormField>
                <UFormField
                  v-else-if="form.kind !== 'openai_compatible' || form.preset" :label="t('prov.apiUrl')"
                  :help="t('prov.apiUrlAdvancedHelp')"
                >
                  <UInput v-model="form.base_url" :placeholder="selectedKind?.base_url_hint" class="w-full font-mono" />
                </UFormField>

                <div>
                  <p class="text-sm font-medium">{{ t('prov.tierModels') }}</p>
                  <p class="mt-0.5 text-xs text-(--ui-text-muted)">
                    {{ t('prov.tierModelsHelp', { strong: modelTierLabel.strong, balanced: modelTierLabel.balanced, fast: modelTierLabel.fast }) }}
                  </p>
                  <div class="mt-2 grid gap-3 sm:grid-cols-3">
                    <UFormField v-for="tier in (['strong', 'balanced', 'fast'] as const)" :key="tier" :label="modelTierLabel[tier]">
                      <UInput v-model="form.tier_models[tier]" :list="`models-${tier}`" class="w-full font-mono" size="sm" />
                      <datalist :id="`models-${tier}`">
                        <option v-for="m in editing?.models ?? []" :key="m" :value="m" />
                      </datalist>
                    </UFormField>
                  </div>
                </div>
              </div>
            </template>
          </UCollapsible>

          <UAlert v-if="formError" color="error" variant="subtle" :description="formError" />
        </form>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.cancel')" @click="formOpen = false" />
          <UButton type="submit" form="provider-form" :loading="saving" :disabled="!cliReady" :label="editing ? t('prov.saveAndTest') : t('prov.addAndTest')" />
        </div>
      </template>
    </UModal>

    <!-- prompt test -->
    <UModal v-model:open="testOpen" :title="t('prov.testTitle', { name: testTarget?.name ?? '' })" :ui="{ content: 'max-w-xl' }">
      <template #body>
        <div class="space-y-4">
          <UFormField :label="t('prov.model2')">
            <UInput v-model="testModel" list="test-models" class="w-full font-mono" />
            <datalist id="test-models">
              <option v-for="m in testTarget?.models ?? []" :key="m" :value="m" />
            </datalist>
          </UFormField>
          <UFormField :label="t('prov.prompt')">
            <UTextarea v-model="testPrompt" :rows="3" class="w-full" />
          </UFormField>
          <UAlert v-if="testResult && !testResult.ok" color="error" variant="subtle" :description="testResult.detail" />
          <CliSetup
            v-if="testResult?.needs_login && testResult.cli_tool" :key="testResult.cli_tool"
            :tool="testResult.cli_tool" @ready="onCliReady"
          />
          <div v-if="testResult?.response" class="rounded-md bg-(--ui-bg-muted) p-3 text-sm">
            <p class="whitespace-pre-wrap">{{ testResult.response.text }}</p>
            <p class="mt-2 text-xs text-(--ui-text-muted)">
              {{ testResult.response.model }} · {{ testResult.response.input_tokens }} in / {{ testResult.response.output_tokens }} out
              · {{ testResult.response.duration_ms }} ms
              <span v-if="testResult.response.cost_usd"> · ${{ testResult.response.cost_usd.toFixed(4) }}</span>
            </p>
          </div>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="ghost" :label="t('common.close')" @click="testOpen = false" />
          <UButton icon="i-lucide-send" :label="t('prov.send')" :loading="testing === testTarget?.id" @click="testTarget && runTest(testTarget, testPrompt)" />
        </div>
      </template>
    </UModal>
  </PageShell>
</template>
