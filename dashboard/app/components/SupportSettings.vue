<script setup lang="ts">
// The pack's settings (ADR-174): case types (their words, how answers go
// out, SLA), the default SLA, the words asking for a person; and the hooks
// told of a ticket's events, with what was delivered.
const props = defineProps<{ projectId: string }>()
const { t, when } = useLang()
const toast = useToast()
const { isAdmin } = useAuth()
const base = computed(() => `/api/projects/${props.projectId}/support`)

const { data: st } = useLiveFetch<SupportSettings>(() => `${base.value}/settings`, { lazy: true })
const form = reactive<{ sla: number, human: string, cases: (CaseType & { words: string })[] }>({ sla: 60, human: '', cases: [] })
watch(st, (v) => {
  if (!v) return
  form.sla = v.sla_minutes
  form.human = v.human_words.join(', ')
  form.cases = v.cases.map(c => ({ ...c, words: c.keywords.join(', ') }))
}, { immediate: true })
const modes = computed(() => [{ label: t('sup.caseMode.source'), value: '' }, { label: t('sup.mode.approve'), value: 'approve' },
  { label: t('sup.mode.auto'), value: 'auto' }, { label: t('sup.caseMode.human'), value: 'human' }])
const split = (s: string) => s.split(',').map(x => x.trim()).filter(Boolean)
const saving = ref(false)
async function save() {
  if (saving.value) return
  saving.value = true
  try {
    await $fetch(`${base.value}/settings`, { method: 'PUT', body: {
      sla_minutes: Number(form.sla), human_words: split(form.human),
      cases: form.cases.map(c => ({ key: c.key, name: c.name, description: c.description, mode: c.mode, sla_minutes: Number(c.sla_minutes) || 0, keywords: split(c.words) }))
    } })
    toast.add({ title: t('feat.saved'), color: 'success' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    saving.value = false
  }
}
const addCase = () => form.cases.push({ key: '', name: '', keywords: [], words: '', description: '', mode: '', sla_minutes: 0 })

// hooks
const { data: hooks } = useLiveFetch<{ hooks: SupportHook[], events: string[] }>(() => `${base.value}/hooks`, { lazy: true })
const { data: autos } = useLiveFetch<{ automations: Automation[] }>(() => `/api/projects/${props.projectId}/automations`, { lazy: true })
const { data: dels } = useLiveFetch<{ deliveries: SupportDelivery[] }>(() => `${base.value}/deliveries`, { lazy: true })
const hookOpen = ref(false)
const hook = reactive({ kind: 'webhook' as SupportHook['kind'], name: '', url: '', automation_id: '', events: [] as string[] })
const hookSecret = ref('')
function openHook() {
  Object.assign(hook, { kind: 'webhook', name: '', url: '', automation_id: autos.value?.automations[0]?.id ?? '', events: [] })
  hookOpen.value = true
}
async function saveHook() {
  try {
    const r = await $fetch<{ secret: string }>(`${base.value}/hooks`, { method: 'POST', body: hook })
    hookOpen.value = false
    hookSecret.value = r.secret
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function toggleHook(h: SupportHook, enabled: boolean) {
  try {
    await $fetch(`${base.value}/hooks/${h.id}`, { method: 'PATCH', body: { enabled } })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
async function removeHook(h: SupportHook) {
  if (!confirm(t('sup.hookDelete'))) return
  try {
    await $fetch(`${base.value}/hooks/${h.id}`, { method: 'DELETE' })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}
const delColor = { pending: 'warning', sent: 'success', failed: 'error' } as const
async function copy(text: string) {
  if (await copyText(text)) toast.add({ title: t('auto.copied'), color: 'success' })
  else toast.add({ title: text, color: 'neutral', duration: 0 })
}
</script>

<template>
  <div class="max-w-4xl space-y-4">
    <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
      <p class="font-medium">{{ t('sup.casesTitle') }}</p>
      <div class="grid gap-3 sm:grid-cols-[13rem_1fr]">
        <UFormField :label="t('sup.slaDefault')" :hint="t('sup.minutes')"><UInput v-model="form.sla" type="number" min="0" class="w-full" :disabled="!isAdmin" /></UFormField>
        <UFormField :label="t('sup.humanWords')" :hint="t('coll.tagsHint')"><UInput v-model="form.human" class="w-full" :disabled="!isAdmin" /></UFormField>
      </div>
      <div v-for="(c, i) in form.cases" :key="i" class="space-y-2 rounded-lg border border-(--ui-border) p-3">
        <div class="grid grid-cols-[1fr_1fr_auto] gap-2 sm:grid-cols-[8rem_1fr_10rem_6rem_auto]">
          <UInput v-model="c.key" :placeholder="t('sup.caseKey')" size="sm" :disabled="!isAdmin" />
          <UInput v-model="c.name" :placeholder="t('sup.caseName')" size="sm" :disabled="!isAdmin" />
          <USelect v-model="c.mode" :items="modes" size="sm" :disabled="!isAdmin" />
          <UInput v-model="c.sla_minutes" type="number" min="0" :placeholder="t('sup.slaShort')" size="sm" :disabled="!isAdmin" />
          <UButton v-if="isAdmin" size="xs" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('coll.delete')" class="row-start-1 col-start-3 sm:col-start-5" @click="form.cases.splice(i, 1)" />
        </div>
        <UInput v-model="c.words" :placeholder="t('sup.caseWords')" size="sm" class="w-full" :disabled="!isAdmin" />
        <UInput v-model="c.description" :placeholder="t('sup.caseWhen')" size="sm" class="w-full" :disabled="!isAdmin" />
      </div>
      <div v-if="isAdmin" class="flex gap-2">
        <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-plus" :label="t('sup.addCase')" @click="addCase" />
        <UButton size="xs" :label="t('common.save')" :loading="saving" class="ms-auto" @click="save" />
      </div>
    </UCard>

    <UCard :ui="{ body: 'space-y-3 sm:p-4' }">
      <div class="flex items-center gap-2">
        <p class="flex-1 font-medium">{{ t('sup.hooks') }} <InfoTip :text="t('sup.hooksHelp')" /></p>
        <UButton v-if="isAdmin" size="xs" icon="i-lucide-plus" :label="t('sup.addHook')" @click="openHook" />
      </div>
      <p v-if="hooks && !hooks.hooks.length" class="text-sm text-(--ui-text-muted)">{{ t('sup.noHooks') }}</p>
      <div v-for="h in hooks?.hooks ?? []" :key="h.id" class="flex flex-wrap items-center gap-2 rounded-lg border border-(--ui-border) px-3 py-2 text-sm">
        <UIcon :name="h.kind === 'webhook' ? 'i-lucide-webhook' : 'i-lucide-alarm-clock'" class="size-4 text-primary" />
        <span class="min-w-0 flex-1 truncate">{{ h.kind === 'webhook' ? h.url : (autos?.automations.find(a => a.id === h.automation_id)?.name ?? h.automation_id) }}</span>
        <span class="text-xs text-(--ui-text-muted)">{{ h.events.length ? h.events.join(', ') : t('sup.allEvents') }}</span>
        <template v-if="isAdmin">
          <USwitch :model-value="h.enabled" size="sm" :aria-label="t('sup.enabled')" @update:model-value="v => toggleHook(h, v)" />
          <UButton size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('coll.delete')" @click="removeHook(h)" />
        </template>
      </div>
      <details v-if="dels?.deliveries.length" class="text-xs">
        <summary class="cursor-pointer text-(--ui-text-muted)">{{ t('sup.deliveries', { n: dels.deliveries.length }) }}</summary>
        <ul class="mt-2 space-y-1">
          <li v-for="d in dels.deliveries" :key="d.id" class="flex flex-wrap items-center gap-2">
            <UBadge size="sm" variant="subtle" :color="delColor[d.status]" :label="d.status" />
            <span>{{ d.kind === 'reply' ? t('sup.replyOut') : d.event }}</span>
            <span class="text-(--ui-text-dimmed)">{{ when(d.updated_at) }} · {{ t('sup.attempts', { n: d.attempts }) }}</span>
            <span v-if="d.last_error" class="min-w-0 truncate text-(--ui-error)">{{ d.last_error }}</span>
          </li>
        </ul>
      </details>
    </UCard>

    <UModal v-model:open="hookOpen" :title="t('sup.addHook')">
      <template #body>
        <form id="sup-hook" class="space-y-3" @submit.prevent="saveHook">
          <UFormField :label="t('sup.hookKind')">
            <USelect v-model="hook.kind" :items="[{ label: 'Webhook (POST)', value: 'webhook' }, { label: t('sup.hookAuto'), value: 'automation' }]" class="w-full" />
          </UFormField>
          <UFormField v-if="hook.kind === 'webhook'" label="URL" required><UInput v-model="hook.url" class="w-full" placeholder="https://…" /></UFormField>
          <UFormField v-else :label="t('sup.hookAuto')" required>
            <USelect v-model="hook.automation_id" :items="(autos?.automations ?? []).map(a => ({ label: a.name, value: a.id }))" class="w-full" />
          </UFormField>
          <UFormField :label="t('sup.events')" :hint="t('sup.allEventsHint')">
            <div class="flex flex-wrap gap-x-4 gap-y-1">
              <UCheckbox v-for="ev in hooks?.events ?? []" :key="ev" :label="ev" :model-value="hook.events.includes(ev)"
                @update:model-value="v => hook.events = v ? [...hook.events, ev] : hook.events.filter(x => x !== ev)" />
            </div>
          </UFormField>
        </form>
      </template>
      <template #footer>
        <UButton type="submit" form="sup-hook" :label="t('common.save')" />
      </template>
    </UModal>

    <UModal :open="!!hookSecret" :dismissible="false" :close="false" :title="t('sup.hookSecret')" :description="t('sup.hookSecretDesc')">
      <template #body>
        <div class="flex items-start gap-2 text-sm">
          <code class="min-w-0 flex-1 break-all rounded bg-(--ui-bg-elevated) px-2 py-1 text-xs">{{ hookSecret }}</code>
          <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-copy" :aria-label="t('auto.copy')" @click="copy(hookSecret)" />
        </div>
      </template>
      <template #footer>
        <UButton :label="t('auto.savedIt')" @click="hookSecret = ''" />
      </template>
    </UModal>
  </div>
</template>
