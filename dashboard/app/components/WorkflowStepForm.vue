<script setup lang="ts">
// The canvas's right panel (ADR-108): the selected step's form, the Input
// node's inputs, or (nothing selected) the workflow's name. Fields the office
// would refuse are marked; every change is a new def.
import type { MessageKey } from '~/locales/vi'

const props = defineProps<{
  def: WorkflowDef
  sel: string // '' | '__input' | a step's id
  projectId?: string
  bindings?: Record<string, string>
  agents: Agent[]
  workflows: { key: string, name: string, inputs: WorkflowField[] }[]
}>()
const emit = defineEmits<{ 'update:def': [WorkflowDef], 'update:bindings': [Record<string, string>], 'select': [string] }>()
const { t } = useLang()
const NONE = '_'

const steps = computed(() => props.def.steps ?? [])
const step = computed(() => steps.value.find(s => s.id === props.sel))
const missing = computed(() => step.value ? stepMissing(step.value, props.def) : [])
const miss = (f: string) => missing.value.includes(f) ? t('wf.step.missing') : undefined
const ui = { hint: 'text-(--ui-error) text-xs' }

const setDef = (p: Partial<WorkflowDef>) => emit('update:def', { ...props.def, ...p })
function patch(p: Partial<WorkflowStep>) {
  const id = props.sel
  setDef({ steps: steps.value.map(s => s.id === id ? { ...s, ...p } : s) })
}

// the id: renamed on Enter or leaving the field, links and templates follow
const idDraft = ref('')
watch(() => props.sel, () => { idDraft.value = step.value?.id ?? '' }, { immediate: true })
const idBad = computed(() => idDraft.value !== step.value?.id && (!validStepId(idDraft.value) || steps.value.some(s => s.id === idDraft.value)))
function commitId() {
  const from = step.value?.id
  if (!from || idDraft.value === from) return
  if (idBad.value) { idDraft.value = from; return }
  setDef({ steps: renameStep(steps.value, from, idDraft.value), start: props.def.start?.map(x => x === from ? idDraft.value : x) })
  emit('select', idDraft.value)
}

function remove() {
  const id = props.sel
  const rest = steps.value.filter(s => s.id !== id).map(s => unlink(s, [id]))
  setDef({ steps: rest.length ? rest : [{ id: 'end', type: 'end' }], start: props.def.start?.filter(x => x !== id) })
  emit('select', '')
}

// where an output may go: one step or several (they run at once)
const targetItems = computed(() => steps.value.filter(s => s.id !== props.sel).map(s => ({ label: s.name ? `${s.name} · ${s.id}` : s.id, value: s.id, icon: stepIcon(s.type) })))
const link = (f: StepLink) => step.value ? linksOf(step.value, f) : []
function setLink(f: StepLink, v: unknown) {
  const id = props.sel
  setDef({ steps: steps.value.map(s => s.id === id ? withLinks(s, f, (v as string[]) ?? []) : s) })
}

// ---- switch: its cases ----
const cases = computed(() => step.value?.cases ?? [])
const setCases = (list: WorkflowCase[]) => patch({ cases: list })

// the templates usable here: the inputs, and the steps that may run before it
const vars = computed(() => {
  const before = new Set<string>()
  const preds = new Map<string, string[]>()
  for (const s of steps.value) {
    for (const to of stepOuts(s)) preds.set(to, [...(preds.get(to) ?? []), s.id])
  }
  const queue = [props.sel]
  while (queue.length) {
    for (const p of preds.get(queue.shift()!) ?? []) {
      if (!before.has(p)) { before.add(p); queue.push(p) }
    }
  }
  before.delete(props.sel)
  return [
    ...(props.def.inputs ?? []).map(f => `{{input.${f.key}}}`),
    ...steps.value.filter(s => before.has(s.id)).map(s => `{{steps.${s.id}.output}}`)
  ]
})

