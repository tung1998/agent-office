<script setup lang="ts">
// Where customers' messages come from (ADR-173, ADR-174): an API, a web
// form, a bot of the project or Crisp; each answered by an automation (an
// agent or a workflow), sent at once (auto) or after a person approves.
// Replies go back on the bot or Crisp, or by email for a form. The token
// shows once.
const props = defineProps<{ projectId: string }>()
const { t } = useLang()
const toast = useToast()
const { isAdmin } = useAuth()
const base = computed(() => `/api/projects/${props.projectId}/support/sources`)
const { data, error } = useLiveFetch<{ sources: SupportSource[] }>(() => base.value, { lazy: true })
const { data: autos } = useLiveFetch<{ automations: Automation[] }>(() => `/api/projects/${props.projectId}/automations`, { lazy: true })
const { data: bots } = useLiveFetch<{ channels: { id: string, name: string, kind: string, bot_name?: string }[] }>(() => `/api/projects/${props.projectId}/channels`, { lazy: true })
const answerers = computed(() => (autos.value?.automations ?? []).filter(a => a.action === 'chat' || a.action === 'workflow'))
const autoName = (id: string) => answerers.value.find(a => a.id === id)?.name ?? t('sup.noAutomation')
const modes = computed(() => [{ label: t('sup.mode.approve'), value: 'approve' }, { label: t('sup.mode.auto'), value: 'auto' }])
const kinds = computed(() => (['api', 'form', 'bot', 'crisp'] as const).map(k => ({ label: t(`sup.kind.${k}` as 'sup.kind.api'), value: k })))
const replyWays = computed(() => [{ label: t('sup.via.none'), value: 'none' }, { label: t('sup.via.hook'), value: 'hook' }, { label: t('sup.via.email'), value: 'email' }])
const kindIcon: Record<string, string> = { api: 'i-lucide-webhook', form: 'i-lucide-file-input', bot: 'i-lucide-bot', crisp: 'i-lucide-message-circle' }
const hookURL = (id: string) => `${window.location.origin}/hooks/support/${id}`

const open = ref(false)
const editing = ref<SupportSource | null>(null)
const blank = () => ({ kind: 'api' as SupportSource['kind'], name: '', automation_id: answerers.value[0]?.id ?? '', mode: 'approve' as SupportSource['mode'], rate_per_min: 30,
  channel_id: '', website_id: '', identifier: '', reply_via: 'none', smtp_host: '', smtp_port: '587', smtp_user: '', smtp_from: '', secret: '' })
