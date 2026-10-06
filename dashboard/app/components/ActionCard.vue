<script setup lang="ts">
// An operation an agent proposed (a process/container operation or git commit/branch/push).
// Nothing happens until an admin approves.
import type { MessageKey } from '~/locales/vi'
export interface ProposedAction {
  id: string
  kind: string
  label: string
  target: string
  reason: string
  status: 'pending' | 'done' | 'failed' | 'rejected'
  detail: string
  proposed_by: string
  decided_by: string
  message?: string
  target_id?: string
  files?: string[]
  // run_command: the pattern "luôn cho phép" would add (none: a risky command)
  always?: string
  // config_change: a settings change (ADR-045)
  change?: { resource: string, op: 'create' | 'update' | 'delete', id?: string, patch?: Record<string, unknown>, before?: Record<string, unknown> }
  // create_automation / update_automation: the proposed automation
  automation?: { name: string, source: string, every_minutes?: number, cron?: string, timezone?: string, action: string, prompt?: string,
    script?: { lang: string, body: string, timeout_s?: number }, escalate?: { when?: string, action?: string, agent_id?: string, prompt?: string } }
}

const props = defineProps<{ action: ProposedAction, projectId?: string }>()
const emit = defineEmits<{ updated: [ProposedAction] }>()
const toast = useToast()
const { isAdmin } = useAuth()
const { t } = useLang()
const busy = ref<'' | 'approve' | 'always' | 'reject' | 'skip'>('')

// "Duyệt & luôn cho phép": the pattern it adds and the pack it goes to
interface Allowed { pattern: string, pack: string, new_pack?: boolean, auto?: boolean, error?: string }
const canAlways = computed(() => props.action.kind === 'run_command' && !!props.action.always && props.action.status === 'pending' && isAdmin.value)
const plan = ref<Allowed | null>(null)
watch(canAlways, async (on) => {
  if (!on || plan.value) return
  plan.value = await $fetch<Allowed>(`/api/actions/${props.action.id}/always`).catch(() => null)
}, { immediate: true })
const alwaysInfo = computed(() => {
  const p = plan.value
  if (!p?.pattern) return props.action.always ?? ''
  return t(p.new_pack ? 'action.alwaysInfoNew' : 'action.alwaysInfo', { pattern: p.pattern, pack: p.pack })
})

const icon = computed(() => props.action.kind === 'send_message' ? 'i-lucide-send' : props.action.kind.endsWith('_automation') ? 'i-lucide-alarm-clock' : props.action.kind === 'git_commit' ? 'i-lucide-git-commit-horizontal'
  : props.action.kind === 'git_branch' ? 'i-lucide-git-branch'
    : props.action.kind === 'git_push' ? 'i-lucide-upload'
      : props.action.kind.startsWith('stop') ? 'i-lucide-square'
  : props.action.kind.startsWith('restart') ? 'i-lucide-rotate-cw' : 'i-lucide-play')
const kindLabels: Record<string, string> = {
  run_process: 'action.kind.run_process',
  restart_process: 'action.kind.restart_process',
  stop_process: 'action.kind.stop_process',
  start_container: 'action.kind.start_container',
  restart_container: 'action.kind.restart_container',
  stop_container: 'action.kind.stop_container',
  git_commit: 'action.kind.git_commit',
  git_branch: 'action.kind.git_branch',
  git_push: 'action.kind.git_push',
  run_command: 'action.kind.run_command',
  create_automation: 'action.kind.create_automation',
  update_automation: 'action.kind.update_automation',
  config_change: 'action.kind.config_change',
  start_task: 'action.kind.start_task',
  run_automation: 'action.kind.run_automation',
  remember: 'action.kind.remember',
  mcp_call: 'action.kind.mcp_call',
  send_message: 'action.kind.send_message'
}
const kindLabel = computed(() => {
  const key = kindLabels[props.action.kind]
  return key ? t(key as MessageKey) : props.action.kind
})
const statusMeta = computed<Record<ProposedAction['status'], { label: string, color: 'warning' | 'success' | 'neutral' | 'error' }>>(() => ({
  pending: { label: t('action.pending'), color: 'warning' },
  done: { label: t('action.done'), color: 'success' },
  failed: { label: t('action.failed'), color: 'error' },
  rejected: { label: t('action.rejected'), color: 'neutral' }
}))
const spec = computed(() => props.action.automation)
const when = computed(() => {
  const s = spec.value
  if (!s) return ''
  if (s.source === 'webhook') return t('auto.sourceWebhook')
  return s.cron ? `${s.cron}${s.timezone ? ` (${s.timezone})` : ''}` : t('auto.every', { n: s.every_minutes ?? 0 })
})
// a settings change: what each field goes from and to
const changeRows = computed(() => {
  const c = props.action.change
  if (!c) return []
  if (c.op === 'delete') return []
  const after = { ...(c.before ?? {}), ...(c.patch ?? {}) }
  return auditDiff(c.op === 'create' ? null : (c.before ?? null), c.op === 'create' ? (c.patch ?? {}) : after)
})
const show = (v: unknown) => v === undefined ? '—' : typeof v === 'string' ? v : JSON.stringify(v)
// a new AI connection: the person pastes its key here, the AI never sees it
const apiKey = ref('')
const needsKey = computed(() => props.action.change?.resource === 'provider' && props.action.change.op === 'create')