// ---- agent: its role, the role's access and agent ----
const roleItems = computed(() => props.def.roles.filter(r => !r.workflow).map(r => ({ label: `${r.name} · ${r.key}`, value: r.key })))
const role = computed(() => props.def.roles.find(r => r.key === step.value?.role))
function patchRole(p: Partial<WorkflowRole>) {
  const key = role.value?.key
  setDef({ roles: props.def.roles.map(r => r.key === key ? { ...r, ...p } : r) })
}
function newRole() {
  let k = 'role'
  for (let i = 2; props.def.roles.some(r => r.key === k); i++) k = `role-${i}`
  const id = props.sel
  setDef({ roles: [...props.def.roles, { key: k, name: k }], steps: steps.value.map(s => s.id === id ? { ...s, role: k } : s) })
}
const accessItems = computed(() => (['agent', 'analyze', 'propose', 'edit'] as const).map(a => ({ label: t(`wf.access.${a}` as MessageKey), value: a, icon: accessIcon[a] })))
const agentItems = computed(() => [{ label: t('wf.step.unbound'), value: NONE }, ...props.agents.map(a => ({ label: a.name, value: a.id }))])
const boundAgent = computed(() => (role.value && props.bindings?.[role.value.key]) || NONE)
function bind(v: string) {
  if (!role.value) return
  const b = { ...(props.bindings ?? {}) }
  if (v === NONE) delete b[role.value.key]
  else b[role.value.key] = v
  emit('update:bindings', b)
}

// ---- coordinate: its roles, who coordinates ----
const coordItems = computed(() => [{ label: t('wf.step.runCoordinator'), value: NONE }, ...roleItems.value])
function newCoordRole() {
  let k = 'role'
  for (let i = 2; props.def.roles.some(r => r.key === k); i++) k = `role-${i}`
  const id = props.sel
  setDef({ roles: [...props.def.roles, { key: k, name: k }], steps: steps.value.map(s => s.id === id ? { ...s, roles: [...(s.roles ?? []), k] } : s) })
}

// ---- workflow: which one, its inputs ----
const wfItems = computed(() => {
  const list = props.workflows.map(w => ({ label: `${w.name} · /${w.key}`, value: w.key }))
  const cur = step.value?.workflow
  if (cur && !list.some(i => i.value === cur)) list.unshift({ label: `#${cur}`, value: cur })
  return list
})
const child = computed(() => props.workflows.find(w => w.key === step.value?.workflow))
function setWorkflow(key: string) {
  const w = props.workflows.find(x => x.key === key)
  const inputs = { ...Object.fromEntries((w?.inputs ?? []).map(f => [f.key, ''])), ...(step.value?.inputs ?? {}) }
  patch({ workflow: key, inputs })
}

const LANGS = ['bash', 'node', 'python']
const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']
const onErrorItems = computed(() => [{ label: t('wf.step.onErrorStop'), value: 'stop' }, { label: t('wf.step.onErrorContinue'), value: 'continue' }])
</script>

