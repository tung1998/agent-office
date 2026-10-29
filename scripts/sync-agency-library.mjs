#!/usr/bin/env node
// Imports agents from agency-agents (MIT) into templates/library/agency/
// (ADR-154): one Markdown file each, office frontmatter on top, the code
// executable samples dropped, output templates retained.
//
//   node scripts/sync-agency-library.mjs            # the pinned commit
//   node scripts/sync-agency-library.mjs <sha>      # another commit (then pin it here)
import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { agencyBody, agentDescription, agentDescriptionVi } from './lib/agent-library.mjs'

const REPO = 'msitarzewski/agency-agents'
const SHA = process.argv[2] ?? '652b2692f8d8ec2b1f1a38e559073e287dfdc469'
// upstream division → office category (ADR-090, plus design); others are left out
const DIVISIONS = {
  engineering: 'code', testing: 'code', security: 'code',
  design: 'design',
  product: 'plan', 'project-management': 'plan', finance: 'plan',
  support: 'customer', sales: 'customer',
  marketing: 'content', 'paid-media': 'content'
}
const out = join(dirname(fileURLToPath(import.meta.url)), '..', 'templates', 'library', 'agency')

const tree = await (await fetch(`https://api.github.com/repos/${REPO}/git/trees/${SHA}?recursive=1`)).json()
if (!tree.tree) throw new Error(`tree: ${JSON.stringify(tree).slice(0, 200)}`)
const files = tree.tree.map(x => x.path).filter(p => {
  const parts = p.split('/')
  return parts.length === 2 && DIVISIONS[parts[0]] && p.endsWith('.md') && !/readme/i.test(parts[1])
})

rmSync(out, { recursive: true, force: true })
mkdirSync(out, { recursive: true })
const seen = new Set()
let n = 0
for (const path of files) {
  const [division, file] = path.split('/')
  const raw = await (await fetch(`https://raw.githubusercontent.com/${REPO}/${SHA}/${path}`)).text()
  const m = raw.match(/^---\n([\s\S]*?)\n---\n([\s\S]*)$/)
  if (!m) { console.warn('skip (no frontmatter):', path); continue }
  const fm = Object.fromEntries(m[1].split('\n').map(l => l.match(/^(\w+):\s*(.*)$/)).filter(Boolean)
    .map(([, k, v]) => [k, v.replace(/^["']|["']$/g, '').trim()]))
  if (!fm.name) { console.warn('skip (no name):', path); continue }
  // a key office accepts: no division prefix, lowercase, ≤41 chars, unique
  // (the division prefix only goes when what is left still says something:
  // engineering-frontend-developer → frontend-developer, sales-coach stays)
  const base = file.replace(/\.md$/, '')
  const rest = base.replace(new RegExp(`^${division}-`), '')
  let key = (rest.includes('-') ? rest : base).toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-').replace(/^[^a-z]+/, '').slice(0, 41).replace(/-+$/, '')
  if (seen.has(key)) key = `${division}-${key}`.slice(0, 41).replace(/-+$/, '')
  if (!key || seen.has(key)) { console.warn('skip (key):', path); continue }
  seen.add(key)
  const body = agencyBody(key, m[2])
  const head = {
    key, name: fm.name, role: fm.vibe ?? '', description: agentDescription('agency', key, fm.description ?? ''),
    ...(agentDescriptionVi('agency', key) ? { description_vi: agentDescriptionVi('agency', key) } : {}),
    category: DIVISIONS[division], emoji: fm.emoji ?? '', model_tier: 'balanced', level: 'read',
    source: `https://github.com/${REPO}/blob/${SHA}/${path}`
  }
  const yaml = Object.entries(head).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join('\n')
  writeFileSync(join(out, `${key}.md`), `---\n${yaml}\n---\n\n${body}\n`)
  n++
}
console.log(`${n} agents → ${out}`)