const form = reactive(blank())
function openForm(s?: SupportSource) {
  editing.value = s ?? null
  const c = (s?.config ?? {}) as Record<string, string>
  Object.assign(form, blank(), s
    ? { kind: s.kind, name: s.name, automation_id: s.automation_id, mode: s.mode, rate_per_min: s.rate_per_min, channel_id: c.channel_id ?? '',
        website_id: c.website_id ?? '', identifier: c.identifier ?? '', reply_via: c.reply_via || 'none', smtp_host: c.smtp_host ?? '',
        smtp_port: c.smtp_port || '587', smtp_user: c.smtp_user ?? '', smtp_from: c.smtp_from ?? '' }
    : {})
  open.value = true
}
function body() {
  const config: Record<string, string> = {}
  if (form.kind === 'bot') config.channel_id = form.channel_id
  if (form.kind === 'crisp') Object.assign(config, { website_id: form.website_id, identifier: form.identifier })
  if (form.kind === 'api' || form.kind === 'form') {
    config.reply_via = form.reply_via
    if (form.reply_via === 'email') Object.assign(config, { smtp_host: form.smtp_host, smtp_port: form.smtp_port, smtp_user: form.smtp_user, smtp_from: form.smtp_from })
  }
  return { kind: form.kind, name: form.name, automation_id: form.automation_id, mode: form.mode, rate_per_min: Number(form.rate_per_min), config,
    ...(form.secret ? { secret: form.secret } : {}) }
}
const needsSecret = computed(() => form.kind === 'crisp' || (form.kind !== 'bot' && form.reply_via === 'email'))
const saving = ref(false)
const secret = ref<{ id: string, kind: string, token: string } | null>(null)
async function save() {
  if (saving.value) return
  saving.value = true
  try {
    if (editing.value) {
      const { kind: _, ...rest } = body()
      await $fetch(`${base.value}/${editing.value.id}`, { method: 'PATCH', body: rest })
    } else {
      const r = await $fetch<{ source: SupportSource, token: string }>(base.value, { method: 'POST', body: body() })
      if (r.source.kind !== 'bot') secret.value = { id: r.source.id, kind: r.source.kind, token: r.token }
    }
    open.value = false
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
async function toggle(s: SupportSource, enabled: boolean) {
  try {
    await $fetch(`${base.value}/${s.id}`, { method: 'PATCH', body: { enabled } })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function rotate(s: SupportSource) {
  if (!confirm(t('sup.rotateConfirm'))) return
  try {
    const r = await $fetch<{ token: string }>(`${base.value}/${s.id}/token`, { method: 'POST' })
    secret.value = { id: s.id, kind: s.kind, token: r.token }
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
// how to send to it: Crisp and a form carry the token in the URL
const rows = computed(() => {
  const v = secret.value
  if (!v) return []
  const url = hookURL(v.id)
  const withToken = `${url}?token=${encodeURIComponent(v.token)}`
  if (v.kind === 'crisp') return [{ label: t('sup.crispHook'), text: withToken }]
  if (v.kind === 'form') {
    return [{ label: 'Token', text: v.token }, { label: t('sup.formHTML'), text: `<form method="post" action="${withToken}">
  <input name="name"> <input name="email" type="email">
  <textarea name="message"></textarea>
  <input type="hidden" name="redirect" value="https://your-site/thanks">
  <button>Send</button>
</form>` }]
  }
  return [{ label: 'URL', text: url }, { label: 'Token', text: v.token }, { label: 'curl', text: `curl -X POST ${url} -H 'Authorization: Bearer ${v.token}' -H 'Content-Type: application/json' \\\n  -d '{"external_id":"review-1","thread":"order-77","text":"Wrong size delivered","customer":{"name":"An","email":"an@example.com"},"wait":60}'` }]
})
// no automation answers yet: make one from the template, with the default agent
const making = ref(false)
async function makeAnswerer() {
  making.value = true
  try {
    const r = await $fetch<{ automation: Automation }>(`/api/projects/${props.projectId}/automations`, { method: 'POST', body: {
      name: t('sup.replyAutoName'), source: 'webhook', action: 'chat', enabled: true, prompt: supportReplyPrompt, config: { auth: 'bearer' },
      limits: { max_parallel: 2, max_minutes: 5, max_runs_per_hour: 120 } } })
    form.automation_id = r.automation.id
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    making.value = false
  }
}
async function copy(text: string) {
  if (await copyText(text)) toast.add({ title: t('auto.copied'), color: 'success' })
  else toast.add({ title: text, color: 'neutral', duration: 0 })
}
const where = (s: SupportSource) => {
  const c = s.config as Record<string, string>
  if (s.kind === 'bot') return bots.value?.channels.find(b => b.id === c.channel_id)?.name ?? '—'
  if (s.kind === 'crisp') return `Crisp · ${c.website_id}`
  return c.reply_via === 'email' ? t('sup.via.email') : c.reply_via === 'hook' ? t('sup.via.hook') : t('sup.via.none')
}
</script>

<template>
  <div class="max-w-4xl space-y-3">
    <div class="flex items-center gap-2">
      <p class="flex-1 text-sm text-(--ui-text-muted)">{{ t('sup.sourcesHelp') }}</p>
      <UButton v-if="isAdmin" size="sm" icon="i-lucide-plus" :label="t('sup.addSource')" @click="openForm()" />
    </div>
    <UAlert v-if="error" color="error" variant="subtle" :title="apiError(error)" />
    <div v-else-if="!data" class="rounded-lg border border-(--ui-border)"><LoadingRows :n="2" /></div>
    <p v-else-if="!data.sources.length" class="rounded-lg border border-dashed border-(--ui-border) p-4 text-sm text-(--ui-text-muted)">{{ t('sup.noSources') }}</p>
    <div v-for="s in data?.sources ?? []" :key="s.id" class="space-y-2 rounded-lg border border-(--ui-border) p-3">
      <div class="flex flex-wrap items-center gap-2">
        <UIcon :name="kindIcon[s.kind] ?? 'i-lucide-plug'" class="size-4 text-primary" />
        <p class="font-medium">{{ s.name }}</p>
        <UBadge size="sm" variant="subtle" color="neutral" :label="t(`sup.kind.${s.kind}` as 'sup.kind.api')" />
        <UBadge size="sm" variant="subtle" color="neutral" :label="t(`sup.mode.${s.mode}` as 'sup.mode.auto')" />
        <span class="text-xs text-(--ui-text-muted)">→ {{ autoName(s.automation_id) }} · {{ where(s) }} · {{ t('sup.perMin', { n: s.rate_per_min }) }}</span>
        <div v-if="isAdmin" class="ms-auto flex items-center gap-1">
          <USwitch :model-value="s.enabled" size="sm" :aria-label="t('sup.enabled')" @update:model-value="v => toggle(s, v)" />
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-pencil" :aria-label="t('auto.edit')" @click="openForm(s)" />
          <UButton v-if="s.kind !== 'bot'" size="xs" color="neutral" variant="ghost" icon="i-lucide-key-round" :aria-label="t('sup.rotate')" @click="rotate(s)" />
        </div>
      </div>
      <div v-if="s.kind !== 'bot'" class="flex items-center gap-2">
        <code class="min-w-0 flex-1 truncate rounded bg-(--ui-bg-elevated) px-2 py-1 text-xs">POST {{ hookURL(s.id) }}</code>
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-copy" :aria-label="t('auto.copy')" @click="copy(hookURL(s.id))" />
      </div>
    </div>

    <UModal v-model:open="open" :title="editing ? t('sup.editSource') : t('sup.addSource')">
      <template #body>
        <form id="sup-source" class="space-y-3" @submit.prevent="save">
          <div class="grid gap-3 sm:grid-cols-2">
            <UFormField :label="t('sup.kindLabel')"><USelect v-model="form.kind" :items="kinds" class="w-full" :disabled="!!editing" /></UFormField>
            <UFormField :label="t('sup.name')" required><UInput v-model="form.name" class="w-full" :placeholder="t('sup.namePlaceholder')" /></UFormField>
          </div>
          <UFormField v-if="form.kind === 'bot'" :label="t('sup.bot')" :hint="t('sup.botHint')" required>
            <USelect v-model="form.channel_id" :items="(bots?.channels ?? []).map(b => ({ label: b.name || b.bot_name || b.id, value: b.id }))" class="w-full" />
          </UFormField>
          <div v-if="form.kind === 'crisp'" class="grid gap-3 sm:grid-cols-2">
            <UFormField label="Website ID" required><UInput v-model="form.website_id" class="w-full" /></UFormField>
            <UFormField label="Identifier" required><UInput v-model="form.identifier" class="w-full" /></UFormField>
          </div>
          <UFormField v-if="form.kind === 'api' || form.kind === 'form'" :label="t('sup.replyVia')">
            <USelect v-model="form.reply_via" :items="replyWays" class="w-full" />
          </UFormField>
          <div v-if="(form.kind === 'api' || form.kind === 'form') && form.reply_via === 'email'" class="grid gap-3 sm:grid-cols-2">
            <UFormField label="SMTP host" required><UInput v-model="form.smtp_host" class="w-full" placeholder="smtp.gmail.com" /></UFormField>
            <UFormField label="SMTP port"><UInput v-model="form.smtp_port" class="w-full" /></UFormField>
            <UFormField :label="t('sup.smtpUser')"><UInput v-model="form.smtp_user" class="w-full" /></UFormField>
            <UFormField :label="t('sup.smtpFrom')" required><UInput v-model="form.smtp_from" class="w-full" placeholder="shop@example.com" /></UFormField>
          </div>
          <UFormField v-if="needsSecret" :label="form.kind === 'crisp' ? 'Crisp key' : t('sup.smtpPassword')" :hint="editing ? t('sup.secretKeep') : undefined">
            <UInput v-model="form.secret" type="password" autocomplete="new-password" class="w-full" />
          </UFormField>
          <UFormField :label="t('sup.automation')" :hint="t('sup.automationHint')" required>
            <div class="flex gap-2">
              <USelect v-model="form.automation_id" :items="answerers.map(a => ({ label: a.name, value: a.id }))" class="min-w-0 flex-1" :placeholder="t('sup.noAutomation')" />
              <UButton v-if="!answerers.length" size="sm" color="neutral" variant="outline" icon="i-lucide-sparkles" :label="t('sup.makeAnswerer')" :loading="making" @click="makeAnswerer" />
            </div>
          </UFormField>
          <div class="grid gap-3 sm:grid-cols-2">
            <UFormField :label="t('sup.modeLabel')"><USelect v-model="form.mode" :items="modes" class="w-full" /></UFormField>
            <UFormField :label="t('sup.rate')"><UInput v-model="form.rate_per_min" type="number" min="1" max="600" class="w-full" /></UFormField>
          </div>
        </form>
      </template>
      <template #footer>
        <UButton type="submit" form="sup-source" :label="t('common.save')" :loading="saving" :disabled="!form.name || !form.automation_id" />
      </template>
    </UModal>

    <!-- the token, once -->
    <UModal :open="!!secret" :dismissible="false" :close="false" :title="t('sup.tokenTitle')" :description="t('sup.tokenDesc')">
      <template #body>
        <div class="space-y-3 text-sm">
          <div v-for="row in rows" :key="row.label" class="space-y-1">
            <p class="text-xs font-medium text-(--ui-text-muted)">{{ row.label }}</p>
            <div class="flex items-start gap-2">
              <code class="min-w-0 flex-1 whitespace-pre-wrap break-all rounded bg-(--ui-bg-elevated) px-2 py-1 text-xs">{{ row.text }}</code>
              <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-copy" :aria-label="t('auto.copy')" @click="copy(row.text)" />
            </div>
          </div>
        </div>
      </template>
      <template #footer>
        <UButton :label="t('auto.savedIt')" @click="secret = null" />
      </template>
    </UModal>
  </div>
</template>
