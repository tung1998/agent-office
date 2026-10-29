// Automations and jobs as the dashboard sees them (ADR-040).
export type JobStatus = 'pending' | 'running' | 'done' | 'failed' | 'cancelled' | 'skipped' | 'needs_input'

export interface Job {
  id: string
  project_id: string
  project_name: string
  kind: 'chat_turn' | 'task' | 'script'
  origin: 'user' | 'automation' | 'monitor' | 'retry'
  origin_id: string
  trigger: string
  created_by: string
  conversation_id: string
  message_id: string
  task_id: string
  status: JobStatus
  error: string
  error_code: string
  agent_id: string
  agent_name: string
  automation_name: string
  title: string
  cost_usd: number
  input_tokens: number
  output_tokens: number
  duration_ms: number
  created_at: string
  started_at: string | null
  finished_at: string | null
  exit_code: number | null
  parent_job_id: string
  output?: string
  payload?: string
}

export interface AutomationConfig {
  every_minutes?: number, cron?: string, timezone?: string, auth?: string, auth_name?: string
  // a schedule runs only when its data or work board has work (ADR-194, ADR-196); kept as read on save
  only_if?: { collection?: string, where?: { field: string, op: string, value: unknown }[], changed?: boolean, board?: '' | 'open' | 'changed' | 'any' }
  // telegram | discord (ADR-049): the channel, which messages (a keyword; none = any), within a scope
  channel_id?: string, keywords?: string[], scope?: string
  // …or a custom slash command of the bot, and the text typed after it ("" = none)
  command?: string, command_description?: string, command_arg?: string
  skill?: string // the command calls this project skill
  workflow?: string // action workflow: the workflow it runs, the prompt being its input (ADR-109)
  reply_mode?: '' | 'answer' | 'steps' // what the bot shows of a run ('' = the bot's own)
  own_setup?: boolean // a bot's command set up on its own; false = the bot's default setup
  pull_request?: boolean // a GitHub/Bitbucket PR webhook: runs on a PR opened or updated, with {{diff}}
  notify_channel_id?: string, notify_chat_id?: string // what a run answers goes to this bot's chat too
  tags?: string[] // put on the chat of each run
  ends_at?: string | null // a schedule or event automation turns itself off then (as a Burn); none = runs until turned off
  stop_when?: string // a run whose answer contains it turns it off (ADR-244)
  goal?: AutomationGoal | null // chat/workflow: runs until it is reached (ADR-132)
  // source event (ADR-243): runs when this ends in the project; from: one agent / automation / workflow key ('' = any, 'self' = itself, ADR-244);
  // same_chat: a turn.* run goes on in the chat that turn ended in
  event?: AutomationEvent, event_from?: string, event_same_chat?: boolean
}
export const automationEvents = ['turn.done', 'turn.failed', 'automation.done', 'automation.failed', 'workflow.done', 'workflow.failed'] as const
export type AutomationEvent = typeof automationEvents[number]
// AutomationGoal: after each turn the check (exit 0) and/or the judge; not reached, the chat goes on
export interface AutomationGoal { text?: string, check?: string, judge?: boolean, max_rounds?: number }
export interface AutomationLimits {
  max_parallel?: number // runs at once (ADR-082)
  max_minutes?: number // how long one run may take (0 = no limit)
  max_runs_per_hour?: number
  daily_cost_usd?: number
  disable_after_failures?: number
  debounce_seconds?: number
  debounce_key?: string
  debounce_max_seconds?: number
}
export interface AutomationScript { lang: 'bash' | 'node' | 'python', body: string, timeout_s?: number, burn_start?: boolean } // burn_start: its @@quest lines start a stopped Burn (ADR-132)
export interface AutomationEscalate { when: 'never' | 'failure' | 'signal', action: 'chat' | 'task', agent_id: string, prompt: string } // kept for old ones: never runs (ADR-057)
export interface Automation {
  id: string
  project_id: string
  name: string
  enabled: boolean
  source: 'schedule' | 'webhook' | 'event' | 'telegram' | 'discord'
  config: AutomationConfig
  action: 'chat' | 'script' | 'workflow'
  agent_id: string
  prompt: string
  edit_mode: 'worktree' | 'direct'
  model_tier?: '' | 'strong' | 'balanced' | 'fast' // '' = the agent's own
  keep_context: boolean
  limits: AutomationLimits
  script: AutomationScript
  escalate: AutomationEscalate
  failures: number
  disabled_code: string
  disabled_reason: string
  last_run_at: string | null
  next_run_at: string | null
  webhook_url?: string
  // telegram | discord: the bot it listens to (ADR-049), and how that bot is doing
  bot?: { has_token: boolean, allow: string[], refusal: string }
  bot_status?: { kind: 'telegram' | 'discord', bot_name: string, enabled: boolean, last_error: string, last_message_at: string | null, shared: number, state?: BotState }
  last_job: Job | null
  created_at: string
  version?: string // what an edit is made from (409 when changed since)
  // ADR-074: permission override
  permission_mode?: 'agent' | 'override'
  override_full_access?: boolean
  override_admin_by?: string // who turned full access on (the server stamps it)
  override_extra_dirs?: string[]
}

