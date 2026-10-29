<script setup lang="ts">
// A workflow's steps as a canvas (ADR-108), as n8n draws them: the palette
// on the left (step types, the project's agents, its workflows), the graph
// in the middle, the selected node's form on the right. Every change is a new
// def (update:def); the editor writes it back to the file. A workflow without
// steps (an agent coordinates it all) shows as Input → Coordinate → End and
// is written so once edited (ADR-111).
// One output may be wired to several steps (they run at once, ADR-110); a
// switch has an output per case and one for "none matched".
import { VueFlow, Handle, Position, MarkerType, useVueFlow, getRectOfNodes } from '@vue-flow/core'
import type { Node, Edge, Connection, NodeChange, EdgeChange, NodeDragEvent, NodeMouseEvent } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls, ControlButton } from '@vue-flow/controls'
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import '@vue-flow/controls/dist/style.css'
import type { MessageKey } from '~/locales/vi'

const props = defineProps<{ def: WorkflowDef, projectId?: string, bindings?: Record<string, string> }>()
const emit = defineEmits<{ 'update:def': [WorkflowDef], 'update:bindings': [Record<string, string>] }>()
const { t } = useLang()

const flowId = `wf-canvas-${useId()}`
const { project, fitView, fitBounds, getNodes, vueFlowRef } = useVueFlow(flowId)

const INPUT = '__input'
const COL = 280
const ROW = 130

const view = computed(() => asGraph(props.def)) // what is drawn and edited
const steps = computed(() => view.value.steps ?? [])
const stepLabel = (type: string) => t(`wf.step.type.${type}` as MessageKey)

// edits of one tick go together (deleting a node also deletes its edges)
let work: WorkflowDef | null = null
function edit(fn: (d: WorkflowDef) => void) {
  if (!work) {
    work = JSON.parse(JSON.stringify(view.value)) as WorkflowDef
    queueMicrotask(() => {
      const d = work!
      work = null
      emit('update:def', d)
    })
  }
  fn(work)
}

// what the palette offers besides the step types
interface WfItem { key: string, name: string, inputs: WorkflowField[] }
const agents = ref<Agent[]>([])
const workflows = ref<WfItem[]>([])
onMounted(async () => {
  try {
    if (props.projectId) {
      const [a, w] = await Promise.all([
        $fetch<{ agents: Agent[] }>(`/api/projects/${props.projectId}/chat/agents`),
        $fetch<{ workflows: ProjectWorkflow[] }>(`/api/projects/${props.projectId}/workflows`)
      ])
      agents.value = a.agents.filter(x => x.enabled !== false)
      workflows.value = w.workflows.map(x => ({ key: x.key, name: x.name, inputs: x.inputs ?? [] }))
    } else {
      const w = await $fetch<{ workflows: LibraryWorkflow[] }>('/api/workflow-library')
      workflows.value = w.workflows.filter(x => !x.error).map(x => ({ key: x.def.key, name: x.def.name, inputs: x.def.inputs ?? [] }))
    }
  } catch { /* the palette keeps the step types */ }
})

// ---- the graph ----
const selected = ref('')
const inputPos = ref<{ x: number, y: number } | null>(null)

// steps without a place: left to right by how far they are from the start
function autoLayout(list: WorkflowStep[]) {
  const by = new Map(list.map(s => [s.id, s]))
  const depth = new Map<string, number>()
  const queue = stepStarts(view.value).filter(id => by.has(id))
  for (const id of queue) depth.set(id, 1)
  while (queue.length) {
    const s = by.get(queue.shift()!)!
    for (const to of stepOuts(s)) {
      if (by.has(to) && !depth.has(to)) {
        depth.set(to, depth.get(s.id)! + 1)
        queue.push(to)
      }
    }
  }
  const last = Math.max(1, ...depth.values()) + 1
  const rows = new Map<number, number>()
  const pos = new Map<string, { x: number, y: number }>()
  for (const s of list) {
    const d = depth.get(s.id) ?? last
    const r = rows.get(d) ?? 0
    rows.set(d, r + 1)
    pos.set(s.id, { x: d * COL, y: r * ROW })
  }
  return pos
}

