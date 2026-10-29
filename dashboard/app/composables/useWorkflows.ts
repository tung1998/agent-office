// Workflows (ADR-098): how a project's agents work together, from a library
// of files; each project keeps its own copy with the agent of each role.

// '' = its agent's own permission (the default); a value caps the role
export type WorkflowAccess = '' | 'analyze' | 'propose' | 'edit'

export interface WorkflowRole {
  key: string
  name: string
  hint?: string
  access?: WorkflowAccess
  differ_from?: string[]
  workflow?: string // a sub-workflow fills it (ADR-102)
  prefer?: { tier?: string, family?: string } // the agent it wants (ADR-103)
}

// An input a caller gives a workflow, or an output it gives back (ADR-103).
export interface WorkflowField {
  key: string
  description?: string
  required: boolean
  type?: 'string' | 'number' | 'boolean' | 'list' | 'json'
}

export type WorkflowCallable = '' | 'chat' | 'sub'

export interface WorkflowGate {
  key: string
  name: string
  kind: 'approve' | 'check'
  command?: string
  required?: boolean
}

export interface WorkflowVote {
  roles?: string[]
  quorum: number
  veto?: string[]
}

export interface WorkflowLimits {
  rounds: number
  turns: number
  timeout: string
  budget_usd: number
  depth: number // how deep sub-workflows may go below a run
  concurrency: number // roles at once in a whole tree of runs
  idle?: string // a role working this long is noted
}

export interface WorkflowDef {
  key: string
  name: string
  description: string
  category?: string
  input?: string
  roles: WorkflowRole[]
  parallel?: string[][]
  limits: WorkflowLimits
  brief?: string[]
  gates?: WorkflowGate[]
  vote?: WorkflowVote | null
  supervise?: { role: string, every?: string } | null // a role checking the run now and then (ADR-115)
  strict?: boolean
  inputs?: WorkflowField[]
  outputs?: WorkflowField[]
  callable?: WorkflowCallable
  steps?: WorkflowStep[] // a graph the office runs in order (ADR-108); none: a coordinator decides
  start?: string[] | null // the first steps, run at once (none: the first of steps) (ADR-110)
  body: string
}

// Steps (ADR-108): one node of a workflow's graph. Data goes from step to
// step by templates: {{input.key}}, {{steps.<id>.output}},
// {{steps.<id>.json.a.b}}, {{steps.<id>.status}}.
// One output may go to several steps (a list): they run at once; a step
// several branches reach waits for them all (ADR-110).
export type StepType = 'agent' | 'coordinate' | 'workflow' | 'code' | 'http' | 'condition' | 'switch' | 'wait' | 'approve' | 'check' | 'end'

// a switch's way out: the value it matches and where it goes
export interface WorkflowCase { when: string, next: string[] | null }

export interface WorkflowStep {
  id: string
  type: StepType
  name?: string
  next?: string[] | null
  on_error?: '' | 'stop' | 'continue'
  role?: string // agent; coordinate: the role whose agent coordinates (none: the run's coordinator)
  prompt?: string // agent; coordinate: the coordinator's instructions
  skill?: string // agent: a project skill whose instructions go with the prompt (ADR-185)
  roles?: string[] // coordinate: the roles it hands work to (none: every role)
  workflow?: string // workflow: a sub-workflow's key and its inputs
  inputs?: Record<string, string>
  lang?: 'bash' | 'node' | 'python' | '' // code
  script?: string
  timeout_s?: number
  method?: string // http
  url?: string
  headers?: Record<string, string>
  body?: string
  if?: string // condition
  then?: string[] | null
  else?: string[] | null // condition; switch: no case matched; approve/check: when it does not pass
  value?: string // switch: what is compared with each case
  cases?: WorkflowCase[]
  max_loops?: number
  for?: string // wait: 30m, 1h… (up to 24h)
  note?: string // approve
  command?: string // check
  summary?: string // end
  outputs?: Record<string, string>
  position?: { x: number, y: number } // on the editor's canvas
}

// the step types, in the order the editor offers them
export const STEP_TYPES: { type: StepType, icon: string }[] = [
  { type: 'agent', icon: 'i-lucide-bot' },
  { type: 'coordinate', icon: 'i-lucide-crown' },
  { type: 'workflow', icon: 'i-lucide-workflow' },
  { type: 'code', icon: 'i-lucide-code' },
  { type: 'http', icon: 'i-lucide-globe' },
  { type: 'condition', icon: 'i-lucide-split' },
  { type: 'switch', icon: 'i-lucide-route' },
  { type: 'wait', icon: 'i-lucide-hourglass' },
  { type: 'approve', icon: 'i-lucide-user-check' },
  { type: 'check', icon: 'i-lucide-test-tube' },
  { type: 'end', icon: 'i-lucide-flag' }
]
export const stepIcon = (type: string) => STEP_TYPES.find(s => s.type === type)?.icon ?? 'i-lucide-square'