// A bot's settings as a form edits them (the token only when a new one is pasted).
export interface BotDraft { token?: string, allow: string[], refusal: string, approvers?: string[], approval?: 'ask' | 'direct' | 'admin', header?: string, reply_mode?: '' | 'steps', defaults?: CommandSetup, version?: string }

// What a bot's command does (not when it runs): the bot keeps one as its
// commands' default; a command with own_setup has its own.
export interface CommandSetup {
  action: Automation['action'], agent_id: string, prompt: string, script: AutomationScript, tags: string[], limits: AutomationLimits
  permission_mode?: Automation['permission_mode'], override_full_access?: boolean, override_extra_dirs?: string[]
}
export function setupOf(d: AutomationDraft): CommandSetup {
  return JSON.parse(JSON.stringify({
    action: d.action, agent_id: d.agent_id, prompt: d.prompt, script: d.script, tags: d.config.tags ?? [], limits: d.limits,
    permission_mode: d.permission_mode ?? 'agent', override_full_access: d.override_full_access ?? false, override_extra_dirs: d.override_extra_dirs ?? []
  }))
}
// applySetup puts a setup into a command's draft (a copy: the two never share)
export function applySetup(d: AutomationDraft, s: CommandSetup) {
  const c = JSON.parse(JSON.stringify(s)) as CommandSetup
  d.action = c.action
  d.agent_id = c.agent_id
  d.prompt = c.prompt
  d.script = { ...d.script, ...c.script }
  d.config.tags = c.tags ?? []
  d.limits = { ...d.limits, ...c.limits }
  d.permission_mode = c.permission_mode ?? 'agent'
  d.override_full_access = c.override_full_access ?? false
  d.override_extra_dirs = c.override_extra_dirs ?? []
}
export const sameSetup = (a: CommandSetup, b: CommandSetup) => JSON.stringify(a) === JSON.stringify(b)

// AutomationBody is what PATCH/POST take.
export interface AutomationBody {
  name: string, enabled: boolean, source: Automation['source'], action: Automation['action'], agent_id: string, prompt: string,
  edit_mode: Automation['edit_mode'], model_tier: NonNullable<Automation['model_tier']>, keep_context: boolean,
  config: AutomationConfig, limits: AutomationLimits, script: AutomationScript, escalate: AutomationEscalate, bot?: BotDraft, version?: string
  // ADR-074: permission override
  permission_mode?: 'agent' | 'override'
  override_full_access?: boolean
  override_extra_dirs?: string[]
}

export function automationBody(a: Pick<Automation, 'name' | 'enabled' | 'source' | 'config' | 'action' | 'agent_id' | 'prompt' | 'edit_mode' | 'model_tier' | 'keep_context' | 'limits' | 'script' | 'escalate' | 'permission_mode' | 'override_full_access' | 'override_extra_dirs'> & { bot?: BotDraft }): AutomationBody {
  const body: AutomationBody = {
    name: a.name, enabled: a.enabled, source: a.source, action: a.action, agent_id: a.agent_id, prompt: a.prompt,
    edit_mode: a.edit_mode, model_tier: '', keep_context: a.keep_context, config: a.config, limits: a.limits, script: a.script, escalate: a.escalate,
    permission_mode: a.permission_mode, override_full_access: a.override_full_access, override_extra_dirs: a.override_extra_dirs
  }
  if (isChannelSource(a.source) && a.bot) body.bot = { allow: a.bot.allow, refusal: a.bot.refusal, ...(a.bot.token ? { token: a.bot.token } : {}) }
  return body
}