const nodes = shallowRef<Node[]>([])
const edges = shallowRef<Edge[]>([])

function edge(source: string, handle: string, target: string, label?: string, deletable = true): Edge {
  return {
    id: `e:${source}:${handle}:${target}`, source, target, sourceHandle: handle, label,
    deletable, markerEnd: MarkerType.ArrowClosed, data: { field: handle },
    class: handle === 'else' ? 'wf-edge-else' : undefined
  }
}

function buildSteps() {
  const auto = autoLayout(steps.value)
  const ids = new Set(steps.value.map(s => s.id))
  const starts = stepStarts(view.value).filter(id => ids.has(id))
  const first = steps.value.find(s => s.id === starts[0])
  const firstPos = first ? first.position ?? auto.get(first.id)! : { x: COL, y: 0 }
  nodes.value = [
    { id: INPUT, type: 'wf-input', position: inputPos.value ?? { x: firstPos.x - COL, y: firstPos.y }, deletable: false, data: {} },
    ...steps.value.map(s => ({
      id: s.id, type: 'wf-step', position: s.position ?? auto.get(s.id)!,
      data: { step: s, bad: stepMissing(s, view.value).length > 0, first: starts.includes(s.id) }
    }))
  ]
  const list: Edge[] = starts.map(id => edge(INPUT, 'next', id, undefined, starts.length > 1)) // the last start stays
  const out = (s: WorkflowStep, h: StepLink, label?: string) => {
    for (const to of linksOf(s, h)) if (ids.has(to)) list.push(edge(s.id, h, to, label))
  }
  for (const s of steps.value) {
    if (s.type === 'condition') {
      out(s, 'then', t('wf.canvas.yes'))
      out(s, 'else', t('wf.canvas.no'))
    } else if (s.type === 'switch') {
      s.cases?.forEach((c, i) => out(s, `case:${i}`, c.when || '?'))
      out(s, 'else', t('wf.canvas.otherwise'))
    } else if (s.type !== 'end') {
      out(s, 'next')
      if (s.type === 'approve' || s.type === 'check') out(s, 'else', t('wf.canvas.notPassed'))
    }
  }
  edges.value = list
}

watch(() => props.def, buildSteps, { deep: true, immediate: true })

// ---- graph edits ----
function onConnect(c: Connection) {
  if (!c.target || c.target === INPUT) return
  if (c.source === INPUT) { // one more first step: they start at once
    edit((d) => {
      const list = stepStarts(d)
      if (!list.includes(c.target)) d.start = [...list, c.target]
    })
    return
  }
  // one more step this output goes to (they run at once)
  const field = (c.sourceHandle ?? 'next') as StepLink
  edit((d) => {
    const i = d.steps!.findIndex(x => x.id === c.source)
    const list = i >= 0 ? linksOf(d.steps![i]!, field) : []
    if (i >= 0 && c.target !== c.source && !list.includes(c.target)) d.steps![i] = withLinks(d.steps![i]!, field, [...list, c.target])
  })
}

function onNodesChange(changes: NodeChange[]) {
  const gone = changes.filter(c => c.type === 'remove').map(c => c.id).filter(id => id !== INPUT)
  if (!gone.length) return
  edit((d) => {
    d.steps = d.steps!.filter(s => !gone.includes(s.id)).map(s => unlink(s, gone))
    if (d.start?.length) d.start = d.start.filter(x => !gone.includes(x))
    if (!d.steps.length) d.steps = [{ id: 'end', type: 'end', position: { x: COL, y: 0 } }] // still a graph
  })
  if (gone.includes(selected.value)) selected.value = ''
}

function onEdgesChange(changes: EdgeChange[]) {
  for (const c of changes) {
    if (c.type !== 'remove') continue
    if (c.source === INPUT) {
      edit((d) => { d.start = stepStarts(d).filter(x => x !== c.target) })
      continue
    }
    const field = (c.sourceHandle ?? 'next') as StepLink
    edit((d) => {
      const i = d.steps!.findIndex(x => x.id === c.source)
      if (i >= 0) d.steps![i] = withLinks(d.steps![i]!, field, linksOf(d.steps![i]!, field).filter(x => x !== c.target))
    })
  }
}

