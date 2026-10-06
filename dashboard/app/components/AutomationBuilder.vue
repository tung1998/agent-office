<script setup lang="ts">
// Build an automation: the form on one side, its own chat on the other
// (ADR-042). The chat gets the draft and the last test run with each message
// and may fill the form; nothing is saved until Save.
const props = defineProps<{ projectId: string, automation?: Automation | null, preset?: AutomationDraft }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()

const form = reactive<AutomationDraft>(props.automation ? draftFrom(props.automation) : props.preset ?? emptyDraft())
const editedFrom = props.automation?.version // the automation as it was opened: a save over someone else's change is refused
const saveError = useSaveError()
const highlight = ref<string[]>([])
let clearHl: ReturnType<typeof setTimeout> | undefined
function applyPatch(p: Record<string, unknown>) {
  const changed = mergeDraft(form, p)
  if (!changed.length) return
  highlight.value = changed
  clearTimeout(clearHl)
  clearHl = setTimeout(() => { highlight.value = [] }, 4000)
  toast.add({ title: t('auto.filled', { n: changed.length }), color: 'info' })
}
const lastTest = ref<{ output: string, exit_code: number, timed_out: boolean } | null>(null)
// the last test run first, and a long script cut, so both fit the 8KB the server keeps
const pageContext = () => {
  const draft = automationBody(form)
  if (draft.bot) draft.bot = { ...draft.bot, token: undefined } // the bot's token never goes to the chat
  const body = draft.script.body
  if (body.length > 4000) draft.script = { ...draft.script, body: `${body.slice(0, 4000)}\n… (${body.length - 4000} ký tự nữa, xem trong form)` } // i18n-ignore: sent to the agent
  return JSON.stringify({
    page: props.automation ? 'automation.edit' : 'automation.new',
    automation_id: props.automation?.id ?? '',
    test: lastTest.value ? { exit_code: lastTest.value.exit_code, timed_out: lastTest.value.timed_out, output: lastTest.value.output.slice(-2000) } : null,
    draft
  })
}

const conversationId = ref('')
const saving = ref(false)
const secret = ref<{ url: string, secret: string, auth?: string, authName?: string } | null>(null)
async function save() {
  saving.value = true
  try {
    const body = { ...automationBody(form), conversation_id: conversationId.value, version: editedFrom }
    const res = props.automation
      ? await $fetch<{ automation: Automation, secret?: string }>(`/api/automations/${props.automation.id}`, { method: 'PATCH', body })
      : await $fetch<{ automation: Automation, secret?: string }>(`/api/projects/${props.projectId}/automations`, { method: 'POST', body })
    toast.add({ title: t('auto.saved'), color: 'success' })
    if (res.secret) secret.value = { url: `${location.origin}${res.automation.webhook_url}`, secret: res.secret, auth: res.automation.config.auth, authName: res.automation.config.auth_name }
    else await navigateTo(`/projects/${props.projectId}/automations/${res.automation.id}`)
    savedId.value = res.automation.id
  } catch (e) {
    saveError(e, () => reloadNuxtApp())
  } finally {
    saving.value = false
  }
}
const savedId = ref('')
function secretClosed() {
  secret.value = null
  if (savedId.value) navigateTo(`/projects/${props.projectId}/automations/${savedId.value}`)
}
</script>

<template>
  <div class="grid gap-4 lg:h-[calc(100vh-9rem)] lg:min-h-[36rem] lg:grid-cols-2">
    <div class="flex flex-col lg:min-h-0">
      <div class="lg:min-h-0 lg:flex-1 lg:overflow-y-auto lg:pe-1">
        <AutomationForm :project-id="projectId" :form="form" :highlight="highlight" @tested="(r) => { lastTest = r }" />
      </div>
      <div v-if="isAdmin" class="flex justify-end gap-2 border-t border-(--ui-border) pt-3">
        <UButton color="neutral" variant="ghost" :label="t('org.form.close')" :to="automation ? `/projects/${projectId}/automations/${automation.id}` : { path: `/projects/${projectId}`, query: { tab: 'automations' } }" />
        <UButton icon="i-lucide-save" :loading="saving" :label="t('auto.save')" @click="save" />
      </div>
    </div>
    <div class="h-[32rem] lg:h-auto lg:min-h-0">
      <ChatPanel
        :project-id="projectId" purpose="automation" :automation-id="automation?.id" :page-context="pageContext"
        @automation-patch="applyPatch" @conversation="(id) => { conversationId = id }"
      />
    </div>
    <WebhookSecretModal :value="secret" @close="secretClosed" />
  </div>
</template>