// the log opens right here in the chat: a command's output, or the live log
// of the process / container it ran
const logOpen = ref(false)
const logUrl = computed(() => {
  const a = props.action
  if (a.kind.endsWith('_process') && a.target_id) return `/api/processes/${a.target_id}/stream`
  if (a.kind.endsWith('_container') && props.projectId) return `/api/projects/${props.projectId}/compose/logs?service=${encodeURIComponent(a.target)}`
  return null
})
const hasLog = computed(() => props.action.status !== 'pending' && (props.action.kind === 'run_command' ? !!props.action.detail : !!logUrl.value))

// skip: rejected, and its agent is not run again about it
async function decide(approve: boolean, always = false, skip = false) {
  busy.value = always ? 'always' : approve ? 'approve' : skip ? 'skip' : 'reject'
  try {
    const body = always ? { always: true } : skip ? { skip: true } : approve && apiKey.value ? { api_key: apiKey.value } : {}
    const res = await $fetch<{ action: ProposedAction, always?: Allowed }>(`/api/actions/${props.action.id}/${approve ? 'approve' : 'reject'}`, { method: 'POST', body })
    emit('updated', res.action)
    if (res.action.status === 'failed') toast.add({ title: t('action.failedToast'), description: res.action.detail, color: 'error' })
    const x = res.always
    if (x?.error) toast.add({ title: t('action.alwaysError'), description: x.error, color: 'error' })
    else if (x) toast.add({ title: t(x.auto ? 'action.alwaysDone' : 'action.alwaysNoAuto', { pattern: x.pattern }), color: x.auto ? 'success' : 'warning' })
  } catch (e) {
    const a = (e as { data?: { action?: ProposedAction } }).data?.action
    if (a) emit('updated', a)
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <!-- a phone: the text takes the width, the status and buttons go under it -->
  <div class="flex flex-wrap items-start gap-x-3 gap-y-2 rounded-lg border border-(--ui-border) px-3 py-2.5 sm:items-center">
    <span class="grid size-8 shrink-0 place-items-center rounded-md bg-(--ui-bg-elevated)">
      <UIcon :name="icon" class="size-4 text-primary" />
    </span>
    <div class="min-w-0 flex-1 basis-[calc(100%-2.75rem)] sm:basis-0">
      <p class="text-sm font-medium">{{ kindLabel }}</p>
      <code v-if="action.target" class="mt-0.5 block break-all rounded bg-(--ui-bg-elevated) px-1.5 py-0.5 font-mono text-xs">{{ action.target }}</code>
      <pre v-if="action.message" class="mt-1 whitespace-pre-wrap rounded bg-(--ui-bg-elevated) px-2 py-1 font-mono text-xs">{{ action.message }}</pre>
      <p v-if="action.files?.length" class="mt-1 truncate font-mono text-xs text-(--ui-text-muted)" :title="action.files.join('\n')">{{ t('action.fileCount', { n: action.files.length, files: action.files.join(', ') }) }}</p>
      <div v-if="spec" class="mt-1 space-y-1 text-xs">
        <p class="text-(--ui-text-muted)">
          <UIcon name="i-lucide-clock" class="me-1 inline size-3.5 align-[-2px]" />{{ when }}
          · {{ spec.action === 'script' ? t('auto.actionScript') : t('auto.actionChat') }}
          <template v-if="spec.action === 'script'"> · {{ t(`auto.escalate.${spec.escalate?.when || 'failure'}` as MessageKey) }}</template>
        </p>
        <pre v-if="spec.script?.body" class="max-h-64 overflow-auto rounded bg-(--ui-bg-elevated) px-2 py-1 font-mono">{{ spec.script.lang }} ·
{{ spec.script.body }}</pre>
        <p v-else-if="spec.prompt" class="whitespace-pre-wrap rounded bg-(--ui-bg-elevated) px-2 py-1">{{ spec.prompt }}</p>
        <p v-if="spec.action === 'script' && spec.escalate?.when !== 'never' && (spec.escalate?.agent_id || spec.escalate?.prompt)" class="whitespace-pre-wrap rounded bg-(--ui-bg-elevated) px-2 py-1">
          {{ t('auto.escalateAgent') }}: {{ spec.escalate?.agent_id || t('auto.agentDefault') }}<template v-if="spec.escalate?.prompt">
{{ spec.escalate.prompt }}</template>
        </p>
      </div>
      <div v-if="action.change" class="mt-1 space-y-2 text-xs">
        <p class="text-(--ui-text-muted)">{{ t(`action.op.${action.change.op}` as MessageKey) }} · <code>{{ action.change.resource }}</code></p>
        <table v-if="changeRows.length" class="w-full">
          <tbody>
            <tr v-for="d in changeRows" :key="d.key" class="align-top">
              <td class="w-40 py-0.5 pr-2 font-mono">{{ d.key }}</td>
              <td v-if="action.change.op !== 'create'" class="max-w-64 break-all py-0.5 pr-2 text-(--ui-error) line-through decoration-(--ui-error)/40">{{ show(d.before) }}</td>
              <td class="max-w-64 break-all py-0.5 text-(--ui-success)">{{ show(d.after) }}</td>
            </tr>
          </tbody>
        </table>
        <UInput
          v-if="needsKey && action.status === 'pending' && isAdmin" v-model="apiKey" type="password" size="sm" class="w-full max-w-sm"
          :placeholder="t('action.pasteKey')" icon="i-lucide-key-round"
        />
      </div>
      <p v-if="action.reason" class="text-xs text-(--ui-text-muted)">{{ action.reason }}</p>
      <p v-if="action.status !== 'pending' && action.detail" class="line-clamp-2 text-xs" :title="action.detail" :class="action.status === 'failed' ? 'text-(--ui-error)' : 'text-(--ui-text-muted)'">{{ action.detail }}</p>
    </div>
    <UBadge :color="statusMeta[action.status].color" variant="subtle" size="sm" :label="statusMeta[action.status].label" />
    <UBadge v-if="action.decided_by?.startsWith('auto:')" color="warning" variant="outline" size="sm" icon="i-lucide-zap" :label="t('action.auto')" :title="action.decided_by.slice(5)" />
    <template v-if="action.status === 'pending' && isAdmin">
      <UButton size="xs" icon="i-lucide-check" :label="t('action.approve')" :loading="busy === 'approve'" :disabled="!!busy" @click="decide(true)" />
      <span v-if="canAlways" class="inline-flex items-center gap-1">
        <UButton size="xs" variant="soft" icon="i-lucide-infinity" :label="t('action.approveAlways')" :loading="busy === 'always'" :disabled="!!busy" @click="decide(true, true)" />
        <UTooltip :text="alwaysInfo"><UIcon name="i-lucide-info" class="size-4 text-(--ui-text-muted)" /></UTooltip>
      </span>
      <UButton size="xs" color="neutral" variant="ghost" :label="t('action.reject')" :loading="busy === 'reject'" :disabled="!!busy" @click="decide(false)" />
      <UTooltip :text="t('action.skipInfo')">
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-skip-forward" :label="t('action.skip')" :loading="busy === 'skip'" :disabled="!!busy" @click="decide(false, false, true)" />
      </UTooltip>
    </template>
    <UButton
      v-else-if="hasLog" size="xs" color="neutral" variant="ghost" icon="i-lucide-terminal"
      :trailing-icon="logOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" :label="t('action.seeLog')" @click="logOpen = !logOpen"
    />
    <div v-if="logOpen" class="basis-full">
      <pre v-if="action.kind === 'run_command'" class="max-h-96 overflow-auto rounded-md bg-(--ui-bg-elevated) p-2 font-mono text-xs whitespace-pre-wrap">{{ action.detail }}</pre>
      <div v-else class="h-80 overflow-hidden rounded-md border border-(--ui-border)">
        <LogTerminal :url="logUrl" :title="action.target" :empty="t('ops.emptyLog')" />
      </div>
    </div>
  </div>
</template>