function onDragStop(e: NodeDragEvent) {
  interacted.value = true
  const moved = e.nodes.length ? e.nodes : [e.node]
  const at = (n: { position: { x: number, y: number } }) => ({ x: Math.round(n.position.x), y: Math.round(n.position.y) })
  const input = moved.find(n => n.id === INPUT)
  if (input) inputPos.value = at(input)
  const rest = moved.filter(n => n.id !== INPUT)
  if (!rest.length) return
  edit((d) => {
    for (const n of rest) {
      const s = d.steps!.find(x => x.id === n.id)
      if (s) s.position = at(n)
    }
  })
}

function onNodeClick(e: NodeMouseEvent) {
  selected.value = e.node.id
}

// ---- the palette ----
type Drop = { kind: 'type', type: StepType } | { kind: 'agent', id: string } | { kind: 'workflow', key: string }
const MIME = 'application/x-office-step'

function onPaletteDrag(e: DragEvent, p: Drop) {
  e.dataTransfer?.setData(MIME, JSON.stringify(p))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'copy'
}
function onDragOver(e: DragEvent) {
  if (!e.dataTransfer?.types.includes(MIME)) return
  e.preventDefault()
  e.dataTransfer.dropEffect = 'copy'
}
function onDrop(e: DragEvent) {
  const raw = e.dataTransfer?.getData(MIME)
  if (!raw) return
  e.preventDefault()
  const rect = vueFlowRef.value?.getBoundingClientRect()
  const p = project({ x: e.clientX - (rect?.left ?? 0), y: e.clientY - (rect?.top ?? 0) })
  addStep(JSON.parse(raw) as Drop, { x: Math.round(p.x - 90), y: Math.round(p.y - 25) }, false)
}
// a click adds it right of the selected step (and links it when that one goes nowhere yet)
function onPaletteClick(p: Drop) {
  const from = steps.value.find(s => s.id === selected.value)
  const fromNode = nodes.value.find(n => n.id === selected.value)
  const maxX = Math.max(0, ...nodes.value.map(n => n.position.x))
  const pos = fromNode ? { x: fromNode.position.x + COL, y: fromNode.position.y } : { x: maxX + COL, y: 0 }
  addStep(p, pos, !!from)
}

const slug = (s: string) => s.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/đ/g, 'd').replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 36) || 'step' // i18n-ignore
function uniqueKey(base: string, taken: Set<string>) {
  let k = slug(base)
  for (let i = 2; taken.has(k); i++) k = `${slug(base).slice(0, 34)}-${i}`
  return k
}

function addStep(p: Drop, position: { x: number, y: number }, link: boolean) {
  const ids = new Set(steps.value.map(s => s.id))
  let s: WorkflowStep
  let bindings: Record<string, string> | null = null
  let role: WorkflowRole | null = null
  if (p.kind === 'agent') {
    const a = agents.value.find(x => x.id === p.id)
    if (!a) return
    // a role already given to that agent, else a new one for it
    const bound = props.def.roles.find(r => props.bindings?.[r.key] === a.id && !r.workflow)
    let key = bound?.key
    if (!key) {
      const taken = new Set([...props.def.roles.map(r => r.key), 'dieu-phoi'])
      key = uniqueKey(a.key || a.name, taken)
      role = { key, name: a.name }
      bindings = { ...(props.bindings ?? {}), [key]: a.id }
    }
    s = { id: uniqueKey(key, ids), type: 'agent', role: key, prompt: '' }
  } else if (p.kind === 'workflow') {
    const w = workflows.value.find(x => x.key === p.key)
    s = { id: uniqueKey(p.key, ids), type: 'workflow', workflow: p.key, inputs: Object.fromEntries((w?.inputs ?? []).map(f => [f.key, ''])) }
  } else {
    const base: Record<StepType, Partial<WorkflowStep>> = {
      agent: { role: props.def.roles[0]?.key ?? '', prompt: '' },
      coordinate: { prompt: '' },
      workflow: { workflow: '', inputs: {} },
      code: { lang: 'bash', script: '' },
      http: { method: 'GET', url: '' },
      condition: { if: '', then: [], else: [] },
      switch: { value: '', cases: [{ when: '', next: [] }] },
      wait: { for: '30m' },
      approve: { note: '' },
      check: { command: '' },
      end: {}
    }
    s = { id: uniqueKey(p.type, ids), type: p.type, ...base[p.type] }
  }
  s.position = position
  const from = selected.value
  edit((d) => {
    if (role) d.roles.push(role)
    d.steps!.push(s)
    const src = link ? d.steps!.find(x => x.id === from) : undefined
    if (src && src.type !== 'end' && src.type !== 'condition' && src.type !== 'switch' && !src.next?.length) src.next = [s.id]
  })
  if (bindings) emit('update:bindings', bindings)
  selected.value = s.id
}

