export type ModelTier = 'strong' | 'balanced' | 'fast'
export type AgentTier = 'lead' | 'manager' | 'worker'

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
  models: string[]
  is_default: boolean
  enabled: boolean
  status: 'unknown' | 'ok' | 'error'
  status_detail: string
  checked_at: string | null
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
export interface PermCap { id: string, group: PermGroup, readonly label: string, readonly description: string, min: PermLevel, icon: string }
export const permCaps: PermCap[] = [
  { id: 'propose', group: 'code', get label() { return useLang().t('perm.cap.propose.label') }, get description() { return useLang().t('perm.cap.propose.description') }, min: 'propose', icon: 'i-lucide-message-square-diff' },
  { id: 'code.apply', group: 'code', get label() { return useLang().t('perm.cap.codeApply.label') }, get description() { return useLang().t('perm.cap.codeApply.description') }, min: 'edit', icon: 'i-lucide-pencil' },
  { id: 'tools.mcp', group: 'commands', get label() { return useLang().t('perm.cap.toolsMcp.label') }, get description() { return useLang().t('perm.cap.toolsMcp.description') }, min: 'read', icon: 'i-lucide-plug-zap' },
  { id: 'tools.mcp.write', group: 'commands', get label() { return useLang().t('perm.cap.toolsMcpWrite.label') }, get description() { return useLang().t('perm.cap.toolsMcpWrite.description') }, min: 'operate', icon: 'i-lucide-plug' },
  { id: 'chat.send', group: 'commands', get label() { return useLang().t('perm.cap.chatSend.label') }, get description() { return useLang().t('perm.cap.chatSend.description') }, min: 'operate', icon: 'i-lucide-send' },
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
export const presetCaps = (l: PermLevel) => permCaps.filter(c => permRank(c.min) <= permRank(l)).map(c => c.id)
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
  org_model_id: string
  key: string
  name: string
  tier: AgentTier
  role: string
  description: string
  reports_to: string[]
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
export function effortLabel(e: string, t: (k: `effort.${Effort}`) => string) {
  return (EFFORTS as readonly string[]).includes(e) ? t(`effort.${e as Effort}`) : e
}

export interface Governance {
  mode: string
  quorum?: number
  veto?: string[]
  notes?: string
}

export interface OrgModel {
  id: string
  repo_id: string
  source_template_id: string
  key: string
  name: string
  description: string
  kind: 'solo' | 'team' | 'council' | 'custom'
  governance: Governance
  builtin: boolean
  is_template: boolean
  agent_count: number
  tiers: Partial<Record<AgentTier, number>>
  agents?: Agent[]
  updated_at: string
}

export interface Project {
  id: string
  name: string
  path: string // '' for a machine-wide helper
  scope: 'folder' | 'machine'
  git_remote: string
  description: string
  exists: boolean
  model: OrgModel | null
  created_at: string
}

// Getter-backed records: `tierLabel.lead` etc. keep working at every call
// site while re-evaluating the translation at render/access time.
export const tierLabel: Record<AgentTier, string> = {
  get lead() { return useLang().t('tier.lead') },
  get manager() { return useLang().t('tier.manager') },
  get worker() { return useLang().t('tier.worker') }
}
export const modelTierLabel: Record<ModelTier, string> = {
  get strong() { return useLang().t('tier.model.strong') },
  get balanced() { return useLang().t('tier.model.balanced') },
  get fast() { return useLang().t('tier.model.fast') }
}
export const kindLabel: Record<string, string> = {
  get solo() { return useLang().t('org.kind.solo') },
  get team() { return useLang().t('org.kind.team') },
  get council() { return useLang().t('org.kind.council') },
  get custom() { return useLang().t('org.kind.custom') }
}
export const kindIcon: Record<string, string> = {
  solo: 'i-lucide-user',
  team: 'i-lucide-users',
  council: 'i-lucide-landmark',
  custom: 'i-lucide-shapes'
}
export const governanceLabel: Record<string, string> = {
  get single() { return useLang().t('org.governance.single') },
  get hierarchy() { return useLang().t('org.governance.hierarchy') },
  get council() { return useLang().t('org.governance.council') }
}
