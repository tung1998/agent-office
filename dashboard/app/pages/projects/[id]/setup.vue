<script setup lang="ts">
import type { Permissions as AgentPermissions } from '~/composables/useOffice'

interface AgentDoc { path: string, kind: string, name?: string, description?: string }
interface Summary {
  name: string
  description: string
  manifests: string[]
  frameworks: string[]
  services: string[]
  infra: string[]
  languages: Record<string, number>
  agent_docs: AgentDoc[]
  readme: string
  file_count: number
  env_vars: string[]
}
// a library agent the AI picked, as the person edits it (ADR-166)
interface AgentPick {
  id: string
  key?: string
  name?: string
  role?: string
  model_tier?: ModelTier
  instructions?: string
  level?: PermLevel
  caps?: string[]
  commands?: string[]
  reason: string
}
interface PlanAgent { key: string, name: string, role?: string, model_tier?: ModelTier, permissions: AgentPermissions }
interface Plan { default: string, agents: PlanAgent[] }
interface Proposal {
  description: string
  reason: string
  confidence: number
  default_agent: string
  agents: AgentPick[]
  workflows: Pick[]
  automations: Pick[]
  skills: Pick[]
  mcp: Pick[]
  quick_checks: string
  notes: string[]
}
interface Pick { name: string, reason: string, needs?: 'key' | 'login' }
interface ReadyItem { kind: string, name: string, status: 'ok' | 'todo' | 'error', detail?: string }
interface ProposeResult {
  proposal: Proposal
  plan: Plan
  problems: string[]
  warnings: string[]
  provider: string
  model: string
  usage: { input_tokens: number, output_tokens: number, cost_usd?: number, duration_ms: number }
}

const route = useRoute()
const toast = useToast()
const { t } = useLang()
const id = computed(() => route.params.id as string)
const here = computed(() => `/projects/${id.value}/setup`)

const _f1 = useLiveFetch<{ project: Project }>(() => `/api/projects/${id.value}`)
const { data: projData } = _f1
const _f2 = useLiveFetch<{ providers: Provider[] }>('/api/providers')
const { data: provData } = _f2
await Promise.all([_f1, _f2]) // started together: one round trip, not 2 (a phone over a VPN)
const project = computed(() => projData.value?.project)
const readyProvider = computed(() => provData.value?.providers.find(p => p.is_default && p.status === 'ok')
  ?? provData.value?.providers.find(p => p.status === 'ok'))

const { isOfficeAdmin } = useAuth()
function goConnect() {
  if (!isOfficeAdmin.value) { // the connections are an admin's
    toast.add({ title: t('setup.needProvider'), description: t('setup.needProviderAdmin'), color: 'warning' })
    return
  }
  toast.add({ title: t('setup.needProvider'), description: t('setup.needProviderDesc'), color: 'warning' })
  return navigateTo({ path: '/providers', query: { next: here.value } })
}

// Redirect straight away when no AI connection works.
if (!readyProvider.value) {
  await goConnect()
}

// ---- step 1: scan ----
const summary = ref<Summary | null>(null)
const scanning = ref(false)
const goal = ref('')
async function runScan() {
  if (project.value?.scope !== 'folder') return
  scanning.value = true
  try {
    const res = await $fetch<{ summary: Summary }>(`/api/projects/${id.value}/setup/scan`, { method: 'POST', body: {} })
    summary.value = res.summary
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  } finally {
    scanning.value = false
  }
}
onMounted(runScan)

const topLanguages = computed(() => Object.entries(summary.value?.languages ?? {}).sort((a, b) => b[1] - a[1]).slice(0, 6))
const docKind = computed<Record<string, string>>(() => ({ instructions: t('setup.docInstructions'), subagent: t('setup.docSubagent'), skill: t('setup.docSkill'), rules: t('setup.docRules') }))

// ---- step 2: propose ----
const proposing = ref(false)
const result = ref<ProposeResult | null>(null)
const description = ref('')
const picks = ref<AgentPick[]>([])
const accepted = ref<boolean[]>([])
const editing = ref<number | null>(null)
const workflowsAccepted = ref<boolean[]>([])
const automationsAccepted = ref<boolean[]>([])
const preview = ref<{ plan: Plan, problems: string[], warnings: string[] } | null>(null)
const skillsAccepted = ref<boolean[]>([])
const mcpAccepted = ref<boolean[]>([])
const quickChecks = ref('')
const useQuickChecks = ref(true)
const ready = ref<ReadyItem[] | null>(null)
let previewToken = 0