// the first draw fits the view once the nodes are measured; until the user
// pans, zooms or drags a node, the graph box resizing re-fits too: on a phone
// the nodes are measured while the box is still 0 wide (that fit is skipped)
// and only a later resize gives the room to fit into
const fitted = ref(false)
const interacted = ref(false)
function onNodesInitialized() {
  if (fitted.value) return
  fitted.value = true
  fitFree()
}
function onUserMoved() {
  interacted.value = true
}
const canvasEl = ref<HTMLElement>()
let resizeObserver: ResizeObserver | undefined
let resizeFrame = 0
onMounted(() => {
  if (!canvasEl.value) return
  resizeObserver = new ResizeObserver(() => {
    if (!fitted.value || interacted.value) return
    cancelAnimationFrame(resizeFrame)
    resizeFrame = requestAnimationFrame(fitFree)
  })
  resizeObserver.observe(canvasEl.value)
})
onUnmounted(() => {
  resizeObserver?.disconnect()
  cancelAnimationFrame(resizeFrame)
})

// the roles a coordinate step hands work to (none named: every role but its coordinator)
const coordRoles = (s: WorkflowStep) => s.roles?.length ? s.roles : view.value.roles.filter(r => r.key !== s.role).map(r => r.key)

// the floating panels (wider screens): hidden for more room
const paletteOpen = ref(true)
const formOpen = ref(true)
const paletteEl = ref<HTMLElement>()
const formEl = ref<HTMLElement>()
// fit the graph into the room the panels leave (the whole box on a phone)
function fitFree() {
  const box = vueFlowRef.value?.getBoundingClientRect()
  const nodes = getNodes.value
  if (!box?.width || !nodes.length) return
  const side = (el?: HTMLElement, open = true) => open && el && getComputedStyle(el).position === 'absolute' ? el.offsetWidth + 8 : 0
  const left = side(formEl.value, formOpen.value)
  const right = side(paletteEl.value, paletteOpen.value)
  const free = box.width - left - right
  if (free < box.width * 0.3) return fitView({ padding: 0.2 })
  const r = getRectOfNodes(nodes)
  const width = r.width * box.width / free // the graph spans the free part, the panels' part is empty room
  fitBounds({ x: r.x - (width - r.width) * left / (left + right || 1), y: r.y, width, height: r.height }, { padding: 0.12 })
}
watch(selected, (id) => { if (id) formOpen.value = true }) // a node picked: its form shows

const palette = computed(() => STEP_TYPES.map(s => ({ ...s, label: stepLabel(s.type) })))
</script>