const STEP_KEY = /^[a-z0-9][a-z0-9-]{0,39}$/
export const validStepKey = (k: string) => STEP_KEY.test(k)
// a step's id may have "_" too (internal/workflow ValidStepID)
const STEP_ID = /^[a-z0-9][a-z0-9_-]{0,39}$/
export const validStepId = (k: string) => STEP_ID.test(k)

// an output of a step (a node's handle): next, then, else, or a switch's case:<i>
export type StepLink = 'next' | 'then' | 'else' | `case:${number}`
export function linksOf(s: WorkflowStep, h: StepLink): string[] {
  if (h.startsWith('case:')) return s.cases?.[Number(h.slice(5))]?.next ?? []
  return s[h as 'next' | 'then' | 'else'] ?? []
}
// the step with that output going to list
export function withLinks(s: WorkflowStep, h: StepLink, list: string[]): WorkflowStep {
  if (h.startsWith('case:')) {
    const i = Number(h.slice(5))
    return { ...s, cases: (s.cases ?? []).map((c, j) => j === i ? { ...c, next: list } : c) }
  }
  return { ...s, [h]: list.length ? list : undefined }
}
// every step it may go to
export const stepOuts = (s: WorkflowStep) => [...(s.next ?? []), ...(s.then ?? []), ...(s.else ?? []), ...(s.cases ?? []).flatMap(c => c.next ?? [])]
// the first steps
export const stepStarts = (d: WorkflowDef) => d.start?.length ? d.start : d.steps?.[0] ? [d.steps[0].id] : []
// a step gone: no link goes to it any more
export function unlink(s: WorkflowStep, gone: string[]): WorkflowStep {
  const keep = (l?: string[] | null) => l?.length ? l.filter(x => !gone.includes(x)) : l
  return { ...s, next: keep(s.next), then: keep(s.then), else: keep(s.else), cases: s.cases?.map(c => ({ ...c, next: keep(c.next) ?? [] })) }
}

// what a step lacks (internal/workflow validateSteps): the fields to mark
export function stepMissing(s: WorkflowStep, def: WorkflowDef): string[] {
  const ids = new Set((def.steps ?? []).map(x => x.id))
  const bad: string[] = []
  const ref = (field: StepLink, need: boolean) => {
    const to = linksOf(s, field)
    if ((!to.length && need) || to.some(x => !ids.has(x))) bad.push(field)
  }
  if (!validStepId(s.id) || (def.steps ?? []).filter(x => x.id === s.id).length > 1) bad.push('id')
  switch (s.type) {
    case 'agent':
      if (!def.roles.some(r => r.key === s.role)) bad.push('role')
      if (!s.prompt?.trim()) bad.push('prompt')
      break
    case 'coordinate':
      if (s.role && !def.roles.some(r => r.key === s.role)) bad.push('role')
      if ((s.roles ?? []).some(k => k === s.role || !def.roles.some(r => r.key === k)) || !def.roles.some(r => r.key !== s.role)) bad.push('roles')
      if (!s.prompt?.trim()) bad.push('prompt')
      break
    case 'workflow':
      if (!validStepKey(s.workflow ?? '')) bad.push('workflow')
      break
    case 'code':
      if (!['bash', 'node', 'python'].includes(s.lang ?? '')) bad.push('lang')
      if (!s.script?.trim()) bad.push('script')
      break
    case 'http': {
      const u = s.url ?? ''
      if (!u.startsWith('http://') && !u.startsWith('https://') && !u.startsWith('{{')) bad.push('url')
      break
    }
    case 'condition':
      if (!s.if?.trim()) bad.push('if')
      ref('then', true)
      ref('else', true)
      break
    case 'switch': {
      if (!s.value?.trim()) bad.push('value')
      if (!s.cases?.length) bad.push('cases')
      const seen = new Set<string>()
      s.cases?.forEach((c, i) => {
        const w = c.when.trim().toLowerCase()
        if (!w || seen.has(w)) bad.push(`when:${i}`)
        seen.add(w)
        ref(`case:${i}`, true)
      })
      ref('else', false)
      break
    }
    case 'wait':
      if (!validWait(s.for ?? '')) bad.push('for')
      break
    case 'approve':
      if (!s.note?.trim()) bad.push('note')
      ref('else', false)
      break
    case 'check':
      if (!s.command?.trim()) bad.push('command')
      ref('else', false)
      break
  }
  if (s.type !== 'end' && s.type !== 'condition' && s.type !== 'switch') ref('next', true)
  return bad
}

// a wait's length as Go reads it (30m, 1h30m, 90s), more than 0 and at most 24h; a template passes
const WAIT_UNIT: Record<string, number> = { ms: 0.001, s: 1, m: 60, h: 3600 }
export function validWait(v: string): boolean {
  const t = v.trim()
  if (t.includes('{{')) return true
  if (!/^(\d+(\.\d+)?(ms|s|m|h))+$/.test(t)) return false
  let secs = 0
  for (const [, n, , u] of t.matchAll(/(\d+(\.\d+)?)(ms|s|m|h)/g)) secs += Number(n) * WAIT_UNIT[u!]!
  return secs > 0 && secs <= 24 * 3600
}