// the workflow library's names; the automation templates live in the dashboard
const { data: wfLib } = useLiveFetch<{ workflows: { def: { key: string, name: string } }[] }>('/api/workflow-library', { lazy: true })
const workflowName = (key: string) => wfLib.value?.workflows.find(w => w.def.key === key)?.def.name ?? key
const automationTemplate = (key: string) => automationTemplates.find(x => x.key === key)

async function propose() {
  proposing.value = true
  try {
    const automations = automationTemplates.map(x => ({ name: x.key, description: `${x.name}: ${x.description}` }))
    const res = await $fetch<{ result: ProposeResult }>(`/api/projects/${id.value}/setup/propose`, { method: 'POST', body: { goal: goal.value, automations } })
    const p = res.result.proposal
    result.value = res.result
    description.value = p.description
    picks.value = p.agents.map(a => ({ ...a }))
    previewToken++
    preview.value = { plan: res.result.plan, problems: res.result.problems, warnings: res.result.warnings }
    accepted.value = p.agents.map(a => !!planned(a)) // one the project has (left out): unticked
    editing.value = null
    workflowsAccepted.value = p.workflows.map(() => true)
    automationsAccepted.value = p.automations.map(() => true)
    skillsAccepted.value = p.skills.map(() => true)
    mcpAccepted.value = p.mcp.map(() => true)
    quickChecks.value = p.quick_checks
    useQuickChecks.value = true
    ready.value = null
  } catch (e) {
    const d = (e as { data?: { code?: string, error?: string } }).data
    if (d?.code === 'no_provider') return goConnect()
    if (d?.code === 'budget') {
      toast.add({ title: t('setup.needProviderBudget'), description: d.error, color: 'warning', actions: [{ label: t('setup.viewCosts'), onClick: () => { navigateTo(`/projects/${route.params.id}?tab=info`) } }] })
      return
    }
    toast.add({ title: t('setup.proposeFailed'), description: apiError(e), color: 'error' })
  } finally {
    proposing.value = false
  }
}

const chosenAgents = computed(() => picks.value.filter((_, i) => accepted.value[i]))
const kept = <T,>(list: T[] | undefined, on: boolean[]) => (list ?? []).filter((_, i) => on[i])
const chosenWorkflows = computed(() => kept(result.value?.proposal.workflows, workflowsAccepted.value).map(p => p.name))
const chosenAutomations = computed(() => kept(result.value?.proposal.automations, automationsAccepted.value).map(p => p.name))
const chosenSkills = computed(() => kept(result.value?.proposal.skills, skillsAccepted.value).map(p => p.name))
const chosenMCP = computed(() => kept(result.value?.proposal.mcp, mcpAccepted.value).map(p => p.name))

// Rebuild the preview whenever the ticked or edited agents move.
watch([picks, accepted], async () => {
  if (!result.value) return
  const token = ++previewToken
  try {
    const res = await $fetch<{ plan: Plan, problems: string[], warnings: string[] }>(`/api/projects/${id.value}/setup/build`, {
      method: 'POST', body: { agents: chosenAgents.value, default_agent: result.value.proposal.default_agent }
    })
    if (token !== previewToken) return
    preview.value = res
  } catch (e) {
    if (token !== previewToken) return
    toast.add({ title: apiError(e), color: 'error' })
  }
}, { deep: true })

const applying = ref(false)
async function apply() {
  applying.value = true
  try {
    const res = await $fetch<{ ready: ReadyItem[] }>(`/api/projects/${id.value}/setup/apply`, {
      method: 'POST',
      body: {
        agents: chosenAgents.value, default_agent: result.value?.proposal.default_agent ?? '', workflows: chosenWorkflows.value,
        description: description.value, skills: chosenSkills.value, mcp: chosenMCP.value, quick_checks: useQuickChecks.value ? quickChecks.value : undefined
      }
    })
    ready.value = [...res.ready, ...await makeAutomations()]
    toast.add({ title: t('setup.applyDone'), color: 'success' })
  } catch (e) {
    const d = (e as { data?: { problems?: string[] } }).data
    toast.add({ title: t('setup.applyFailed'), description: d?.problems?.join('; ') ?? apiError(e), color: 'error' })
  } finally {
    applying.value = false
  }
}
// the automations picked are made paused: their trigger (schedule, webhook) is the person's to check
async function makeAutomations(): Promise<ReadyItem[]> {
  const out: ReadyItem[] = []
  for (const key of chosenAutomations.value) {
    const tpl = automationTemplate(key)
    if (!tpl) continue
    try {
      await $fetch(`/api/projects/${id.value}/automations`, { method: 'POST', body: automationBody({ ...tpl.make(), enabled: false }) })
      out.push({ kind: 'automation', name: tpl.name, status: 'todo', detail: t('setup.automationPaused') })
    } catch (e) {
      out.push({ kind: 'automation', name: tpl.name, status: 'error', detail: apiError(e) })
    }
  }
  return out
}