// scheduleText describes when an automation runs.
export function scheduleText(a: Pick<Automation, 'source' | 'config' | 'webhook_url' | 'bot_status'>, t: (k: 'auto.every' | 'auto.sourceWebhook' | 'auto.sourceChannel' | 'auto.onEvent', p?: Record<string, string | number>) => string) {
  if (a.source === 'webhook') return a.webhook_url ?? t('auto.sourceWebhook')
  if (a.source === 'event') return t('auto.onEvent', { event: a.config.event ?? '' })
  if (isChannelSource(a.source)) {
    const bot = a.bot_status?.bot_name ? `@${a.bot_status.bot_name}` : t('auto.sourceChannel')
    const which = a.config.command ? ` · /${a.config.command}` : a.config.keywords?.length ? ` · "${a.config.keywords.join('", "')}"` : ''
    return `${bot} · ${a.source === 'discord' ? 'Discord' : 'Telegram'}${which}`
  }
  if (a.config.cron) return `${a.config.cron}${a.config.timezone ? ` (${a.config.timezone})` : ''}`
  return t('auto.every', { n: a.config.every_minutes ?? 0 })
}

export const jobStatusMeta: Record<JobStatus, { color: 'info' | 'neutral' | 'success' | 'error' | 'warning', icon: string }> = {
  pending: { color: 'neutral', icon: 'i-lucide-clock' },
  running: { color: 'info', icon: 'i-lucide-loader' },
  done: { color: 'success', icon: 'i-lucide-circle-check' },
  failed: { color: 'error', icon: 'i-lucide-circle-x' },
  cancelled: { color: 'neutral', icon: 'i-lucide-circle-slash' },
  skipped: { color: 'neutral', icon: 'i-lucide-skip-forward' },
  needs_input: { color: 'warning', icon: 'i-lucide-message-circle-question' }
}

// isChannelSource: messages of a Telegram/Discord channel start it (ADR-049).
export const isChannelSource = (s: string) => s === 'telegram' || s === 'discord'

// ---- the draft the builder page edits (ADR-042) ----
export type AutomationDraft = Omit<Automation, 'id' | 'project_id' | 'failures' | 'disabled_code' | 'disabled_reason' | 'last_run_at' | 'next_run_at' | 'last_job' | 'created_at' | 'webhook_url' | 'bot' | 'bot_status'> & { bot: BotDraft }

// AutomationTemplate is the starter library shown when a person creates an
// automation. Templates deliberately produce a draft, rather than an API
// object: the person can review and change every trigger, prompt and limit
// before saving it.
export interface AutomationTemplate {
  key: string
  name: string
  description: string
  category: string
  icon: string
  custom?: boolean
  make: () => AutomationDraft
}

export interface SavedAutomationTemplate {
  key: string
  name: string
  description: string
  category: string
  prompt: string
  source: 'schedule' | 'webhook'
}

type TemplatePatch = Partial<Pick<AutomationDraft, 'name' | 'source' | 'action' | 'prompt' | 'keep_context'>> & {
  config?: Partial<AutomationConfig>
  script?: Partial<AutomationScript>
  limits?: Partial<AutomationLimits>
}

function templateDraft(patch: TemplatePatch): AutomationDraft {
  const d = emptyDraft()
  Object.assign(d, patch)
  d.config = { ...d.config, ...(patch.config ?? {}) }
  d.script = { ...d.script, ...(patch.script ?? {}) }
  d.limits = { ...d.limits, ...(patch.limits ?? {}) }
  return d
}

