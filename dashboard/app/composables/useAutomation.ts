export type ItemKind = 'skill' | 'agent' | 'mcp'

export interface AutoLocation {
  type: 'user' | 'project' | 'local' | 'plugin' | 'cursor' | 'claude_desktop' | 'codex'
  label: string
  path: string
  project_path?: string
  project_id?: string
  editable: boolean
}
export interface AutoItem {
  kind: ItemKind
  name: string
  description: string
  location: AutoLocation
  meta?: Record<string, string>
  config?: Record<string, unknown>
  disabled?: boolean // mcp: turned off, Claude Code does not load it
}
// MCP sources office can turn off and on (internal/automation/mcptoggle.go)
export const mcpToggleable = (l: AutoLocation) => l.editable && (l.type === 'user' || l.type === 'project' || l.type === 'local')
export interface MachineProject {
  path: string
  name: string
  exists: boolean
  project_id?: string
  skills: number
  agents: number
  mcp: number
  claude_md: boolean
  agents_md: boolean
}
export interface Inventory { items: AutoItem[], projects: MachineProject[] }
export interface TemplateInput { key: string, label: string, kind: 'env' | 'header' | 'arg', secret: boolean, required: boolean, default?: string, hint?: string }
export interface MCPTemplate {
  id?: string
  name: string
  title: string
  description: string
  category?: string
  homepage?: string
  config: Record<string, unknown>
  inputs: TemplateInput[]
  source?: 'catalog' | 'registry' | 'library'
  auth?: string
}
export interface LibraryItem {
  kind: ItemKind
  name: string
  description: string
  updated_at?: string
  files?: Record<string, string>
  template?: MCPTemplate
}
export interface Finding { rule: string, severity: 'refuse' | 'warn', message: string, line: number, excerpt: string }

export const kindMeta: Record<ItemKind, { label: string, icon: string }> = {
  skill: { label: 'Skill', icon: 'i-lucide-sparkles' },
  agent: { label: 'Agent', icon: 'i-lucide-bot' },
  mcp: { label: 'MCP server', icon: 'i-lucide-plug-zap' }
}

export const locationIcon: Record<AutoLocation['type'], string> = {
  user: 'i-lucide-monitor',
  project: 'i-lucide-folder-git-2',
  local: 'i-lucide-lock',
  plugin: 'i-lucide-puzzle',
  cursor: 'i-lucide-mouse-pointer-2',
  claude_desktop: 'i-lucide-app-window',
  codex: 'i-lucide-terminal'
}

export const refOf = (it: AutoItem) => ({
  kind: it.kind, name: it.name, type: it.location.type, path: it.location.path, project_path: it.location.project_path ?? ''
})

// One shared scan for all automation pages; rescans on demand.
export function useInventory() {
  const inv = useState<Inventory | null>('automation-inventory', () => null)
  const loading = useState('automation-scanning', () => false)
  async function scan() {
    loading.value = true
    try {
      inv.value = await $fetch<Inventory>('/api/automation/scan')
    } finally {
      loading.value = false
    }
  }
  if (!inv.value && !loading.value) scan()
  return { inv, loading, scan }
}