// A pick as it will be added: built server-side (the library's agent with
// the edits, internal/perm.Agent for its level), not the AI's raw pick.
const libKey = (p: AgentPick) => p.key || p.id.slice(p.id.indexOf('/') + 1)
const planned = (p: AgentPick) => preview.value?.plan.agents.find(a => a.key === libKey(p))
const levelItems = computed(() => permLevels.map(l => ({ label: l.label, value: l.level, icon: l.icon })))

const readyMeta: Record<ReadyItem['status'], { icon: string, color: string }> = {
  ok: { icon: 'i-lucide-circle-check', color: 'text-(--ui-success)' },
  todo: { icon: 'i-lucide-circle-alert', color: 'text-(--ui-warning)' },
  error: { icon: 'i-lucide-circle-x', color: 'text-(--ui-error)' }
}
const readyKind = computed<Record<string, string>>(() => ({
  agents: t('setup.readyAgents'), workflows: t('setup.readyWorkflows'), skill: 'Skill', mcp: 'MCP', automation: t('setup.readyAutomation'),
  quick_check: t('setup.readyQuickCheck'), command: t('setup.readyCommands')
}))
const needsLabel = computed<Record<string, string>>(() => ({ key: t('setup.needsKey'), login: t('setup.needsLogin') }))

</script>