// Process templates deliberately describe a bounded operating procedure, not
// a one-shot summary prompt. Keeping context lets the automation continue a
// review across turns when the goal check says that evidence or follow-up is
// still missing.
const processBrief = (task: string) => `${task}

OPERATING BRIEF

You are the accountable operator for this process, not a generic summarizer. The incoming event or report is the source material; do not fill gaps with plausible-sounding assumptions. Your job is to turn incomplete input into a decision-ready work packet that another person or system can safely act on.

1. Establish scope. State the event identity, time window, system or customer affected, requested outcome, and any context carried over from an earlier turn. If the payload does not establish one of these, mark it UNKNOWN and say what would resolve it.
2. Normalize before interpreting. Deduplicate records, preserve identifiers, reconcile dates and units, and call out conflicting fields. Quote or point to the evidence used for every material conclusion. Never treat a missing field as a negative result.
3. Separate certainty levels. Label each important statement as CONFIRMED, SUPPORTED, HYPOTHESIS, or UNKNOWN. A hypothesis may guide the next check but must not be presented as a root cause, approval, recovery, or customer commitment.
4. Work in order: understand the desired outcome; inspect the available evidence; identify impact and risk; propose the smallest safe next actions; assign owners and deadlines; define how each action will be verified; then prepare communication for the appropriate audience.
5. Prefer reversible actions and human checkpoints. Do not approve payment, deploy code, change permissions, delete data, contact a customer, promise a deadline, or close an incident solely from an inference. Clearly mark anything requiring a person, a privileged tool, or a separate approval.
6. When information is insufficient, do not stop at “need more information.” Return a prioritized request list with the exact field or artifact needed, why it matters, who should provide it, and the decision it unlocks. Continue all safe analysis that is possible with the current evidence.
7. Check your own work before finishing: identifiers are consistent; arithmetic and dates reconcile; owners are real or explicitly unassigned; every high-risk item has a next action; recommendations have a success signal; and uncertainty is visible.

REQUIRED OUTPUT

Use these headings in this order:
- Executive decision: one sentence and confidence.
- Scope and evidence: inputs used, time window, identifiers, and missing or conflicting data.
- Findings: a table with finding, certainty, evidence, impact, and risk.
- Actions: a table with action, owner, priority, deadline, dependency, and verification signal.
- Human decisions: what must be approved, by whom, and before what action.
- Communication draft: concise wording suitable for the affected audience; do not hide uncertainty.
- Open questions and next checkpoint.

Do not claim that the process is complete unless the completion criteria are demonstrably met. If this is a follow-up turn, preserve unresolved items from the previous turn and show what changed. End with either DONE plus the evidence that satisfies the goal, or CONTINUE plus the smallest next evidence request.`
const processSchedule = (name: string, prompt: string, cron: string, goal: AutomationGoal, config: Partial<AutomationConfig> = {}, limits: Partial<AutomationLimits> = {}) =>
  templateDraft({ name, source: 'schedule', action: 'chat', keep_context: true, prompt: processBrief(prompt), config: { cron, every_minutes: 0, goal, ...config }, limits: { max_parallel: 1, max_minutes: 20, max_runs_per_hour: 24, ...limits } })
const processWebhook = (name: string, prompt: string, goal: AutomationGoal, config: Partial<AutomationConfig> = {}, limits: Partial<AutomationLimits> = {}) =>
  templateDraft({ name, source: 'webhook', action: 'chat', keep_context: true, prompt: processBrief(prompt), config: { auth: 'bearer', goal, ...config }, limits: { max_parallel: 2, max_minutes: 15, max_runs_per_hour: 60, debounce_seconds: 15, debounce_max_seconds: 180, ...limits } })

// The answer of a support source (ADR-173): its final text is what the
// customer gets (sent at once, or after a person approves it).
export const supportReplyPrompt = `A customer wrote to us. The message, the ticket's history, the customer and the case types are in the payload's "support" field.

1. Find how to answer with support_kb_search (the message's gist; the case type when known). Read support_ticket_get only if the history in the payload is not enough.
2. If a case type fits better than the one set, call support_set (when you may).
3. Answer in the customer's language: short, warm, concrete; follow the playbook; give the next step. Never promise what no playbook or data supports (refunds, dates, exceptions); never reveal another customer's data or internal notes.
4. When no playbook covers it, or the customer is angry or asks for a person: say a colleague will follow up, and add a support_note with what is missing.

Your final message is sent to the customer as it is: only the reply, no notes, no headings.`