<template>
  <!-- the Input node: what a caller gives -->
  <div v-if="sel === '__input'" class="space-y-2 text-sm">
    <p class="flex items-center gap-1.5 font-medium">
      <UIcon name="i-lucide-log-in" class="size-4 text-primary" />{{ t('wf.inputs') }}
      <InfoTip :text="t('wf.canvas.inputsInfo')" icon-class="size-3.5 text-(--ui-text-muted)" />
    </p>
    <WorkflowFieldsEditor :model-value="def.inputs" @update:model-value="v => setDef({ inputs: v })" />
  </div>

  <!-- a step -->
  <div v-else-if="step" :key="step.id" class="space-y-3 text-sm">
    <div class="flex items-center gap-1.5">
      <UIcon :name="stepIcon(step.type)" class="size-4 text-primary" />
      <span class="font-medium">{{ t(`wf.step.type.${step.type}` as MessageKey) }}</span>
      <UButton class="ms-auto" size="xs" color="error" variant="ghost" icon="i-lucide-trash-2" :aria-label="t('wf.delete')" @click="remove()" />
    </div>
    <div class="grid grid-cols-2 gap-2">
      <UFormField :label="t('wf.step.id')" :hint="idBad ? t('wf.step.idBad') : miss('id')" :ui="ui">
        <UInput v-model="idDraft" size="sm" class="w-full font-mono" :color="idBad ? 'error' : undefined" @blur="commitId()" @keydown.enter="commitId()" />
      </UFormField>
      <UFormField :label="t('wf.step.name')">
        <UInput :model-value="step.name ?? ''" size="sm" class="w-full" @update:model-value="v => patch({ name: String(v) || undefined })" />
      </UFormField>
    </div>

    <!-- agent -->
    <template v-if="step.type === 'agent'">
      <UFormField :label="t('wf.step.role')" :hint="miss('role')" :ui="ui">
        <div class="flex gap-1">
          <USelect :model-value="step.role || undefined" :items="roleItems" size="sm" class="min-w-0 flex-1" :placeholder="t('wf.step.pickRole')" @update:model-value="v => patch({ role: String(v) })" />
          <UTooltip :text="t('wf.step.newRole')"><UButton size="sm" color="neutral" variant="outline" icon="i-lucide-plus" @click="newRole()" /></UTooltip>
        </div>
      </UFormField>
      <div v-if="role" class="space-y-2 rounded-md border border-(--ui-border) p-2">
        <div class="grid grid-cols-2 gap-2">
          <UInput :model-value="role.name" size="xs" :placeholder="t('wf.step.roleName')" @update:model-value="v => patchRole({ name: String(v) })" />
          <USelect :model-value="accessKey(role.access)" :items="accessItems" size="xs" @update:model-value="v => patchRole({ access: v === 'agent' ? undefined : v as WorkflowAccess })" />
        </div>
        <UFormField v-if="projectId" :label="t('wf.step.agent')" size="xs">
          <USelect :model-value="boundAgent" :items="agentItems" size="xs" class="w-full" @update:model-value="v => bind(String(v))" />
        </UFormField>
      </div>
      <UFormField :label="t('wf.step.prompt')" :hint="miss('prompt')" :ui="ui">
        <UTextarea :model-value="step.prompt ?? ''" :rows="5" autoresize :maxrows="16" class="w-full" @update:model-value="v => patch({ prompt: String(v) })" />
      </UFormField>
      <UFormField>
        <template #label><InfoLabel :label="t('wf.step.skill')" :info="t('wf.step.skillInfo')" /></template>
        <UInput :model-value="step.skill ?? ''" size="sm" class="w-full font-mono" :placeholder="t('wf.step.skillPlaceholder')" @update:model-value="v => patch({ skill: String(v).trim() || undefined })" />
      </UFormField>
    </template>

    <!-- coordinate: an agent decides inside the step (ADR-111) -->
    <template v-else-if="step.type === 'coordinate'">
      <UFormField :hint="miss('roles')" :ui="ui">
        <template #label><InfoLabel :label="t('wf.step.coordRoles')" :info="t('wf.step.coordRolesInfo')" /></template>
        <div class="flex gap-1">
          <USelect
            multiple :model-value="step.roles ?? []" :items="roleItems.filter(r => r.value !== step!.role)" size="sm" class="min-w-0 flex-1" :placeholder="t('wf.step.allRoles')"
            @update:model-value="v => patch({ roles: (v as string[]).length ? v as string[] : undefined })"
          />
          <UTooltip :text="t('wf.step.newRole')"><UButton size="sm" color="neutral" variant="outline" icon="i-lucide-plus" @click="newCoordRole()" /></UTooltip>
        </div>
      </UFormField>
      <UFormField :label="t('wf.step.coordinator')" :hint="miss('role')" :ui="ui">
        <USelect :model-value="step.role || NONE" :items="coordItems" size="sm" class="w-full" @update:model-value="v => patch({ role: v === NONE ? undefined : String(v), roles: step!.roles?.filter(k => k !== v) })" />
      </UFormField>
      <UFormField :hint="miss('prompt')" :ui="ui">
        <template #label><InfoLabel :label="t('wf.step.coordPrompt')" :info="t('wf.step.coordInfo')" /></template>
        <UTextarea :model-value="step.prompt ?? ''" :rows="6" autoresize :maxrows="20" class="w-full" @update:model-value="v => patch({ prompt: String(v) })" />
      </UFormField>
    </template>

    <!-- a sub-workflow -->
    <template v-else-if="step.type === 'workflow'">
      <UFormField :label="t('wf.step.workflow')" :hint="miss('workflow')" :ui="ui">
        <USelect :model-value="step.workflow || undefined" :items="wfItems" size="sm" class="w-full" :placeholder="t('wf.step.pickWorkflow')" @update:model-value="v => setWorkflow(String(v))" />
      </UFormField>
      <UFormField :label="t('wf.inputs')">
        <WorkflowKvEditor :model-value="step.inputs" :fixed="child?.inputs.map(f => f.key)" :required="child?.inputs.filter(f => f.required).map(f => f.key)" @update:model-value="v => patch({ inputs: v })" />
      </UFormField>
    </template>

    <!-- code -->
    <template v-else-if="step.type === 'code'">
      <div class="grid grid-cols-2 gap-2">
        <UFormField :label="t('wf.step.lang')" :hint="miss('lang')" :ui="ui">
          <USelect :model-value="step.lang || undefined" :items="LANGS" size="sm" class="w-full" @update:model-value="v => patch({ lang: v as WorkflowStep['lang'] })" />
        </UFormField>
        <UFormField :label="t('wf.step.timeout')">
          <UInputNumber :model-value="step.timeout_s ?? 0" :min="0" size="sm" class="w-full" @update:model-value="v => patch({ timeout_s: v || undefined })" />
        </UFormField>
      </div>
      <UFormField :hint="miss('script')" :ui="ui">
        <template #label><InfoLabel :label="t('wf.step.script')" :info="t('wf.step.codeInfo')" /></template>
        <UTextarea :model-value="step.script ?? ''" :rows="8" autoresize :maxrows="24" class="w-full font-mono text-xs" @update:model-value="v => patch({ script: String(v) })" />
      </UFormField>
    </template>

    <!-- http -->
    <template v-else-if="step.type === 'http'">
      <div class="flex gap-2">
        <USelect :model-value="(step.method || 'GET').toUpperCase()" :items="METHODS" size="sm" class="w-24" @update:model-value="v => patch({ method: String(v) })" />
        <UFormField class="min-w-0 flex-1" :hint="miss('url')" :ui="ui">
          <UInput :model-value="step.url ?? ''" size="sm" class="w-full font-mono" placeholder="https://…" @update:model-value="v => patch({ url: String(v) })" />
        </UFormField>
      </div>
      <UFormField :label="t('wf.step.headers')">
        <WorkflowKvEditor :model-value="step.headers" key-placeholder="Header" @update:model-value="v => patch({ headers: Object.keys(v).length ? v : undefined })" />
      </UFormField>
      <UFormField :label="t('wf.step.body')">
        <UTextarea :model-value="step.body ?? ''" :rows="4" autoresize :maxrows="16" class="w-full font-mono text-xs" @update:model-value="v => patch({ body: String(v) || undefined })" />
      </UFormField>
      <UFormField :label="t('wf.step.timeout')">
        <UInputNumber :model-value="step.timeout_s ?? 0" :min="0" size="sm" class="w-32" @update:model-value="v => patch({ timeout_s: v || undefined })" />
      </UFormField>
    </template>

    <!-- condition -->
    <template v-else-if="step.type === 'condition'">
      <UFormField :hint="miss('if')" :ui="ui">
        <template #label><InfoLabel :label="t('wf.step.if')" :info="t('wf.step.ifInfo')" /></template>
        <UInput :model-value="step.if ?? ''" size="sm" class="w-full font-mono" placeholder="{{steps.a.status}} == 200" @update:model-value="v => patch({ if: String(v) })" />
      </UFormField>
      <div class="grid grid-cols-2 gap-2">
        <UFormField :label="t('wf.canvas.yes')" :hint="miss('then')" :ui="ui">
          <USelect multiple :model-value="link('then')" :items="targetItems" size="sm" class="w-full" :placeholder="t('wf.step.none')" @update:model-value="v => setLink('then', v)" />
        </UFormField>
        <UFormField :label="t('wf.canvas.no')" :hint="miss('else')" :ui="ui">
          <USelect multiple :model-value="link('else')" :items="targetItems" size="sm" class="w-full" :placeholder="t('wf.step.none')" @update:model-value="v => setLink('else', v)" />
        </UFormField>
      </div>
      <UFormField :label="t('wf.step.maxLoops')">
        <template #hint><InfoTip :text="t('wf.step.maxLoopsInfo')" icon-class="size-3.5 text-(--ui-text-muted)" /></template>
        <UInputNumber :model-value="step.max_loops ?? 0" :min="0" :max="50" size="sm" class="w-32" @update:model-value="v => patch({ max_loops: v || undefined })" />
      </UFormField>
    </template>

    <!-- switch: the value, a case per way out -->
    <template v-else-if="step.type === 'switch'">
      <UFormField :hint="miss('value')" :ui="ui">
        <template #label><InfoLabel :label="t('wf.step.value')" :info="t('wf.step.valueInfo')" /></template>
        <UInput :model-value="step.value ?? ''" size="sm" class="w-full font-mono" placeholder="{{steps.a.json.kind}}" @update:model-value="v => patch({ value: String(v) })" />
      </UFormField>
      <UFormField :label="t('wf.step.cases')" :hint="miss('cases')" :ui="ui">
        <div class="space-y-1.5">
          <div v-for="(c, i) in cases" :key="i" class="flex items-start gap-1">
            <UInput
              :model-value="c.when" size="sm" class="w-24 shrink-0 font-mono" :color="missing.includes(`when:${i}`) ? 'error' : undefined" :placeholder="t('wf.step.when')"
              @update:model-value="v => setCases(cases.map((x, j) => j === i ? { ...x, when: String(v) } : x))"
            />
            <USelect
              multiple :model-value="link(`case:${i}`)" :items="targetItems" size="sm" class="min-w-0 flex-1" :color="missing.includes(`case:${i}`) ? 'error' : undefined" :placeholder="t('wf.step.none')"
              @update:model-value="v => setLink(`case:${i}`, v)"
            />
            <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-x" :aria-label="t('wf.delete')" @click="setCases(cases.filter((_, j) => j !== i))" />
          </div>
          <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-plus" :label="t('wf.step.addCase')" @click="setCases([...cases, { when: '', next: [] }])" />
        </div>
      </UFormField>
      <UFormField :label="t('wf.canvas.otherwise')" :hint="miss('else')" :ui="ui">
        <USelect multiple :model-value="link('else')" :items="targetItems" size="sm" class="w-full" :placeholder="t('wf.step.none')" @update:model-value="v => setLink('else', v)" />
      </UFormField>
    </template>

    <!-- wait -->
    <template v-else-if="step.type === 'wait'">
      <UFormField :hint="miss('for')" :ui="ui">
        <template #label><InfoLabel :label="t('wf.step.waitFor')" :info="t('wf.step.waitInfo')" /></template>
        <UInput :model-value="step.for ?? ''" size="sm" class="w-32 font-mono" placeholder="30m" @update:model-value="v => patch({ for: String(v) })" />
      </UFormField>
    </template>

    <!-- approve / check -->
    <template v-else-if="step.type === 'approve' || step.type === 'check'">
      <UFormField v-if="step.type === 'approve'" :label="t('wf.step.note')" :hint="miss('note')" :ui="ui">
        <UTextarea :model-value="step.note ?? ''" :rows="3" autoresize class="w-full" @update:model-value="v => patch({ note: String(v) })" />
      </UFormField>
      <UFormField v-else :label="t('wf.step.command')" :hint="miss('command')" :ui="ui">
        <UInput :model-value="step.command ?? ''" size="sm" class="w-full font-mono" placeholder="pnpm test" @update:model-value="v => patch({ command: String(v) })" />
      </UFormField>
    </template>

    <!-- end -->
    <template v-else-if="step.type === 'end'">
      <UFormField :label="t('wf.step.summary')">
        <UTextarea :model-value="step.summary ?? ''" :rows="3" autoresize class="w-full" @update:model-value="v => patch({ summary: String(v) || undefined })" />
      </UFormField>
      <UFormField v-if="def.outputs?.length" :label="t('wf.step.outputValues')">
        <WorkflowKvEditor
          :model-value="step.outputs" locked :fixed="def.outputs.map(f => f.key)" :required="def.outputs.filter(f => f.required).map(f => f.key)"
          @update:model-value="v => patch({ outputs: Object.fromEntries(Object.entries(v).filter(([k]) => def.outputs?.some(f => f.key === k))) })"
        />
      </UFormField>
      <UFormField :label="t('wf.outputs')">
        <template #hint><InfoTip :text="t('wf.canvas.outputsInfo')" icon-class="size-3.5 text-(--ui-text-muted)" /></template>
        <WorkflowFieldsEditor :model-value="def.outputs" with-type @update:model-value="v => setDef({ outputs: v })" />
      </UFormField>
    </template>

    <!-- where it goes next -->
    <div v-if="step.type !== 'end' && step.type !== 'condition' && step.type !== 'switch'" class="grid grid-cols-2 gap-2">
      <UFormField :label="t('wf.step.next')" :hint="miss('next')" :ui="ui">
        <USelect multiple :model-value="link('next')" :items="targetItems" size="sm" class="w-full" :placeholder="t('wf.step.none')" @update:model-value="v => setLink('next', v)" />
      </UFormField>
      <UFormField v-if="step.type === 'approve' || step.type === 'check'" :label="t('wf.canvas.notPassed')" :hint="miss('else')" :ui="ui">
        <USelect multiple :model-value="link('else')" :items="targetItems" size="sm" class="w-full" :placeholder="t('wf.step.none')" @update:model-value="v => setLink('else', v)" />
      </UFormField>
    </div>
    <UFormField v-if="step.type !== 'end' && step.type !== 'condition' && step.type !== 'switch'" :label="t('wf.step.onError')">
      <USelect :model-value="step.on_error || 'stop'" :items="onErrorItems" size="sm" class="w-full" @update:model-value="v => patch({ on_error: v === 'stop' ? undefined : v as WorkflowStep['on_error'] })" />
    </UFormField>

    <div class="space-y-1 border-t border-(--ui-border) pt-2">
      <p class="flex items-center gap-1 text-xs text-(--ui-text-muted)">
        {{ t('wf.step.vars') }}
        <InfoTip :text="t('wf.step.varsInfo')" icon-class="size-3.5" />
      </p>
      <div class="flex flex-wrap gap-1">
        <code v-for="v in vars" :key="v" class="rounded bg-(--ui-bg-elevated) px-1 font-mono text-[11px]">{{ v }}</code>
        <span v-if="!vars.length" class="text-xs text-(--ui-text-dimmed)">{{ t('wf.step.noVars') }}</span>
      </div>
    </div>
  </div>

  <!-- nothing selected: the workflow itself -->
  <div v-else class="space-y-3 text-sm">
    <p class="text-xs text-(--ui-text-muted)">{{ t('wf.canvas.pickNode') }}</p>
    <UFormField :label="t('wf.canvas.key')" :hint="validStepKey(def.key) ? undefined : t('wf.step.missing')" :ui="ui">
      <UInput :model-value="def.key" size="sm" class="w-full font-mono" @update:model-value="v => setDef({ key: String(v).trim() })" />
    </UFormField>
    <UFormField :label="t('wf.canvas.name')" :hint="def.name.trim() ? undefined : t('wf.step.missing')" :ui="ui">
      <UInput :model-value="def.name" size="sm" class="w-full" @update:model-value="v => setDef({ name: String(v) })" />
    </UFormField>
    <UFormField :label="t('wf.canvas.description')">
      <UTextarea :model-value="def.description" :rows="2" autoresize class="w-full" @update:model-value="v => setDef({ description: String(v) })" />
    </UFormField>
  </div>
</template>
