export type ModelTier = 'strong' | 'balanced' | 'fast'

export interface Provider {
  id: string
  name: string
  kind: string
  preset: string
  base_url: string
  has_api_key: boolean
  api_key_hint: string
  api_key_env: string
  tier_models: Partial<Record<ModelTier, string>>
  limit_caps?: Partial<Record<'five_hour' | 'seven_day', number>>
  models: string[]
  is_default: boolean
  enabled: boolean
  status: 'unknown' | 'ok' | 'error'
  status_detail: string
  checked_at: string | null
  own_account?: boolean // signed in to a Claude account of its own (ADR-178)
}

export interface ProviderKind {
  kind: string
  label: string
  description: string
  common: boolean
  detected?: { installed?: boolean, version?: string, env_key?: string }
  needs_key: boolean
  is_cli: boolean
  base_url_hint: string
  tier_models: Partial<Record<ModelTier, string>>
}

export interface ProviderPreset {
  id: string
  name: string
  group: 'gateway' | 'global' | 'china' | 'local'
  base_url: string
  key_url?: string
  key_env?: string
  need_key: boolean
  note?: string
}
export interface ProviderStat {
  provider_id: string
  calls: number
  errors: number
  input_tokens: number
  output_tokens: number
  cost_usd: number
  avg_ms: number
  last_used_at: string | null
  top_model: string
  days: { day: string, calls: number, cost_usd: number }[]
}
export type PermLevel = 'read' | 'propose' | 'check' | 'edit' | 'operate'
// nested packages: each includes the ones before it
// label/description are getters (evaluated at render time) so existing call
// sites (`permLevels[i].label`) keep working while text follows the language.
export const permLevels: { level: PermLevel, readonly label: string, readonly description: string, icon: string }[] = [
  { level: 'read', get label() { return useLang().t('perm.level.read.label') }, get description() { return useLang().t('perm.level.read.description') }, icon: 'i-lucide-eye' },
  { level: 'propose', get label() { return useLang().t('perm.level.propose.label') }, get description() { return useLang().t('perm.level.propose.description') }, icon: 'i-lucide-message-square-diff' },
  { level: 'check', get label() { return useLang().t('perm.level.check.label') }, get description() { return useLang().t('perm.level.check.description') }, icon: 'i-lucide-flask-conical' },
  { level: 'edit', get label() { return useLang().t('perm.level.edit.label') }, get description() { return useLang().t('perm.level.edit.description') }, icon: 'i-lucide-pencil' },
  { level: 'operate', get label() { return useLang().t('perm.level.operate.label') }, get description() { return useLang().t('perm.level.operate.description') }, icon: 'i-lucide-server-cog' }
]
export const permRank = (l?: string) => Math.max(0, permLevels.findIndex(x => x.level === l))
export const permOf = (l?: string) => permLevels[permRank(l)]!
export function agentLevel(p: Permissions): PermLevel {
  if (p.caps) return p.caps.reduce<PermLevel>((m, id) => { const c = permCaps.find(x => x.id === id); return c && permRank(c.min) > permRank(m) ? c.min : m }, 'read')
  if (p.level && permLevels.some(x => x.level === p.level)) return p.level
  return p.read_only ? 'read' : 'propose'
}

// single capabilities; a package is a preset of them (same list as internal/perm)
export type PermGroup = 'code' | 'commands' | 'git' | 'ops'
export interface PermCap { id: string, group: PermGroup, readonly label: string, readonly description: string, min: PermLevel, icon: string, optIn?: boolean }
export const permCaps: PermCap[] = [
  { id: 'propose', group: 'code', get label() { return useLang().t('perm.cap.propose.label') }, get description() { return useLang().t('perm.cap.propose.description') }, min: 'propose', icon: 'i-lucide-message-square-diff' },
  { id: 'code.apply', group: 'code', get label() { return useLang().t('perm.cap.codeApply.label') }, get description() { return useLang().t('perm.cap.codeApply.description') }, min: 'edit', icon: 'i-lucide-pencil' },
  { id: 'tools.mcp', group: 'commands', get label() { return useLang().t('perm.cap.toolsMcp.label') }, get description() { return useLang().t('perm.cap.toolsMcp.description') }, min: 'read', icon: 'i-lucide-plug-zap' },
  { id: 'tools.web', group: 'commands', get label() { return useLang().t('perm.cap.toolsWeb.label') }, get description() { return useLang().t('perm.cap.toolsWeb.description') }, min: 'read', icon: 'i-lucide-globe', optIn: true },
  { id: 'self.manage', group: 'commands', get label() { return useLang().t('perm.cap.selfManage.label') }, get description() { return useLang().t('perm.cap.selfManage.description') }, min: 'propose', icon: 'i-lucide-calendar-cog', optIn: true },
  { id: 'tools.mcp.write', group: 'commands', get label() { return useLang().t('perm.cap.toolsMcpWrite.label') }, get description() { return useLang().t('perm.cap.toolsMcpWrite.description') }, min: 'operate', icon: 'i-lucide-plug' },
  { id: 'chat.send', group: 'commands', get label() { return useLang().t('perm.cap.chatSend.label') }, get description() { return useLang().t('perm.cap.chatSend.description') }, min: 'operate', icon: 'i-lucide-send' },
  { id: 'chat.cross', group: 'commands', get label() { return useLang().t('perm.cap.chatCross.label') }, get description() { return useLang().t('perm.cap.chatCross.description') }, min: 'operate', icon: 'i-lucide-waypoints' },
  { id: 'commands.run', group: 'commands', get label() { return useLang().t('perm.cap.commandsRun.label') }, get description() { return useLang().t('perm.cap.commandsRun.description') }, min: 'check', icon: 'i-lucide-square-terminal' },
  { id: 'git.commit', group: 'git', get label() { return useLang().t('perm.cap.gitCommit.label') }, get description() { return useLang().t('perm.cap.gitCommit.description') }, min: 'edit', icon: 'i-lucide-git-commit-horizontal' },
  { id: 'git.branch', group: 'git', get label() { return useLang().t('perm.cap.gitBranch.label') }, get description() { return useLang().t('perm.cap.gitBranch.description') }, min: 'operate', icon: 'i-lucide-git-branch' },
  { id: 'ops.process', group: 'ops', get label() { return useLang().t('perm.cap.opsProcess.label') }, get description() { return useLang().t('perm.cap.opsProcess.description') }, min: 'operate', icon: 'i-lucide-rotate-cw' },
  { id: 'ops.container', group: 'ops', get label() { return useLang().t('perm.cap.opsContainer.label') }, get description() { return useLang().t('perm.cap.opsContainer.description') }, min: 'operate', icon: 'i-lucide-container' }
]
export const permGroups: { id: PermGroup, readonly label: string, icon: string }[] = [
  { id: 'code', get label() { return useLang().t('perm.group.code') }, icon: 'i-lucide-code' },
  { id: 'commands', get label() { return useLang().t('perm.group.commands') }, icon: 'i-lucide-square-terminal' },
  { id: 'git', get label() { return useLang().t('perm.group.git') }, icon: 'i-lucide-git-fork' },
  { id: 'ops', get label() { return useLang().t('perm.group.ops') }, icon: 'i-lucide-server-cog' }
]
export const presetCaps = (l: PermLevel) => permCaps.filter(c => !c.optIn && permRank(c.min) <= permRank(l)).map(c => c.id)
export const agentCaps = (p: Permissions) => p.caps ?? presetCaps(agentLevel(p))