<template>
  <PageShell :title="t('setup.title', { name: project?.name ?? '' })">
    <template #actions>
      <UButton :to="`/projects/${id}`" icon="i-lucide-arrow-left" :label="t('setup.back')" color="neutral" variant="ghost" />
    </template>

    <div v-if="project" class="mx-auto max-w-5xl space-y-6">
      <UAlert
        v-if="project.agent_count" color="info" variant="subtle" icon="i-lucide-info"
        :description="t('team.setup.hasAgentsWarn', { n: project.agent_count })"
      />

      <!-- step 1 -->
      <UCard>
        <template #header>
          <div class="flex items-center justify-between gap-2">
            <div class="flex items-center gap-2">
              <span class="flex size-6 items-center justify-center rounded-full bg-(--ui-primary) text-xs font-semibold text-white">1</span>
              <p class="font-medium">{{ t('setup.step1') }}</p>
            </div>
            <UButton v-if="project.scope === 'folder'" icon="i-lucide-refresh-cw" size="xs" color="neutral" variant="ghost" :loading="scanning" :label="t('setup.rescan')" @click="runScan" />
          </div>
        </template>

        <div v-if="project.scope === 'machine'" class="text-sm text-(--ui-text-muted)">
          {{ t('setup.machineNoScan') }}
        </div>
        <div v-else-if="scanning && !summary" class="text-sm text-(--ui-text-muted)">{{ t('setup.scanning') }}</div>
        <div v-else-if="summary" class="grid gap-4 md:grid-cols-2">
          <div class="space-y-3 text-sm">
            <div>
              <p class="text-xs text-(--ui-text-muted)">{{ t('setup.stack') }}</p>
              <div class="mt-1 flex flex-wrap gap-1">
                <UBadge v-for="f in summary.frameworks" :key="f" :label="f" variant="subtle" />
                <UBadge v-for="[l, n] in topLanguages" :key="l" :label="`${l} ${n}`" color="neutral" variant="outline" />
              </div>
            </div>
            <div v-if="summary.services.length">
              <p class="text-xs text-(--ui-text-muted)">{{ t('setup.services') }}</p>
              <div class="mt-1 flex flex-wrap gap-1">
                <UBadge v-for="s in summary.services" :key="s" :label="s" color="info" variant="subtle" />
              </div>
            </div>
            <div v-if="summary.infra.length">
              <p class="text-xs text-(--ui-text-muted)">{{ t('setup.infra') }}</p>
              <p>{{ summary.infra.join(' · ') }}</p>
            </div>
            <p class="text-xs text-(--ui-text-muted)">{{ t('setup.fileCount', { n: summary.file_count, manifests: summary.manifests.join(', ') || t('setup.noManifest') }) }}</p>
          </div>
          <div class="text-sm">
            <p class="text-xs text-(--ui-text-muted)">{{ t('setup.agentDocs', { n: summary.agent_docs.length }) }}</p>
            <ul v-if="summary.agent_docs.length" class="mt-1 space-y-1">
              <li v-for="d in summary.agent_docs" :key="d.path" class="flex items-center gap-2">
                <UBadge :label="docKind[d.kind] ?? d.kind" size="sm" color="neutral" variant="soft" />
                <code class="truncate text-xs">{{ d.path }}</code>
              </li>
            </ul>
            <p v-else class="mt-1 text-(--ui-text-muted)">{{ t('setup.noAgentDocs') }}</p>
          </div>
        </div>
      </UCard>

      <!-- step 2 -->
      <UCard>
        <template #header>
          <div class="flex items-center gap-2">
            <span class="flex size-6 items-center justify-center rounded-full bg-(--ui-primary) text-xs font-semibold text-white">2</span>
            <p class="font-medium">{{ t('setup.step2') }}</p>
          </div>
        </template>
        <div class="space-y-3">
          <UFormField :label="project.scope === 'machine' ? t('setup.goalLabelMachine') : t('setup.goalLabelFolder')" :required="project.scope === 'machine'">
            <UTextarea
              v-model="goal" :rows="3" class="w-full"
              :placeholder="t('setup.goalPlaceholder')"
            />
          </UFormField>
          <div class="flex flex-wrap items-center gap-3">
            <UButton
              icon="i-lucide-sparkles" :label="result ? t('setup.analyzeAgain') : t('setup.analyze')" :loading="proposing"
              :disabled="project.scope === 'machine' && !goal.trim()" @click="propose"
            />
            <span v-if="readyProvider" class="text-xs text-(--ui-text-muted)">
              {{ t('setup.usingProvider', { name: readyProvider.name, model: readyProvider.tier_models.strong || '' }) }}
            </span>
          </div>
          <p v-if="proposing" class="text-sm text-(--ui-text-muted)">{{ t('setup.analyzing') }}</p>
        </div>
      </UCard>

      <!-- step 3 -->
      <UCard v-if="result">
        <template #header>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex items-center gap-2">
              <span class="flex size-6 items-center justify-center rounded-full bg-(--ui-primary) text-xs font-semibold text-white">3</span>
              <p class="font-medium">{{ t('setup.step3') }}</p>
            </div>
            <span class="text-xs text-(--ui-text-muted)">
              {{ result.provider }} · {{ result.model }} · {{ result.usage.input_tokens }} in / {{ result.usage.output_tokens }} out
              · {{ Math.round(result.usage.duration_ms / 1000) }}s
              <template v-if="result.usage.cost_usd"> · ${{ result.usage.cost_usd.toFixed(3) }}</template>
            </span>
          </div>
        </template>

        <div class="space-y-6">
          <UFormField>
            <template #label><InfoLabel :label="t('setup.projectDesc')" :info="t('setup.projectDescHelp')" /></template>
            <UTextarea v-model="description" :rows="3" class="w-full" />
          </UFormField>

          <div class="space-y-2">
            <div class="flex flex-wrap items-center gap-2">
              <p class="me-auto text-sm font-medium">{{ t('setup.libAgents', { n: chosenAgents.length, total: picks.length }) }}</p>
              <UBadge :label="t('setup.confidence', { n: Math.round(result.proposal.confidence * 100) })" color="neutral" variant="soft" size="sm" />
            </div>
            <p class="text-sm text-(--ui-text-muted)">{{ result.proposal.reason }}</p>
            <div
              v-for="(p, i) in picks" :key="p.id + i"
              class="flex gap-3 rounded-lg border p-3"
              :class="accepted[i] ? 'border-(--ui-border)' : 'border-dashed border-(--ui-border) opacity-60'"
            >
              <UCheckbox v-model="accepted[i]" class="mt-0.5" />
              <div class="min-w-0 flex-1 space-y-1">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium">{{ planned(p)?.name ?? p.name ?? p.id }}</span>
                  <code class="text-xs text-(--ui-text-muted)">{{ p.id }}</code>
                  <UBadge v-if="accepted[i] && planned(p)?.key === preview?.plan.default" :label="t('team.default')" icon="i-lucide-star" variant="subtle" size="sm" />
                  <UBadge v-if="planned(p)?.model_tier" :label="modelTierLabel[planned(p)!.model_tier!]" size="sm" color="neutral" variant="outline" />
                  <UBadge v-if="planned(p)" :label="permOf(agentLevel(planned(p)!.permissions)).label" :icon="permOf(agentLevel(planned(p)!.permissions)).icon" size="sm" color="neutral" variant="soft" />
                  <UButton
                    v-if="accepted[i]" class="ms-auto" size="xs" color="neutral" variant="ghost" icon="i-lucide-pencil"
                    :label="editing === i ? t('setup.editDone') : t('setup.editPick')" @click="editing = editing === i ? null : i"
                  />
                </div>
                <p v-if="planned(p)?.role" class="text-xs text-(--ui-text-muted)">{{ planned(p)!.role }}</p>
                <p class="text-sm text-(--ui-text-muted)">{{ p.reason }}</p>
                <div v-if="editing === i && accepted[i]" class="grid gap-3 pt-2 sm:grid-cols-2">
                  <UFormField :label="t('org.form.name')">
                    <UInput v-model="p.name" :placeholder="planned(p)?.name" class="w-full" />
                  </UFormField>
                  <UFormField :label="t('setup.pickLevel')">
                    <USelect v-model="p.level" :items="levelItems" :placeholder="planned(p) ? permOf(agentLevel(planned(p)!.permissions)).label : ''" class="w-full" />
                  </UFormField>
                  <UFormField :label="t('setup.instructionsAdded')" class="sm:col-span-2">
                    <UTextarea v-model="p.instructions" :rows="3" autoresize class="w-full" />
                  </UFormField>
                </div>
                <details v-else-if="p.instructions" class="text-sm">
                  <summary class="cursor-pointer text-xs text-(--ui-text-muted)">{{ t('setup.instructionsAdded') }}</summary>
                  <p class="mt-1 whitespace-pre-wrap rounded bg-(--ui-bg-muted) p-2 text-xs">{{ p.instructions }}</p>
                </details>
              </div>
            </div>
            <UAlert v-if="preview?.problems.length" color="error" variant="subtle" :title="t('setup.invalidTitle')">
              <template #description>
                <ul class="list-disc ps-4">
                  <li v-for="x in preview.problems" :key="x">{{ x }}</li>
                </ul>
              </template>
            </UAlert>
            <UAlert v-if="preview?.warnings.length" color="warning" variant="subtle" :title="t('setup.warningsTitle')">
              <template #description>
                <ul class="list-disc ps-4">
                  <li v-for="w in preview.warnings" :key="w">{{ w }}</li>
                </ul>
              </template>
            </UAlert>
          </div>

          <div v-if="result.proposal.workflows.length || result.proposal.automations.length" class="space-y-2">
            <p class="text-sm font-medium">{{ t('setup.libProcesses', { n: chosenWorkflows.length + chosenAutomations.length, total: result.proposal.workflows.length + result.proposal.automations.length }) }}</p>
            <div
              v-for="(w, i) in result.proposal.workflows" :key="`w-${w.name}`" class="flex gap-3 rounded-lg border p-3"
              :class="workflowsAccepted[i] ? 'border-(--ui-border)' : 'border-dashed border-(--ui-border) opacity-60'"
            >
              <UCheckbox v-model="workflowsAccepted[i]" class="mt-0.5" />
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <UBadge :label="t('setup.kindWorkflow')" color="neutral" variant="outline" size="sm" />
                  <span class="font-medium">{{ workflowName(w.name) }}</span>
                </div>
                <p class="text-sm text-(--ui-text-muted)">{{ w.reason }}</p>
              </div>
            </div>
            <div
              v-for="(a, i) in result.proposal.automations" :key="`a-${a.name}`" class="flex gap-3 rounded-lg border p-3"
              :class="automationsAccepted[i] ? 'border-(--ui-border)' : 'border-dashed border-(--ui-border) opacity-60'"
            >
              <UCheckbox v-model="automationsAccepted[i]" class="mt-0.5" />
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <UBadge :label="t('setup.kindAutomation')" color="neutral" variant="outline" size="sm" />
                  <span class="font-medium">{{ automationTemplate(a.name)?.name ?? a.name }}</span>
                  <UBadge :label="t('setup.automationPausedBadge')" color="warning" variant="subtle" size="sm" />
                </div>
                <p class="text-sm text-(--ui-text-muted)">{{ a.reason }}</p>
              </div>
            </div>
          </div>

          <div v-if="result.proposal.skills.length || result.proposal.mcp.length" class="space-y-2">
            <p class="text-sm font-medium">{{ t('setup.tools', { n: chosenSkills.length + chosenMCP.length, total: result.proposal.skills.length + result.proposal.mcp.length }) }}</p>
            <div
              v-for="(p, i) in result.proposal.skills" :key="`s-${p.name}`" class="flex gap-3 rounded-lg border p-3"
              :class="skillsAccepted[i] ? 'border-(--ui-border)' : 'border-dashed border-(--ui-border) opacity-60'"
            >
              <UCheckbox v-model="skillsAccepted[i]" class="mt-0.5" />
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <UBadge :label="t('setup.docSkill')" color="neutral" variant="outline" size="sm" />
                  <span class="font-medium">{{ p.name }}</span>
                </div>
                <p class="text-sm text-(--ui-text-muted)">{{ p.reason }}</p>
              </div>
            </div>
            <div
              v-for="(p, i) in result.proposal.mcp" :key="`m-${p.name}`" class="flex gap-3 rounded-lg border p-3"
              :class="mcpAccepted[i] ? 'border-(--ui-border)' : 'border-dashed border-(--ui-border) opacity-60'"
            >
              <UCheckbox v-model="mcpAccepted[i]" class="mt-0.5" />
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <UBadge label="MCP" color="neutral" variant="outline" size="sm" />
                  <span class="font-medium">{{ p.name }}</span>
                  <UBadge v-if="p.needs" :label="needsLabel[p.needs]" color="warning" variant="subtle" size="sm" />
                </div>
                <p class="text-sm text-(--ui-text-muted)">{{ p.reason }}</p>
              </div>
            </div>
          </div>

          <div class="space-y-2">
            <div class="flex items-center gap-2">
              <UCheckbox v-model="useQuickChecks" />
              <p class="text-sm font-medium">{{ t('setup.quickChecks') }}</p>
            </div>
            <UTextarea
              v-if="useQuickChecks" v-model="quickChecks" :rows="2" autoresize class="w-full font-mono text-xs"
              :placeholder="t('setup.quickChecksPlaceholder')"
            />
          </div>

          <UAlert v-if="result.proposal.notes.length" color="info" variant="subtle" icon="i-lucide-lightbulb" :title="t('setup.moreSuggestions')">
            <template #description>
              <ul class="list-disc ps-4">
                <li v-for="n in result.proposal.notes" :key="n">{{ n }}</li>
              </ul>
            </template>
          </UAlert>

          <div v-if="ready" class="space-y-2 rounded-lg border border-(--ui-border) p-3">
            <p class="text-sm font-medium">{{ t('setup.readyTitle') }}</p>
            <ul class="space-y-1 text-sm">
              <li v-for="(r, i) in ready" :key="i" class="flex items-start gap-2">
                <UIcon :name="readyMeta[r.status].icon" class="mt-0.5 size-4 shrink-0" :class="readyMeta[r.status].color" />
                <span class="font-medium">{{ readyKind[r.kind] ?? r.kind }}<template v-if="r.kind === 'skill' || r.kind === 'mcp' || r.kind === 'automation'">{{ ` ${r.name}` }}</template></span>
                <span v-if="r.detail" class="min-w-0 break-words text-(--ui-text-muted)">{{ r.detail }}</span>
              </li>
            </ul>
            <div class="flex justify-end">
              <UButton :to="`/projects/${id}`" icon="i-lucide-arrow-right" :label="t('setup.openProject')" />
            </div>
          </div>

          <div v-else class="flex justify-end gap-2">
            <UButton :to="`/projects/${id}`" color="neutral" variant="ghost" :label="t('common.cancel')" />
            <UButton icon="i-lucide-check" :label="t('setup.applySetup')" :loading="applying" :disabled="!!preview?.problems.length" @click="apply" />
          </div>
        </div>
      </UCard>
    </div>
  </PageShell>
</template>
