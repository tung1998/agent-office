// What needs a person ("cần xử lý"), acted on from anywhere: the header's
// list, a toast, a browser notification (public/sw.js).
export interface Incident { kind: string, severity: 'error' | 'warning' | 'info', project_id: string, project_name: string, title: string, detail: string, at: string, link: string, id: string, key: string, prompt?: string }

export const isApproval = (x: Incident) => x.kind === 'approval' || x.kind === 'patch'

// an item's text, short enough for a toast or a notification: a diff of many
// files says how many and the first few; anything else is cut at max
export function incidentText(x: Incident, files: (n: number, list: string) => string, max = 140): string {
  let text = x.title
  const list = x.kind === 'patch' ? text.split(', ') : []
  if (list.length > 3) text = files(list.length, list.slice(0, 3).map(f => f.split('/').pop()).join(', '))
  return text.length > max ? text.slice(0, max - 1).trimEnd() + '…' : text
}

// what can be done to an item right from a notification, besides opening it
export type IncidentAct = 'approve' | 'reject' | 'seen' | 'continue' | 'retryNow' | 'cancelRetry' | 'dismiss'
export function incidentActs(x: Incident): IncidentAct[] {
  if (isApproval(x)) return ['approve', 'reject']
  if (x.kind === 'unread') return ['seen']
  if (x.kind === 'stalled') return ['continue']
  if (x.kind === 'retrying' || x.kind === 'quota_wait') return ['retryNow', 'cancelRetry'] // it goes on by itself: after a passing API failure (ADR-260), once the quota resets (ADR-269)
  return ['dismiss']
}

// decided first by someone else, or by office on its own (an agent's
// permission, a bot's direct mode): the 409 office answers, with the card as
// it is now. deciding: who is deciding it right now ("" = it is decided)
export interface AlreadyDecided { status: string, by: string, deciding: string }
export function alreadyDecided(e: unknown): AlreadyDecided | null {
  const r = e as { status?: number, statusCode?: number, data?: { action?: { status: string, decided_by?: string, deciding?: string }, patch?: { status: string, decided_by?: string, deciding?: string } } }
  if ((r?.status ?? r?.statusCode) !== 409) return null
  const x = r.data?.action ?? r.data?.patch
  return x ? { status: x.status, by: x.decided_by ?? '', deciding: x.deciding ?? '' } : null
}
// what it says instead of an error: being decided, or decided already
export function alreadyDecidedText(d: AlreadyDecided, t: ReturnType<typeof useLang>['t']) {
  const who = (by: string) => by.startsWith('auto:') ? t('bell.byOffice') : by || '?'
  return d.deciding ? t('bell.deciding', { by: who(d.deciding) }) : t('bell.decided', { by: who(d.by) })
}

// approve or reject (reject = skip: its agent is not run again about it)
export async function decideApproval(x: Pick<Incident, 'kind' | 'id'>, what: 'approve' | 'reject') {
  const base = x.kind === 'patch' ? '/api/patches/' : '/api/actions/'
  await $fetch(`${base}${x.id}/${what}`, { method: 'POST', body: what === 'reject' ? { skip: true } : {} })
}

export async function actOnIncident(x: Incident, what: IncidentAct) {
  switch (what) {
    case 'approve':
    case 'reject':
      return decideApproval(x, what)
    case 'seen': // a chat answered since the person looked
      return $fetch(`/api/conversations/${x.id}/seen`, { method: 'POST', body: { seen: true } })
    case 'continue': // a chat stopped for tokens: its agent goes on
      return $fetch(`/api/conversations/${x.id}/messages`, { method: 'POST', body: { text: x.prompt } })
    case 'retryNow': // goes on now, not at its planned time
      return $fetch(`/api/conversations/${x.id}/retry-now`, { method: 'POST' })
    case 'cancelRetry':
      return $fetch(`/api/conversations/${x.id}/retry`, { method: 'DELETE' })
    case 'dismiss':
      return $fetch('/api/incidents/dismiss', { method: 'POST', body: { key: x.key } })
  }
}

// the browser's permission for notifications, asked once from the header
export function useBrowserNotify() {
  const supported = import.meta.client && 'Notification' in window && 'serviceWorker' in navigator
  const permission = useState<NotificationPermission>('notify-permission', () => supported ? Notification.permission : 'denied')
  async function enable() {
    if (!supported) return
    permission.value = await Notification.requestPermission()
  }
  return { supported, permission, enable }
}
