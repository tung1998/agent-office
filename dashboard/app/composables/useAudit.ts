// Change log (ADR-043): who changed what, where from, before/after.
export interface AuditEntry {
  id: string, action: string, actor_kind: 'human' | 'agent' | 'automation' | 'system' | '', actor_id: string, actor_name: string
  approved_by: string, via: string, project_id: string, project_name: string, conversation_id: string, job_id: string
  task_id: string, action_id: string, resource: string, resource_id: string, target: string
  detail: Record<string, unknown>, before: Record<string, unknown> | null, after: Record<string, unknown> | null, ok: boolean, at: string
}
export interface AuditDiff { key: string, before: unknown, after: unknown }

const isObj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)

// auditDiff lists the fields that differ, nested objects as dotted keys,
// arrays compared whole; sorted by key.
export function auditDiff(before: Record<string, unknown> | null, after: Record<string, unknown> | null, prefix = ''): AuditDiff[] {
  const out: AuditDiff[] = []
  const keys = new Set([...Object.keys(before ?? {}), ...Object.keys(after ?? {})])
  for (const k of keys) {
    const b = before?.[k]
    const a = after?.[k]
    const key = prefix + k
    if (isObj(b) && isObj(a)) out.push(...auditDiff(b, a, key + '.'))
    else if (JSON.stringify(b) !== JSON.stringify(a)) out.push({ key, before: b, after: a })
  }
  return out.sort((x, y) => x.key.localeCompare(y.key))
}

export const whoIcon = (k: string) => ({ human: 'i-lucide-user', agent: 'i-lucide-bot', automation: 'i-lucide-alarm-clock' } as Record<string, string>)[k] ?? 'i-lucide-cog'
