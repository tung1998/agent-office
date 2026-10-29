<script setup lang="ts">
// A workflow run (ADR-098) in the chat that called it: who fills each role
// and how far they got, its gates (one waiting on a person is decided here),
// turns and cost; Stop ends it. detail: on the run's own page (no link to it).
import type { MessageKey } from '~/locales/vi'

const props = defineProps<{ run: WorkflowRun, detail?: boolean }>()
const { manages } = useAuth()
const isAdmin = computed(() => manages(props.run.project_id)) // on any page (ADR-224)
const emit = defineEmits<{ updated: [WorkflowRun] }>()
const toast = useToast()
const { t, dateLocale } = useLang()

const statusColor: Record<WorkflowRun['status'], 'info' | 'success' | 'error' | 'neutral'> = { running: 'info', done: 'success', failed: 'error', stopped: 'neutral' }
const roleIcon: Record<RunRole['status'], string> = { idle: 'i-lucide-circle-dashed', working: 'i-lucide-loader-circle', done: 'i-lucide-circle-check', failed: 'i-lucide-circle-x' }
const roleColor: Record<RunRole['status'], string> = { idle: 'text-(--ui-text-dimmed)', working: 'text-(--ui-info) animate-spin', done: 'text-(--ui-success)', failed: 'text-(--ui-error)' }
const gateColor: Record<RunGate['status'], 'neutral' | 'warning' | 'success' | 'error'> = { open: 'neutral', waiting: 'warning', passed: 'success', failed: 'error' }
const usd = (n: number) => `$${n.toFixed(n < 1 ? 3 : 2)}`
const when = (d: string) => new Date(d).toLocaleTimeString(dateLocale.value, { hour: '2-digit', minute: '2-digit', second: '2-digit' })

const deciding = ref('')
async function decide(g: RunGate, approve: boolean) {
  deciding.value = g.key
  try {
    await $fetch(`/api/actions/${g.action_id}/${approve ? 'approve' : 'reject'}`, { method: 'POST', body: {} })
    const res = await $fetch<{ run: WorkflowRun }>(`/api/workflow-runs/${props.run.id}`)
    emit('updated', res.run)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    deciding.value = ''
  }
}
const waiting = computed(() => props.run.status === 'running' ? props.run.gates.filter(g => g.kind === 'approve' && g.status === 'waiting' && g.action_id) : [])
const pageTo = computed(() => `/projects/${props.run.project_id}/workflows/runs/${props.run.id}`)

const stopping = ref(false)
async function stop() {
  stopping.value = true
  try {
    const res = await $fetch<{ run: WorkflowRun }>(`/api/workflow-runs/${props.run.id}/stop`, { method: 'POST', body: {} })
    emit('updated', res.run)
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    stopping.value = false
  }
}
</script>