export interface CommandPack { id: string, label: string, icon?: string, commands: string[], custom?: boolean }

export interface Permissions {
  level?: PermLevel
  read_only: boolean
  caps?: string[] | null
  commands?: string[] | null
  processes?: string[] | null
  containers?: string[] | null
  requires_approval?: boolean
  // ADR-074: agent full access as administrator
  full_access?: boolean
  full_access_by?: string
  extra_dirs?: string[]
}

export interface Agent {
  id: string
  project_id: string
  key: string
  name: string
  role: string
  description: string
  provider_id: string
  fallback_provider_ids?: string[] // tried next, top to bottom
  model_tier: ModelTier
  llm_model: string
  effort?: string // thinking level by default (EFFORTS; '' = the CLI's own)
  instructions: string
  permissions: Permissions
  avatar?: AvatarSpec
  sort: number
  enabled?: boolean // false = paused: out of chat, gets no work
}

// how hard the model thinks, lowest first ('max' shows as Ultra)
export const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'] as const
export type Effort = typeof EFFORTS[number]
// the levels a model takes on a connection (storage.EffortsFor): Antigravity
// names it in the model (gemini-3.8-flash-high), so only its default model picks one
export function effortsFor(kind: string | undefined, model: string | undefined): readonly string[] {
  if (kind === 'antigravity_cli') return model ? [] : EFFORTS.slice(0, 3)
  return EFFORTS
}
export function effortLabel(e: string, t: (k: `effort.${Effort}`) => string) {
  return (EFFORTS as readonly string[]).includes(e) ? t(`effort.${e as Effort}`) : e
}

// A starter pack (ADR-099): the agents a new project begins with and the
// workflows installed for it.
export interface Pack {
  key: string
  name: string
  description: string
  default: string
  workflows: string[]
  agents: { key: string, name: string, role: string, model_tier: ModelTier }[]
}

// The agent library (ADR-153, ADR-154): the list has no instructions; one is
// read in full (LibraryAgent) when opened, then added or used as a model.
export type LibraryCategory = 'code' | 'design' | 'plan' | 'customer' | 'content'
export interface LibraryEntry {
  id: string // "<set>/<key>"
  set: 'office' | 'agency' | 'ecc' | 'aitmpl'
  key: string
  name: string
  role: string
  description: string
  description_vi?: string
  category: LibraryCategory
  emoji?: string
  model_tier: ModelTier
  level: PermLevel
  featured?: boolean
  source?: string
  words: number
  skills?: string[] // the library skills it keeps at hand once added (ADR-185)
  requires_pack?: string // workflows, skills and data provided by this project pack
}
export interface LibraryAgent extends LibraryEntry {
  instructions: string
  permissions: Permissions
}

export interface Project {
  id: string
  name: string
  path: string // '' for a machine-wide helper
  scope: 'folder' | 'machine'
  git_remote: string
  description: string
  exists: boolean
  agent_count: number // 0: no agent yet (pick a starter pack)
  default_agent_id: string // answers when nobody is named
  features?: FeatureId[] // features on (feature packs)
  agents?: Agent[]
  created_at: string
}

// the features of the packs (internal/features): a typo in a gate fails typecheck
export type FeatureId = 'git' | 'ops' | 'support'
// features of a pack off by default (support, ADR-171): unknown = off
const defaultOff: FeatureId[] = ['support']
// hasFeature: a feature of the project's packs is on (unknown: on, but a pack off by default)
export function hasFeature(p: Pick<Project, 'features'> | undefined, f: FeatureId): boolean {
  return p?.features ? p.features.includes(f) : !defaultOff.includes(f)
}

// Getter-backed records re-evaluate the translation at every access.
export const modelTierLabel: Record<ModelTier, string> = {
  get strong() { return useLang().t('tier.model.strong') },
  get balanced() { return useLang().t('tier.model.balanced') },
  get fast() { return useLang().t('tier.model.fast') }
}