// Keep this list intentionally focused: each starter is a bounded operating
// procedure with a completion condition, not just a different prompt.
export const automationTemplates: AutomationTemplate[] = [
  { key: 'incident-response', name: 'Incident response coordinator', description: 'Correlate an alert, assess impact, produce a response plan and keep escalating until the incident is owned.', category: 'Engineering', icon: 'i-lucide-siren', make: () => processWebhook('Incident response coordinator', `Run this incident procedure on {{payload}}.

Phase 1 — normalize: extract service, environment, timestamp, alert identity and affected customer surface. If facts conflict, list the conflict.
Phase 2 — assess: classify severity and confidence; distinguish observed facts from hypotheses; identify blast radius and what is still unknown.
Phase 3 — act: propose only reversible, evidence-backed checks. Assign an owner and a deadline to every action. Never claim mitigation or recovery without a confirming signal.
Phase 4 — communicate: draft an internal update with impact, timeline, current owner, next checkpoint and explicit customer-risk wording.
Phase 5 — close: produce a follow-up list for root cause, prevention and post-incident review. If any required evidence is missing, say exactly what is missing and continue the next turn rather than declaring success.

Output: severity, confidence, facts, hypotheses, action table, communication draft, open questions and closure criteria.`, { text: 'Incident has an owner, evidence-backed next actions and explicit closure criteria', max_rounds: 3 }, {}, { debounce_key: 'alert_id', max_parallel: 1, max_runs_per_hour: 30 }) },
  { key: 'pr-review-gate', name: 'Pull request review gate', description: 'Run security, correctness, test and maintainability review with a merge decision.', category: 'Engineering', icon: 'i-lucide-git-pull-request', make: () => processWebhook('Pull request review gate', `Review this pull request: {{payload}}.

Work in passes: (1) understand the intended behavior and changed surface, (2) inspect correctness and edge cases, (3) inspect security, permissions and data exposure, (4) inspect tests and failure paths, (5) inspect maintainability and rollout risk. For every finding include file:line, severity, evidence, impact and a concrete fix. Separate confirmed findings from questions. Check whether the proposed tests actually fail before the change and cover the risky branch. End with one verdict: ready, ready with follow-up, or changes required, plus a minimum merge checklist.`, { text: 'Every review pass is complete and the verdict has file:line evidence', max_rounds: 2 }, { pull_request: true, auth: 'query', auth_name: 'token' }, { debounce_key: 'pull_request.number', max_parallel: 1, max_runs_per_hour: 20 }) },
  { key: 'release-readiness', name: 'Release readiness review', description: 'Turn release data into a go/no-go decision, rollback plan and owner checklist.', category: 'Engineering', icon: 'i-lucide-rocket', make: () => processSchedule('Release readiness review', `Prepare a release decision from {{payload}}.

Reconcile version, commits, migrations, feature flags, dependency changes, test results, monitoring coverage and open incidents. Identify blockers and distinguish missing evidence from a failed check. Build a go/no-go table with owner, evidence, deadline and rollback trigger. Draft the rollout sequence in small reversible stages, the success metrics to watch, and the rollback procedure. If the release is not ready, state the exact evidence required to re-evaluate it; do not soften a blocker into a recommendation.`, '0 8 * * 1-5', { text: 'All release checks have evidence or an explicitly owned blocker', max_rounds: 2 }, {}, { max_runs_per_hour: 10 }) },
  { key: 'security-findings-remediation', name: 'Security findings remediation', description: 'Prioritize findings, validate exploitability and produce a tracked remediation plan.', category: 'Security', icon: 'i-lucide-shield-check', make: () => processSchedule('Security findings remediation', `Process this security export: {{payload}}.

Deduplicate findings and map each to asset, owner, exposure, exploitability, affected version and evidence. Re-rank by real risk, not scanner severity alone. For each critical or high item, state the safe verification step, remediation options, compensating control, due date and verification evidence. Separate false positives that need proof from accepted risks that need an accountable approver. Finish with a small executive summary and a machine-friendly remediation table.`, '0 9 * * 1-5', { text: 'Each high-risk finding has an owner, due date and verification evidence', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) },
  { key: 'data-quality-recovery', name: 'Data quality recovery', description: 'Diagnose feed failures, find downstream impact and drive a recovery checklist.', category: 'Data', icon: 'i-lucide-table-properties', make: () => processWebhook('Data quality recovery', `Handle this data-quality event: {{payload}}.

First classify freshness, completeness, schema, validity and duplication failures. Map the affected dataset to downstream reports, jobs and decisions. Identify the earliest bad partition or change, state the evidence, and propose a safe quarantine or backfill plan. Do not silently repair or discard data. Define validation queries and a clear recovery/rollback checkpoint. End with owner assignments, customer or business impact, and the exact signal that proves the pipeline is healthy again.`, { text: 'Root cause, downstream impact and a validated recovery plan are documented', max_rounds: 3 }, {}, { debounce_key: 'dataset.id', max_parallel: 1, max_runs_per_hour: 30 }) },
  { key: 'lead-to-opportunity', name: 'Lead qualification and routing', description: 'Qualify an inbound lead, detect missing information, choose a route and draft the next touch.', category: 'Sales', icon: 'i-lucide-user-round-check', make: () => processWebhook('Lead qualification and routing', `Process this inbound lead: {{payload}}.

Extract facts without inventing them. Score fit, intent, urgency and confidence with reasons. Detect duplicates, conflicts, missing qualification fields and disallowed claims. Choose one route with a reason: sales, nurture, support, partner or reject. Draft the next message in the right tone, list the questions it must answer, and define a follow-up deadline. Never promise pricing, availability or compliance without evidence. Return a CRM-ready record plus a human-review queue for low-confidence decisions.`, { text: 'Lead has a justified route, next action, owner and confidence score', max_rounds: 2 }, {}, { debounce_key: 'lead.email', max_parallel: 2, max_runs_per_hour: 60 }) },
  { key: 'support-escalation', name: 'Support case resolution coordinator', description: 'Summarize a case, search for a safe resolution path and escalate with complete context.', category: 'Customer Service', icon: 'i-lucide-headphones', make: () => processWebhook('Support case resolution coordinator', `Coordinate this support case: {{payload}}.

Build a timeline and state the customer's actual goal. Separate reported symptoms, confirmed facts and assumptions. Check known resolution paths and cite which evidence supports each one. Draft a response that is accurate, empathetic and does not expose private data or make an unsupported promise. Decide whether to resolve, request information, or escalate; if escalating, include impact, reproduction, logs/evidence, attempted fixes and the exact receiving team. Define a follow-up time and closure condition.`, { text: 'Case has a safe next action, accountable queue and closure condition', max_rounds: 3 }, {}, { debounce_key: 'case.id', max_parallel: 2, max_runs_per_hour: 60 }) },
  { key: 'support-reply', name: 'Customer reply (support pack)', description: 'Answers a customer message from the Customers menu: looks up the playbooks, replies with their source, hands hard cases to a person.', category: 'Customer Service', icon: 'i-lucide-messages-square', make: () => templateDraft({ name: 'Customer reply', source: 'webhook', action: 'chat', prompt: supportReplyPrompt, config: { auth: 'bearer' }, limits: { max_parallel: 2, max_minutes: 5, max_runs_per_hour: 120 } }) },
  { key: 'invoice-exception-control', name: 'Invoice exception control', description: 'Validate invoice fields, detect fraud or duplicate signals and prepare a human approval packet.', category: 'Finance & Risk', icon: 'i-lucide-receipt-text', make: () => processWebhook('Invoice exception control', `Review this invoice event: {{payload}}.

Extract vendor, invoice number, dates, currency, subtotal, tax, total, purchase order and payment terms. Reconcile arithmetic and compare available purchase-order, vendor and duplicate signals. Classify each mismatch as confirmed, likely or unresolved. Recommend approve, hold for clarification, or escalate for fraud review, but never approve or pay automatically. Produce the evidence packet, missing documents, accountable reviewer and a time-bounded next step.`, { text: 'Invoice has reconciled fields, risk classification and a named human decision owner', max_rounds: 2 }, {}, { debounce_key: 'invoice.id', max_parallel: 1, max_runs_per_hour: 60 }) },
  { key: 'customer-health-intervention', name: 'Customer health intervention', description: 'Diagnose churn risk, verify the evidence and create an owner-specific intervention plan.', category: 'Customer', icon: 'i-lucide-user-minus', make: () => processSchedule('Customer health intervention', `Review this customer-health export: {{payload}}.

Segment accounts by risk and confidence. For each high-risk account, connect product usage, unresolved support issues, commercial value, stakeholder changes and recent sentiment without inferring facts that are not present. Identify the smallest useful intervention, owner, deadline and success signal. Draft a respectful outreach plan and a save-risk summary. Separate accounts needing data cleanup from accounts needing a human conversation.`, '0 8 * * 1', { text: 'Every high-risk account has evidence, owner, intervention and success signal', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) },
  { key: 'campaign-experiment-loop', name: 'Campaign experiment loop', description: 'Diagnose campaign performance, separate signal from noise and propose the next measurable experiment.', category: 'Marketing', icon: 'i-lucide-megaphone', make: () => processSchedule('Campaign experiment loop', `Analyze this campaign snapshot: {{payload}}.

Validate metric definitions, attribution window, sample size and comparison baseline before interpreting changes. Segment performance by audience, channel, creative and funnel stage. Separate observed results from causal hypotheses. Identify wasted spend and promising signals, then propose no more than three experiments with hypothesis, change, guardrail, owner, duration and success threshold. End with what should not be changed yet because evidence is insufficient.`, '0 9 * * 1', { text: 'Recommendations are tied to validated metrics and measurable experiments', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) },
  { key: 'vendor-risk-control', name: 'Vendor risk control', description: 'Monitor supplier changes, evidence expiry and concentration risk with accountable follow-up.', category: 'Procurement', icon: 'i-lucide-building-2', make: () => processSchedule('Vendor risk control', `Review this vendor risk export: {{payload}}.

Normalize each vendor and evidence date. Flag incidents, expired certifications, SLA deterioration, concentration, access risk and missing owners. Rank by business impact and evidence confidence. For each material risk, propose a verification request, interim control, accountable owner and due date. Do not mark a vendor safe because a field is blank; treat unknown as unknown and state the decision needed.`, '0 9 * * 1', { text: 'Material vendor risks have evidence, owner, due date and interim control', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) },
  { key: 'knowledge-maintenance', name: 'Knowledge base maintenance', description: 'Find stale knowledge, verify sources and prepare prioritized updates.', category: 'Knowledge', icon: 'i-lucide-book-open-check', make: () => processSchedule('Knowledge base maintenance', `Review this knowledge-base report: {{payload}}.

Find stale, duplicated, contradictory and unanswered content. Verify each proposed change against the cited source date and product context. Rank by user impact and confidence. For the highest-value items, draft the replacement outline, owner, source links, review deadline and deprecation plan for the old page. Keep uncertain claims in a review queue rather than presenting them as fact.`, '0 10 * * 1', { text: 'High-impact knowledge gaps have verified sources and assigned updates', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) },
  { key: 'compliance-evidence-pack', name: 'Compliance evidence pack', description: 'Map controls to evidence, identify gaps and prepare an audit-ready request list.', category: 'Compliance', icon: 'i-lucide-clipboard-check', make: () => processSchedule('Compliance evidence pack', `Prepare an evidence review from {{payload}}.

Map every control to its owner, evidence type, period, expiry and reviewer. Distinguish missing, stale, incomplete, contradictory and valid evidence. Never mark a control compliant from a claim alone. Build a prioritized gap register with requested artifact, accountable owner, deadline, compensating control and verification step. Finish with an audit-ready executive summary and a list of items requiring human sign-off.`, '0 9 * * 1', { text: 'Every control is mapped to valid evidence or an explicitly owned gap', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) },
  { key: 'weekly-operating-review', name: 'Weekly operating review', description: 'Turn cross-functional metrics into decisions, risks and accountable actions.', category: 'Operations', icon: 'i-lucide-layout-dashboard', make: () => processSchedule('Weekly operating review', `Run the weekly operating review from {{payload}}.

Validate period, definitions and missing data. Compare actuals with targets and prior period, then explain only changes supported by evidence. Group issues by customer impact, revenue, delivery, reliability and people capacity. For each material issue create a decision or action with owner, deadline, dependency, risk and success metric. End with decisions needed from leadership and explicitly state what the data cannot tell us.`, '0 16 * * 5', { text: 'Material variances have decisions, owners, deadlines and success metrics', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) },
  { key: 'agent-quality-review', name: 'Agent quality and cost review', description: 'Inspect agent outcomes, failure modes, cost and human overrides, then propose one controlled improvement.', category: 'AI Governance', icon: 'i-lucide-gauge', make: () => processSchedule('Agent quality and cost review', `Evaluate this agent run report: {{payload}}.

Check whether the success criteria are measurable. Segment failures by prompt, tool, model, permissions, data quality and human handoff. Compare quality, latency, cost, retries and override rate with the previous period. Identify regressions and confounders. Recommend exactly one improvement with a test design, rollback trigger, owner and metric; do not recommend shipping a change without a before/after measurement plan.`, '0 11 * * 1', { text: 'One measurable improvement is proposed with baseline, guardrail and rollback trigger', max_rounds: 2 }, {}, { max_runs_per_hour: 5 }) }
]

// Featured means “recommended starting point based on current demand signals”
// (not a fabricated user rating). The UI renders these with a star.
export const featuredTemplateKeys = new Set([
  'incident-response', 'pr-review-gate', 'release-readiness', 'security-findings-remediation',
  'data-quality-recovery', 'lead-to-opportunity', 'support-escalation', 'invoice-exception-control',
  'customer-health-intervention', 'campaign-experiment-loop', 'support-reply', 'compliance-evidence-pack',
  'weekly-operating-review', 'agent-quality-review'
])

export function emptyDraft(): AutomationDraft {
  return {
    name: '', enabled: true, source: 'schedule',
    config: { every_minutes: 0, cron: '0 8 * * 1-5', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, auth: 'bearer', auth_name: '', channel_id: '', keywords: [], scope: '', command: '', command_description: '', command_arg: '', skill: '', reply_mode: '', own_setup: false, pull_request: false, notify_channel_id: '', notify_chat_id: '', tags: [] },
    bot: { token: '', allow: [], refusal: '' },
    action: 'script', agent_id: '', prompt: '', edit_mode: 'worktree', model_tier: '', keep_context: false,
    script: { lang: 'bash', body: '', timeout_s: 300 },
    escalate: { when: 'never', action: 'chat', agent_id: '', prompt: '' }, // a script calls no agent in (ADR-057)
    limits: { max_parallel: 1, max_minutes: 0, max_runs_per_hour: 0, daily_cost_usd: 0, disable_after_failures: 5, debounce_seconds: 0, debounce_key: '', debounce_max_seconds: 0 },
    // ADR-074: "agent" follows the agent's own permissions; only "override" needs these
    permission_mode: 'agent', override_full_access: false, override_admin_by: '', override_extra_dirs: []
  }
}

export function automationTemplateFromLibrary(template: SavedAutomationTemplate): AutomationTemplate {
  return {
    ...template,
    icon: 'i-lucide-zap',
    custom: true,
    make: () => {
      const draft = emptyDraft()
      draft.name = template.name
      draft.prompt = template.prompt
      draft.source = template.source
      draft.action = 'chat'
      return draft
    }
  }
}

export function draftFrom(a: Automation): AutomationDraft {
  const e = emptyDraft()
  const b = JSON.parse(JSON.stringify(automationBody(a))) as AutomationDraft
  const d: AutomationDraft = {
    ...e, ...b, config: { ...e.config, ...b.config }, limits: { ...e.limits, ...b.limits },
    bot: { token: '', allow: [...(a.bot?.allow ?? [])], refusal: a.bot?.refusal ?? '' },
    script: { ...e.script, ...(b.script?.lang ? b.script : {}) }, escalate: { ...e.escalate, ...(b.escalate?.when ? b.escalate : {}) }
  }
  if ((d.action as string) === 'task') d.action = 'chat' // Giao Việc is gone (ADR-057): the agent gets the message
  return d
}

const draftObjects = ['config', 'limits', 'script', 'escalate', 'bot'] as const
const draftScalars = ['name', 'enabled', 'source', 'action', 'agent_id', 'prompt', 'edit_mode', 'keep_context'] as const
const allowed: Record<string, readonly string[]> = {
  source: ['schedule', 'webhook', 'event', 'telegram', 'discord'], action: ['script', 'chat'], edit_mode: ['worktree', 'direct'], model_tier: ['', 'strong', 'balanced', 'fast'],
  'script.lang': ['bash', 'node', 'python'], 'escalate.when': ['never'],
  'config.auth': ['bearer', 'header', 'query']
}
const fits = (key: string, v: unknown) => !allowed[key] || allowed[key]!.includes(v as string)

// mergeDraft applies an agent's partial draft: only fields the draft has, of
// the same type and an allowed value (anything else is ignored, so a bad
// block cannot break the form or a later Save). A schedule is either a cron
// or every N minutes: setting one clears the other. It returns the top-level
// keys it changed.
export function mergeDraft(d: AutomationDraft, patch: Record<string, unknown>): string[] {
  const changed: string[] = []
  for (const k of draftScalars) {
    const v = k === 'action' && patch[k] === 'task' ? 'chat' : patch[k] // Giao Việc is gone (ADR-057): the agent gets the message
    if (v === undefined || typeof v !== typeof d[k] || !fits(k, v)) continue
    ;(d as Record<string, unknown>)[k] = v
    changed.push(k)
  }
  for (const k of draftObjects) {
    const v = patch[k]
    if (!v || typeof v !== 'object' || Array.isArray(v)) continue
    const target = d[k] as Record<string, unknown>
    let any = false
    for (const [f, x] of Object.entries(v as Record<string, unknown>)) {
      if (!(f in target) || x === null || typeof x !== typeof target[f] || !fits(`${k}.${f}`, x)) continue
      if (k === 'bot' && f === 'token') continue // a person pastes it; never from the chat
      target[f] = x
      any = true
    }
    if (k === 'config' && any) {
      const c = v as Record<string, unknown>
      if (typeof c.every_minutes === 'number' && c.every_minutes > 0 && c.cron === undefined) d.config.cron = ''
      if (typeof c.cron === 'string' && c.cron.trim() && c.every_minutes === undefined) d.config.every_minutes = 0
    }
    if (any) changed.push(k)
  }
  return changed
}