<template>
  <div class="space-y-2 rounded-lg border border-(--ui-border) px-3 py-2.5 text-sm">
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <UIcon name="i-lucide-workflow" class="size-4 shrink-0 text-primary" />
      <span class="font-medium">{{ run.workflow_name }}</span>
      <span class="font-mono text-xs text-(--ui-text-muted)">/{{ run.workflow_key }}</span>
      <UBadge :color="statusColor[run.status]" variant="subtle" size="sm" :label="t(`wf.status.${run.status}` as MessageKey)" :icon="run.status === 'running' ? 'i-lucide-loader-circle' : undefined" :ui="{ leadingIcon: 'animate-spin' }" />
      <span class="ms-auto flex items-center gap-2 text-xs text-(--ui-text-muted)">
        <span :title="t('wf.run.turnsInfo')">{{ run.max_turns ? t('wf.run.turnsOf', { n: run.turns, max: run.max_turns }) : t('wf.run.turns', { n: run.turns }) }}</span>
        <span>· {{ usd(run.cost_usd) }}</span>
        <UButton v-if="!detail" size="xs" color="neutral" variant="ghost" icon="i-lucide-external-link" :label="t('wf.runs.details')" :to="pageTo" />
        <UButton v-if="run.status === 'running'" size="xs" color="neutral" variant="outline" icon="i-lucide-square" :label="t('chat.stop')" :loading="stopping" @click="stop" />
      </span>
    </div>
    <p class="text-xs text-(--ui-text-muted)">
      {{ t('wf.run.coordinator', { name: run.coordinator_name }) }}<template v-if="run.input"> · <span class="italic">{{ run.input }}</span></template>
    </p>

    <ul v-if="run.roles.length" class="divide-y divide-(--ui-border) rounded-md border border-(--ui-border)">
      <li v-for="r in run.roles" :key="r.role" class="flex items-center gap-2 px-2.5 py-1.5 text-xs">
        <UIcon :name="roleIcon[r.status]" class="size-4 shrink-0" :class="roleColor[r.status]" :title="t(`wf.roleStatus.${r.status}` as MessageKey)" />
        <span class="font-medium">{{ r.name || r.role }}</span>
        <UTooltip :text="t(`wf.access.${accessKey(r.access)}` as MessageKey)">
          <UIcon :name="accessIcon[accessKey(r.access)]" class="size-3.5 text-(--ui-text-muted)" />
        </UTooltip>
        <NuxtLink v-if="r.workflow" :to="r.run_id ? `/projects/${run.project_id}/workflows/runs/${r.run_id}` : undefined" class="flex items-center gap-0.5 font-mono text-primary" :title="t('wf.subInfo')">
          <UIcon name="i-lucide-corner-down-right" class="size-3.5" />/{{ r.workflow }}
        </NuxtLink>
        <span class="min-w-0 truncate" :class="r.agent_name ? '' : 'text-(--ui-text-dimmed)'">→ {{ r.agent_name || t('wf.unbound') }}</span>
        <span class="ms-auto shrink-0 text-(--ui-text-muted)">
          {{ t('wf.run.turns', { n: r.turns }) }}<template v-if="r.rounds"> · {{ t('wf.run.rounds', { n: r.rounds }) }}</template> · {{ usd(r.cost_usd) }}
        </span>
      </li>
    </ul>

    <div v-if="run.gates.length" class="flex flex-wrap items-center gap-1.5 text-xs">
      <span class="text-(--ui-text-muted)">{{ t('wf.gates') }}</span>
      <UBadge
        v-for="g in run.gates" :key="g.key" :color="gateColor[g.status]" variant="subtle" size="sm"
        :icon="g.kind === 'approve' ? 'i-lucide-user-check' : 'i-lucide-test-tube'"
        :label="`${g.name} · ${t(`wf.gateStatus.${g.status}` as MessageKey)}`" :title="g.detail || undefined"
      />
    </div>

    <div v-for="g in waiting" :key="g.key" class="flex flex-wrap items-center gap-2 rounded-md bg-(--ui-warning)/10 px-2.5 py-1.5 text-xs">
      <UIcon name="i-lucide-user-check" class="size-4 text-(--ui-warning)" />
      <span class="min-w-0 flex-1 basis-48">{{ t('wf.runs.gateWaiting', { name: g.name }) }} <NuxtLink v-if="!detail" :to="pageTo" class="text-primary">{{ t('wf.runs.seeWhat') }}</NuxtLink></span>
      <template v-if="isAdmin">
        <UButton size="xs" color="success" icon="i-lucide-check" :label="t('wf.runs.approve')" :loading="deciding === g.key" @click="decide(g, true)" />
        <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-x" :label="t('wf.runs.reject')" :disabled="deciding === g.key" @click="decide(g, false)" />
      </template>
    </div>

    <p v-if="run.error" class="whitespace-pre-wrap text-xs text-(--ui-error)">{{ run.error }}</p>
    <p v-else-if="run.result && run.status !== 'running' && !run.caller_conversation_id && !detail" class="line-clamp-3 whitespace-pre-wrap text-xs text-(--ui-text-muted)" :title="run.result">{{ run.result }}</p>

    <details v-if="run.log?.length" class="text-xs text-(--ui-text-muted)">
      <summary class="cursor-pointer">{{ t('wf.run.log', { n: run.log.length }) }}</summary>
      <ul class="mt-1 space-y-0.5 ps-4">
        <li v-for="(l, i) in run.log" :key="i"><span class="font-mono text-(--ui-text-dimmed)">{{ when(l.at) }}</span> {{ l.text }}</li>
      </ul>
    </details>
  </div>
</template>
