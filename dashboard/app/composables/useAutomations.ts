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
  // telegram | discord (ADR-049): the channel, which messages (a keyword; none = any), within a scope
  channel_id?: string, keywords?: string[], scope?: string
  // …or a custom slash command of the bot, and the text typed after it ("" = none)
  command?: string, command_description?: string, command_arg?: string
  skill?: string // the command calls this project skill
  reply_mode?: '' | 'answer' | 'steps' // what the bot shows of a run ('' = the bot's own)
  pull_request?: boolean // a GitHub/Bitbucket PR webhook: runs on a PR opened or updated, with {{diff}}
  notify_channel_id?: string, notify_chat_id?: string // what a run answers goes to this bot's chat too
  tags?: string[] // put on the chat of each run
  ends_at?: string | null // a schedule turns itself off then (as a Burn); none = runs until turned off
}
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
export interface AutomationScript { lang: 'bash' | 'node' | 'python', body: string, timeout_s?: number }
export interface AutomationEscalate { when: 'never' | 'failure' | 'signal', action: 'chat' | 'task', agent_id: string, prompt: string } // kept for old ones: never runs (ADR-057)
export interface Automation {
  id: string
  project_id: string
  name: string
  enabled: boolean
  source: 'schedule' | 'webhook' | 'telegram' | 'discord'
  config: AutomationConfig
  action: 'chat' | 'script'
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
export interface BotDraft { token?: string, allow: string[], refusal: string, approvers?: string[], approval?: 'ask' | 'direct' | 'admin', header?: string, reply_mode?: '' | 'steps', version?: string }

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
export function scheduleText(a: Pick<Automation, 'source' | 'config' | 'webhook_url' | 'bot_status'>, t: (k: 'auto.every' | 'auto.sourceWebhook' | 'auto.sourceChannel', p?: Record<string, string | number>) => string) {
  if (a.source === 'webhook') return a.webhook_url ?? t('auto.sourceWebhook')
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

// prReviewDraft: a GitHub/Bitbucket PR webhook, the agent reviews the diff and the chat hears it
export function prReviewDraft(t: (k: 'auto.prName' | 'auto.prPrompt') => string): AutomationDraft {
  const d = emptyDraft()
  d.name = t('auto.prName')
  d.source = 'webhook'
  d.action = 'chat'
  d.config.pull_request = true
  d.config.auth = 'query'
  d.config.auth_name = 'token'
  d.prompt = t('auto.prPrompt')
  d.limits.max_runs_per_hour = 20
  return d
}

export function emptyDraft(): AutomationDraft {
  return {
    name: '', enabled: true, source: 'schedule',
    config: { every_minutes: 0, cron: '0 8 * * 1-5', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, auth: 'bearer', auth_name: '', channel_id: '', keywords: [], scope: '', command: '', command_description: '', command_arg: '', skill: '', reply_mode: '', pull_request: false, notify_channel_id: '', notify_chat_id: '', tags: [] },
    bot: { token: '', allow: [], refusal: '' },
    action: 'script', agent_id: '', prompt: '', edit_mode: 'worktree', model_tier: '', keep_context: false,
    script: { lang: 'bash', body: '', timeout_s: 300 },
    escalate: { when: 'never', action: 'chat', agent_id: '', prompt: '' }, // a script calls no agent in (ADR-057)
    limits: { max_parallel: 1, max_minutes: 0, max_runs_per_hour: 0, daily_cost_usd: 0, disable_after_failures: 5, debounce_seconds: 0, debounce_key: '', debounce_max_seconds: 0 },
    // ADR-074: "agent" follows the agent's own permissions; only "override" needs these
    permission_mode: 'agent', override_full_access: false, override_admin_by: '', override_extra_dirs: []
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
  source: ['schedule', 'webhook', 'telegram', 'discord'], action: ['script', 'chat'], edit_mode: ['worktree', 'direct'], model_tier: ['', 'strong', 'balanced', 'fast'],
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
    const v = patch[k]
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
