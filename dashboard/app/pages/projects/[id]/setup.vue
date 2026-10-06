<script setup lang="ts">
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
interface AgentChange {
  action: 'update' | 'add' | 'remove'
  key: string
  name?: string
  tier?: AgentTier
  role?: string
  reports_to?: string[]
  model_tier?: ModelTier
  instructions?: string
  tools?: string[]
  source?: string
  reason: string
}
interface TemplateSpec {
  key: string
  name: string
  kind: string
  agents: { key: string, name: string, tier: AgentTier, role?: string }[]
}
interface Proposal {
  description: string
  template_key: string
  reason: string
  confidence: number
  agent_changes: AgentChange[]
  notes: string[]
}
interface ProposeResult {
  proposal: Proposal
  template: TemplateSpec
  problems: string[]
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
const _f3 = useLiveFetch<{ templates: OrgModel[] }>('/api/templates')
const { data: tplData } = _f3
await Promise.all([_f1, _f2, _f3]) // started together: one round trip, not 3 (a phone over a VPN)
const project = computed(() => projData.value?.project)
const templates = computed(() => tplData.value?.templates ?? [])
const readyProvider = computed(() => provData.value?.providers.find(p => p.is_default && p.status === 'ok')
  ?? provData.value?.providers.find(p => p.status === 'ok'))

function goConnect() {
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
const templateKey = ref('')
const accepted = ref<boolean[]>([])
const preview = ref<{ template: TemplateSpec, problems: string[] } | null>(null)

async function propose() {
  proposing.value = true
  try {
    const res = await $fetch<{ result: ProposeResult }>(`/api/projects/${id.value}/setup/propose`, { method: 'POST', body: { goal: goal.value } })
    result.value = res.result
    description.value = res.result.proposal.description
    templateKey.value = res.result.proposal.template_key
    accepted.value = res.result.proposal.agent_changes.map(() => true)
    preview.value = { template: res.result.template, problems: res.result.problems }
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

const chosenChanges = computed(() => (result.value?.proposal.agent_changes ?? []).filter((_, i) => accepted.value[i]))

// Rebuild the preview whenever the template or ticked changes move.
watch([templateKey, accepted], async () => {
  if (!result.value) return
  try {
    preview.value = await $fetch(`/api/projects/${id.value}/setup/build`, {
      method: 'POST', body: { template_key: templateKey.value, changes: chosenChanges.value }
    })
  } catch (e) {
    toast.add({ title: apiError(e), color: 'error' })
  }
}, { deep: true })

const templateId = computed({
  get: () => templates.value.find(t => t.key === templateKey.value)?.id ?? '',
  set: (v: string) => { templateKey.value = templates.value.find(t => t.id === v)?.key ?? templateKey.value }
})

const applying = ref(false)
async function apply() {
  applying.value = true
  try {
    await $fetch(`/api/projects/${id.value}/setup/apply`, {
      method: 'POST', body: { template_key: templateKey.value, changes: chosenChanges.value, description: description.value }
    })
    toast.add({ title: t('setup.applyDone'), color: 'success' })
    await navigateTo(`/projects/${id.value}`)
  } catch (e) {
    const d = (e as { data?: { problems?: string[] } }).data
    toast.add({ title: t('setup.applyFailed'), description: d?.problems?.join('; ') ?? apiError(e), color: 'error' })
  } finally {
    applying.value = false
  }
}

const actionMeta = computed<Record<string, { label: string, color: 'info' | 'success' | 'error' }>>(() => ({
  update: { label: t('setup.actionUpdate'), color: 'info' },
  add: { label: t('setup.actionAdd'), color: 'success' },
  remove: { label: t('setup.actionRemove'), color: 'error' }
}))
const previewRows = computed(() => (['lead', 'manager', 'worker'] as const)
  .map(t => ({ tier: t, agents: preview.value?.template.agents.filter(a => a.tier === t) ?? [] }))
  .filter(r => r.agents.length))
</script>

<template>
  <PageShell :title="t('setup.title', { name: project?.name ?? '' })">
    <template #actions>
      <UButton :to="`/projects/${id}`" icon="i-lucide-arrow-left" :label="t('setup.back')" color="neutral" variant="ghost" />
    </template>

    <div v-if="project" class="mx-auto max-w-5xl space-y-6">
      <UAlert
        v-if="project.model" color="warning" variant="subtle" icon="i-lucide-triangle-alert"
        :description="t('setup.currentModelWarn', { model: project.model.name })"
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
          <UFormField :label="t('setup.projectDesc')" :help="t('setup.projectDescHelp')">
            <UTextarea v-model="description" :rows="3" class="w-full" />
          </UFormField>

          <div class="grid gap-6 lg:grid-cols-2">
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <p class="text-sm font-medium">{{ t('setup.model') }}</p>
                <UBadge :label="t('setup.confidence', { n: Math.round(result.proposal.confidence * 100) })" color="neutral" variant="soft" size="sm" />
              </div>
              <p class="text-sm text-(--ui-text-muted)">{{ result.proposal.reason }}</p>
              <TemplatePicker v-model="templateId" :templates="templates" />
            </div>

            <div class="space-y-2">
              <p class="text-sm font-medium">{{ t('setup.preview') }}</p>
              <div class="space-y-2 rounded-lg border border-(--ui-border) p-3">
                <div v-for="row in previewRows" :key="row.tier">
                  <p class="text-xs uppercase text-(--ui-text-muted)">{{ tierLabel[row.tier] }}</p>
                  <div class="mt-1 flex flex-wrap gap-1">
                    <UBadge v-for="a in row.agents" :key="a.key" :label="a.name" color="neutral" variant="outline" />
                  </div>
                </div>
              </div>
              <UAlert v-if="preview?.problems.length" color="error" variant="subtle" :title="t('setup.invalidTitle')">
                <template #description>
                  <ul class="list-disc ps-4">
                    <li v-for="p in preview.problems" :key="p">{{ p }}</li>
                  </ul>
                </template>
              </UAlert>
            </div>
          </div>

          <div v-if="result.proposal.agent_changes.length" class="space-y-2">
            <p class="text-sm font-medium">{{ t('setup.refineAgents', { n: chosenChanges.length, total: result.proposal.agent_changes.length }) }}</p>
            <div
              v-for="(c, i) in result.proposal.agent_changes" :key="i"
              class="flex gap-3 rounded-lg border p-3"
              :class="accepted[i] ? 'border-(--ui-border)' : 'border-dashed border-(--ui-border) opacity-60'"
            >
              <UCheckbox v-model="accepted[i]" class="mt-0.5" />
              <div class="min-w-0 flex-1 space-y-1">
                <div class="flex flex-wrap items-center gap-2">
                  <UBadge :label="actionMeta[c.action]?.label ?? c.action" :color="actionMeta[c.action]?.color ?? 'neutral'" variant="subtle" size="sm" />
                  <span class="font-medium">{{ c.name || c.key }}</span>
                  <code class="text-xs text-(--ui-text-muted)">{{ c.key }}</code>
                  <UBadge v-if="c.tier" :label="tierLabel[c.tier]" size="sm" color="neutral" variant="outline" />
                </div>
                <p class="text-sm text-(--ui-text-muted)">{{ c.reason }}</p>
                <p v-if="c.source" class="text-xs">{{ t('setup.fromFilePrefix') }} <code>{{ c.source }}</code></p>
                <details v-if="c.instructions" class="text-sm">
                  <summary class="cursor-pointer text-xs text-(--ui-text-muted)">
                    {{ c.action === 'update' ? t('setup.instructionsAdded') : t('setup.instructions') }}
                  </summary>
                  <p class="mt-1 whitespace-pre-wrap rounded bg-(--ui-bg-muted) p-2 text-xs">{{ c.instructions }}</p>
                </details>
              </div>
            </div>
          </div>

          <UAlert v-if="result.proposal.notes.length" color="info" variant="subtle" icon="i-lucide-lightbulb" :title="t('setup.moreSuggestions')">
            <template #description>
              <ul class="list-disc ps-4">
                <li v-for="n in result.proposal.notes" :key="n">{{ n }}</li>
              </ul>
            </template>
          </UAlert>

          <div class="flex justify-end gap-2">
            <UButton :to="`/projects/${id}`" color="neutral" variant="ghost" :label="t('common.cancel')" />
            <UButton icon="i-lucide-check" :label="t('setup.applySetup')" :loading="applying" :disabled="!!preview?.problems.length" @click="apply" />
          </div>
        </div>
      </UCard>
    </div>
  </PageShell>
</template>