<template>
  <!-- a phone: palette, graph, form one under the other; wider: the graph
       fills the box, the form floats on the left and the palette on the right -->
  <div class="wf-canvas relative flex flex-col overflow-hidden rounded-lg border border-(--ui-border) md:block md:h-[40rem] lg:h-full lg:min-h-[30rem]">
    <!-- palette -->
    <aside
      ref="paletteEl" class="flex shrink-0 gap-3 overflow-auto border-b border-(--ui-border) p-2 md:absolute md:end-2 md:top-2 md:z-10 md:max-h-[calc(100%-1rem)] md:w-44 md:flex-col md:rounded-lg md:border md:bg-(--ui-bg)/95 md:shadow-lg md:backdrop-blur"
      :class="!paletteOpen && 'md:hidden'"
    >
      <UButton class="hidden self-end md:inline-flex" size="xs" color="neutral" variant="ghost" icon="i-lucide-panel-right-close" :aria-label="t('wf.canvas.hidePanel')" @click="paletteOpen = false" />
      <div class="space-y-1">
        <p class="flex items-center gap-1 px-1 text-xs font-medium text-(--ui-text-muted)">
          {{ t('wf.canvas.steps') }}
          <InfoTip :text="t('wf.canvas.paletteInfo')" icon-class="size-3.5" />
        </p>
        <div class="flex gap-1 md:flex-col">
          <button
            v-for="p in palette" :key="p.type" type="button" draggable="true"
            class="flex shrink-0 cursor-grab items-center gap-1.5 rounded-md border border-(--ui-border) bg-(--ui-bg) px-2 py-1 text-start text-xs hover:border-primary/60 hover:bg-(--ui-bg-elevated)"
            @dragstart="onPaletteDrag($event, { kind: 'type', type: p.type })" @click="onPaletteClick({ kind: 'type', type: p.type })"
          >
            <UIcon :name="p.icon" class="size-3.5 shrink-0 text-primary" />
            <span class="truncate">{{ p.label }}</span>
          </button>
        </div>
      </div>
      <div v-if="projectId && agents.length" class="space-y-1">
        <p class="px-1 text-xs font-medium text-(--ui-text-muted)">{{ t('wf.canvas.agents') }}</p>
        <div class="flex gap-1 md:flex-col">
          <button
            v-for="a in agents" :key="a.id" type="button" draggable="true"
            class="flex shrink-0 cursor-grab items-center gap-1.5 rounded-md border border-(--ui-border) bg-(--ui-bg) px-2 py-1 text-start text-xs hover:border-primary/60 hover:bg-(--ui-bg-elevated)"
            @dragstart="onPaletteDrag($event, { kind: 'agent', id: a.id })" @click="onPaletteClick({ kind: 'agent', id: a.id })"
          >
            <AgentAvatar :agent="a" size="2xs" class="shrink-0" />
            <span class="truncate">{{ a.name }}</span>
          </button>
        </div>
      </div>
      <div v-if="workflows.length" class="space-y-1">
        <p class="px-1 text-xs font-medium text-(--ui-text-muted)">{{ t('wf.section') }}</p>
        <div class="flex gap-1 md:flex-col">
          <button
            v-for="w in workflows" :key="w.key" type="button" draggable="true" :title="w.name"
            class="flex shrink-0 cursor-grab items-center gap-1.5 rounded-md border border-(--ui-border) bg-(--ui-bg) px-2 py-1 text-start text-xs hover:border-primary/60 hover:bg-(--ui-bg-elevated)"
            @dragstart="onPaletteDrag($event, { kind: 'workflow', key: w.key })" @click="onPaletteClick({ kind: 'workflow', key: w.key })"
          >
            <UIcon name="i-lucide-workflow" class="size-3.5 shrink-0 text-primary" />
            <span class="truncate font-mono">/{{ w.key }}</span>
          </button>
        </div>
      </div>
    </aside>

    <UButton v-if="!paletteOpen" class="absolute end-2 top-2 z-10 max-md:hidden" size="sm" color="neutral" variant="outline" icon="i-lucide-panel-right-open" :label="t('wf.canvas.steps')" @click="paletteOpen = true" />
    <UButton v-if="!formOpen" class="absolute start-2 top-2 z-10 max-md:hidden" size="sm" color="neutral" variant="outline" icon="i-lucide-panel-left-open" :label="t('wf.canvas.details')" @click="formOpen = true" />

    <!-- the graph -->
    <div ref="canvasEl" class="relative h-[26rem] min-w-0 shrink-0 md:absolute md:inset-0 md:h-auto" @dragover="onDragOver" @drop="onDrop">
      <VueFlow
        :id="flowId" :nodes="nodes" :edges="edges"
        :edges-updatable="false"
        :delete-key-code="['Backspace', 'Delete']" :min-zoom="0.2" :max-zoom="1.5"
        :default-edge-options="{ type: 'smoothstep' }"
        @nodes-initialized="onNodesInitialized" @connect="onConnect" @nodes-change="onNodesChange" @edges-change="onEdgesChange"
        @node-drag-stop="onDragStop" @node-click="onNodeClick" @pane-click="selected = ''" @move-start="onUserMoved"
      >
        <Background :gap="18" :size="1.2" />
        <Controls :show-interactive="false" :show-fit-view="false" position="bottom-center">
          <ControlButton :title="t('wf.canvas.fit')" @click="fitFree()"><UIcon name="i-lucide-maximize" /></ControlButton>
        </Controls>

        <template #node-wf-input>
          <div class="wf-node w-44" :class="selected === INPUT && 'wf-node-selected'">
            <div class="flex items-center gap-1.5 font-medium">
              <UIcon name="i-lucide-log-in" class="size-4 text-primary" />{{ t('wf.inputs') }}
            </div>
            <ul v-if="def.inputs?.length" class="mt-1 space-y-0.5">
              <li v-for="f in def.inputs" :key="f.key" class="truncate font-mono text-[11px] text-(--ui-text-muted)">
                {{ f.key }}<span v-if="f.required" class="text-(--ui-warning)">*</span>
              </li>
            </ul>
            <p v-else class="mt-1 text-[11px] text-(--ui-text-dimmed)">{{ def.input || t('wf.canvas.noInputs') }}</p>
            <Handle id="next" type="source" :position="Position.Right" />
          </div>
        </template>

        <template #node-wf-step="{ id, data }">
          <div class="wf-node w-48" :class="[selected === id && 'wf-node-selected', data.bad && 'wf-node-bad']">
            <Handle type="target" :position="Position.Left" />
            <div class="flex items-center gap-1.5">
              <UIcon :name="stepIcon(data.step.type)" class="size-4 shrink-0 text-primary" />
              <span class="truncate font-medium">{{ data.step.name || stepLabel(data.step.type) }}</span>
              <UIcon v-if="data.bad" name="i-lucide-circle-alert" class="ms-auto size-3.5 shrink-0 text-(--ui-error)" />
            </div>
            <p class="truncate font-mono text-[11px] text-(--ui-text-muted)">
              {{ data.step.id }}<template v-if="data.step.type === 'agent' && data.step.role"> · {{ data.step.role }}</template><template v-else-if="data.step.type === 'workflow' && data.step.workflow"> · /{{ data.step.workflow }}</template>
            </p>
            <div v-if="data.step.type === 'coordinate'" class="mt-1 flex flex-wrap gap-0.5">
              <span v-for="r in coordRoles(data.step)" :key="r" class="rounded bg-(--ui-bg-elevated) px-1 font-mono text-[10px]">{{ r }}</span>
            </div>
            <p v-if="data.step.type === 'condition' && data.step.if" class="truncate font-mono text-[11px] text-(--ui-text-dimmed)">{{ data.step.if }}</p>
            <!-- a switch: an output per case, and one when none matches -->
            <div v-if="data.step.type === 'switch'" class="mt-1 space-y-1">
              <p v-if="data.step.value" class="truncate font-mono text-[11px] text-(--ui-text-dimmed)">{{ data.step.value }}</p>
              <div v-for="(c, i) in data.step.cases ?? []" :key="i" class="relative rounded bg-(--ui-bg-elevated) px-1.5 py-0.5 pe-3 text-[11px]">
                <span class="block truncate font-mono">= {{ c.when || '?' }}</span>
                <Handle :id="`case:${i}`" type="source" :position="Position.Right" class="wf-h-case" />
              </div>
              <div class="relative px-1.5 py-0.5 pe-3 text-[11px] text-(--ui-text-muted)">
                {{ t('wf.canvas.otherwise') }}
                <Handle id="else" type="source" :position="Position.Right" class="wf-h-else" />
              </div>
            </div>
            <template v-if="data.step.type === 'condition'">
              <Handle id="then" type="source" :position="Position.Right" class="wf-h-then" style="top: 35%" />
              <Handle id="else" type="source" :position="Position.Right" class="wf-h-else" style="top: 75%" />
            </template>
            <template v-else-if="data.step.type !== 'end' && data.step.type !== 'switch'">
              <Handle id="next" type="source" :position="Position.Right" />
              <Handle v-if="data.step.type === 'approve' || data.step.type === 'check'" id="else" type="source" :position="Position.Bottom" class="wf-h-else" />
            </template>
          </div>
        </template>

      </VueFlow>
    </div>

    <!-- the selected node's form -->
    <aside
      ref="formEl" class="max-h-[36rem] shrink-0 overflow-y-auto border-t border-(--ui-border) p-3 md:absolute md:start-2 md:top-2 md:z-10 md:max-h-[calc(100%-1rem)] md:w-80 md:rounded-lg md:border md:bg-(--ui-bg)/95 md:shadow-lg md:backdrop-blur"
      :class="!formOpen && 'md:hidden'"
    >
      <UButton class="float-end hidden md:inline-flex" size="xs" color="neutral" variant="ghost" icon="i-lucide-panel-left-close" :aria-label="t('wf.canvas.hidePanel')" @click="formOpen = false" />
      <WorkflowStepForm
        :def="view" :sel="selected" :project-id="projectId" :bindings="bindings"
        :agents="agents" :workflows="workflows"
        @update:def="d => emit('update:def', d)" @update:bindings="b => emit('update:bindings', b)" @select="id => selected = id"
      />
    </aside>
  </div>