// One kind of workflow (ADR-111): a file without steps (an agent coordinates
// it all) is the graph Input → one coordinate step → End, its body the
// step's instructions; the canvas shows it so and writes it so once edited.
export const COORD_STEP = 'dieu-phoi'
export function asGraph(d: WorkflowDef): WorkflowDef {
  if (d.steps?.length) return d
  return {
    ...d,
    body: '',
    steps: [
      { id: COORD_STEP, type: 'coordinate', prompt: d.body, next: ['xong'], position: { x: 280, y: 0 } },
      { id: 'xong', type: 'end', summary: `{{steps.${COORD_STEP}.output}}`, outputs: Object.fromEntries((d.outputs ?? []).map(f => [f.key, `{{steps.${COORD_STEP}.json.${f.key}}}`])), position: { x: 560, y: 0 } }
    ]
  }
}

// a step renamed: every link to it and every {{steps.old.…}} follows
export function renameStep(steps: WorkflowStep[], from: string, to: string): WorkflowStep[] {
  const re = new RegExp(`\\{\\{(\\s*)steps\\.${from.replace(/[-]/g, '\\-')}\\.`, 'g')
  const tpl = (v: string) => v.replace(re, `{{$1steps.${to}.`)
  const map = (m?: Record<string, string>) => m && Object.fromEntries(Object.entries(m).map(([k, v]) => [k, tpl(v)]))
  return steps.map((s) => {
    const n: WorkflowStep = { ...s }
    if (n.id === from) n.id = to
    for (const f of ['next', 'then', 'else'] as const) if (n[f]) n[f] = n[f]!.map(x => x === from ? to : x)
    n.cases = n.cases?.map(c => ({ when: tpl(c.when), next: (c.next ?? []).map(x => x === from ? to : x) }))
    for (const f of ['prompt', 'script', 'url', 'body', 'if', 'value', 'for', 'note', 'command', 'summary'] as const) {
      if (n[f]) n[f] = tpl(n[f]!)
    }
    n.inputs = map(n.inputs)
    n.headers = map(n.headers)
    n.outputs = map(n.outputs)
    return n
  })
}

// A library workflow (GET /api/workflow-library).
export interface LibraryWorkflow {
  def: WorkflowDef
  source?: string
  builtin: boolean // shipped with the office
  error?: string
  updated_at: string
  version: string
}

export interface RunRole {
  role: string
  name: string
  access: WorkflowAccess
  agent_id: string
  agent_name: string
  status: 'idle' | 'working' | 'done' | 'failed'
  rounds: number
  turns: number
  result?: string
  cost_usd: number
  workflow?: string // a sub-workflow fills it
  run_id?: string // its latest run
}

export interface RunGate {
  key: string
  name: string
  kind: 'approve' | 'check'
  required: boolean
  status: 'open' | 'waiting' | 'passed' | 'failed'
  action_id?: string
  detail?: string
}

// A run: it works in a chat of its own (conversation_id); the chat that
// called it (caller_conversation_id) shows only its input and output.
export interface WorkflowRun {
  id: string
  project_id: string
  conversation_id: string
  caller_conversation_id: string // '' = a run from before, held in conversation_id itself
  caller_title: string
  parent_run_id: string // the run whose role called it ('' = a chat did)
  depth: number
  workflow_id: string
  workflow_key: string
  workflow_name: string
  coordinator_id: string
  coordinator_name: string
  input: string
  status: 'running' | 'done' | 'failed' | 'stopped'
  turns: number
  max_turns: number
  cost_usd: number
  result: string
  error: string
  roles: RunRole[]
  gates: RunGate[]
  log: { at: string, text: string }[]
  outputs?: Record<string, string> // its declared outputs, by key
  started_at: string
  finished_at: string | null
}

// A project's workflow (GET /api/projects/{id}/workflows).
export interface ProjectWorkflow {
  id: string
  project_id: string
  key: string
  name: string
  description: string
  input: string
  enabled: boolean
  source: string
  bindings: Record<string, string> // role → agent id
  roles: WorkflowRole[]
  parallel: string[][]
  gates: WorkflowGate[]
  vote: WorkflowVote | null
  limits: WorkflowLimits
  brief: string[]
  inputs: WorkflowField[]
  outputs: WorkflowField[]
  callable: WorkflowCallable
  error?: string
  source_key: string // the library workflow it was copied from
  has_update: boolean // that one changed since
  updated_at: string
  last_run: WorkflowRun | null
  version: string // ADR-072: send back on PATCH so a stale save 409s instead of overwriting
}

export type AccessKey = 'agent' | 'analyze' | 'propose' | 'edit'
export const accessKey = (a?: string): AccessKey => (a || 'agent') as AccessKey

export const accessIcon: Record<AccessKey, string> = {
  agent: 'i-lucide-user-check',
  analyze: 'i-lucide-eye',
  propose: 'i-lucide-message-square-diff',
  edit: 'i-lucide-pencil'
}

// what a fenced ```workflow block of an agent's answer holds (the whole file)
export function workflowBlock(text: string): string | null {
  const m = text.match(/```workflow\n([\s\S]*?)\n```/)
  return m ? m[1]! : null
}
