// Reads the company's office map from office's own database, read only: the
// rooms (this project and the projects it links to), the agents, who is
// working (a running job), and who just gave work to or talked with whom.
// Office writes a hand-off as a job titled "From → To" and a message sent into
// another chat as a send_message action; the map reads those, it adds nothing.
import { existsSync, realpathSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

// where office keeps its data, as office looks for it: OFFICE_DB,
// OFFICE_HOME, the nearest .office/ above the repo, else ~/.agent-office
export function findDB(repo) {
  if (process.env.OFFICE_DB) return process.env.OFFICE_DB
  if (process.env.OFFICE_HOME) return join(process.env.OFFICE_HOME, 'office.db')
  for (let d = resolve(repo); ; d = dirname(d)) {
    const f = join(d, '.office', 'office.db')
    if (existsSync(f)) return f
    if (dirname(d) === d) break
  }
  return join(homedir(), '.agent-office', 'office.db')
}

// a read-only query function: node:sqlite (Node 22.5+), else the sqlite3 CLI
async function opener(file) {
  try {
    process.removeAllListeners('warning') // node:sqlite is "experimental": not worth a line in the log
    const { DatabaseSync } = await import('node:sqlite')
    const db = new DatabaseSync(file, { readOnly: true })
    return (sql, ...args) => db.prepare(sql).all(...args)
  } catch {
    return (sql, ...args) => {
      let i = 0
      const text = sql.replace(/\?/g, () => `'${String(args[i++]).replace(/'/g, "''")}'`)
      const out = execFileSync('sqlite3', ['-readonly', '-json', file, text], { encoding: 'utf8' })
      return out.trim() ? JSON.parse(out) : []
    }
  }
}

const json = (s, d) => { try { return JSON.parse(s) } catch { return d } }

export async function openOffice(repo) {
  const file = findDB(repo)
  if (!existsSync(file)) throw new Error(`Không thấy dữ liệu office ở ${file} (đặt OFFICE_DB hoặc OFFICE_HOME)`)
  const q = await opener(file)
  let real = resolve(repo)
  try { real = realpathSync(real) } catch {}
  const own = process.env.OFFICE_PROJECT
    ? q('SELECT id FROM repos WHERE id = ?', process.env.OFFICE_PROJECT)[0]
    : q('SELECT id FROM repos WHERE path IN (?, ?)', resolve(repo), real)[0]
  if (!own) throw new Error(`Office chưa có project ở ${real} (đặt OFFICE_PROJECT là id project)`)
  return { file, load: windowSec => load(q, own.id, windowSec) }
}

// the company and the projects it links to, step by step, at most 50
function linked(q, id) {
  const out = []
  const seen = new Set()
  const next = [id]
  while (next.length && out.length < 50) {
    const p = next.shift()
    if (seen.has(p)) continue
    seen.add(p)
    const r = q('SELECT id, name, default_agent_id FROM repos WHERE id = ?', p)[0]
    if (!r) continue
    out.push(r)
    const pol = q('SELECT value FROM settings WHERE key = ?', `policy:${p}`)[0]
    next.push(...(json(pol?.value, {}).linked_projects || []))
  }
  return out
}

function load(q, projectId, windowSec = 120) {
  const now = new Date()
  const since = new Date(now - windowSec * 1000).toISOString()
  const recent = new Date(now - 6 * 3600 * 1000).toISOString()
  const rooms = []
  const byName = new Map() // project id → agent name (lower case) → id
  for (const r of linked(q, projectId)) {
    const agents = q('SELECT id, key, name, role, avatar, enabled FROM agents WHERE project_id = ? ORDER BY sort, name', r.id)
    const running = q("SELECT agent_id, title FROM jobs WHERE project_id = ? AND status = 'running' AND agent_id <> ''", r.id)
    const doing = new Map()
    for (const j of running) if (!doing.has(j.agent_id) || !doing.get(j.agent_id)) doing.set(j.agent_id, j.title)
    const on = agents.filter(a => a.enabled)
    const head = on.find(a => a.id === r.default_agent_id) || on[0] || agents.find(a => a.id === r.default_agent_id)
    byName.set(r.id, new Map(agents.map(a => [a.name.toLowerCase(), a.id])))
    rooms.push({
      project_id: r.id, name: r.name, head: head?.id || '',
      desks: agents.map(a => {
        const av = json(a.avatar, {})
        return { id: a.id, key: a.key, name: a.name, role: a.role, icon: av.icon || '', color: av.color || '', off: !a.enabled, busy: doing.has(a.id), doing: doing.get(a.id) || '' }
      })
    })
  }
  const ids = rooms.map(r => r.project_id)
  const marks = ids.map(() => '?').join(',')
  const events = []
  // hand-offs: a job titled "From → To" (a workflow's: "Workflow → To (role)"),
  // run by To; one going back the way an earlier one came is a report
  const handoffs = q(`SELECT project_id, agent_id, title, created_at FROM jobs WHERE project_id IN (${marks}) AND created_at > ? AND title LIKE '% → %' ORDER BY created_at`, ...ids, recent)
  const pairs = new Set()
  for (const j of handoffs) {
    const [from, rest] = j.title.split(' → ')
    const to = rest.replace(/ \(.*\)$/, '')
    const names = byName.get(j.project_id)
    const fromId = names?.get(from.trim().toLowerCase()) || ''
    const back = pairs.has(`${j.project_id}|${to.toLowerCase()}|${from.toLowerCase()}`)
    pairs.add(`${j.project_id}|${from.toLowerCase()}|${to.toLowerCase()}`)
    if (j.created_at <= since) continue
    const role = /\((.*)\)$/.exec(rest)?.[1]
    events.push({ at: j.created_at, kind: back ? 'report' : 'delegate', from_project: j.project_id, from: fromId, to_project: j.project_id, to: j.agent_id, text: back ? '' : (role || '') })
  }
  // a message one of its agents sent into another chat
  const sent = q(`SELECT a.project_id, a.proposed_by, a.args, a.created_at, c.project_id AS to_project, c.agent_id AS to_agent
    FROM actions a LEFT JOIN conversations c ON c.id = a.target_id
    WHERE a.project_id IN (${marks}) AND a.kind = 'send_message' AND a.status = 'done' AND a.created_at > ?`, ...ids, since)
  for (const s of sent) {
    if (!s.to_project) continue
    const text = String(json(s.args, {}).message || '').split('\n')[0].slice(0, 80)
    events.push({ at: s.created_at, kind: 'message', from_project: s.project_id, from: byName.get(s.project_id)?.get(String(s.proposed_by).toLowerCase()) || '', to_project: s.to_project, to: s.to_agent || '', text })
  }
  events.sort((a, b) => a.at < b.at ? -1 : 1)
  return { now: now.toISOString(), rooms, events }
}