</template>

<style>
/* Vue Flow's default theme is light: the dashboard's colors, dark mode too */
.wf-canvas .vue-flow {
  --vf-node-bg: var(--ui-bg);
  --vf-node-text: var(--ui-text);
  --vf-connection-path: var(--ui-border-accented);
  --vf-handle: var(--ui-text-muted);
  background: var(--ui-bg-muted);
}
.wf-canvas .vue-flow__background pattern circle { fill: var(--ui-border-accented); }
.wf-canvas .wf-node {
  position: relative;
  border: 1px solid var(--ui-border-accented);
  border-radius: 0.5rem;
  background: var(--ui-bg);
  color: var(--ui-text);
  padding: 0.5rem 0.625rem;
  font-size: 12px;
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.06);
}
.wf-canvas .wf-node-selected { border-color: var(--ui-primary); box-shadow: 0 0 0 2px color-mix(in oklab, var(--ui-primary) 30%, transparent); }
.wf-canvas .wf-node-bad { border-color: var(--ui-error); }
.wf-canvas .vue-flow__handle {
  width: 10px;
  height: 10px;
  background: var(--ui-bg);
  border: 2px solid var(--ui-text-muted);
}
.wf-canvas .vue-flow__handle.wf-h-then { border-color: var(--ui-success); }
.wf-canvas .vue-flow__handle.wf-h-else { border-color: var(--ui-error); }
.wf-canvas .vue-flow__handle.wf-h-case { border-color: var(--ui-primary); }
.wf-canvas .wf-node .relative > .vue-flow__handle { right: -11px; }
.wf-canvas .vue-flow__handle:hover { border-color: var(--ui-primary); }
.wf-canvas .vue-flow__edge-path { stroke: var(--ui-border-accented); stroke-width: 1.5; }
.wf-canvas .vue-flow__edge.selected .vue-flow__edge-path,
.wf-canvas .vue-flow__edge:focus .vue-flow__edge-path { stroke: var(--ui-primary); }
.wf-canvas .wf-edge-else .vue-flow__edge-path { stroke-dasharray: 5 4; }
.wf-canvas .vue-flow__arrowhead polyline { stroke: var(--ui-border-accented); fill: var(--ui-border-accented); }
.wf-canvas .vue-flow__edge-textbg { fill: var(--ui-bg); }
.wf-canvas .vue-flow__edge-text { fill: var(--ui-text-muted); font-size: 11px; }
.wf-canvas .vue-flow__controls { box-shadow: none; border: 1px solid var(--ui-border); border-radius: 0.375rem; overflow: hidden; }
.wf-canvas .vue-flow__controls-button { background: var(--ui-bg); border-bottom: 1px solid var(--ui-border); color: var(--ui-text); }
.wf-canvas .vue-flow__controls-button svg { fill: currentColor; }
.wf-canvas .vue-flow__controls-button:hover { background: var(--ui-bg-elevated); }
</style>
