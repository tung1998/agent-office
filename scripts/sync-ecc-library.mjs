#!/usr/bin/env node
// Imports ECC (affaan-m/ECC, MIT): its agents into templates/library/ecc/
// (ADR-154, one Markdown file each, office frontmatter on top, the
// boilerplate "Prompt Defense Baseline" dropped) and its skills into the
// office's machine library (<office>/library/skills), not into the repo,
// each SKILL.md marked `source:`. A skill of the same name already there
// that is not from ECC (the person's own, or another catalog's) is left alone.
//
//   node scripts/sync-ecc-library.mjs                  # the pinned commit, the office of cwd
//   node scripts/sync-ecc-library.mjs --sha=<commit> --dir=<office>/library/skills   # (then pin it here)
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { agentDescription, agentDescriptionVi } from './lib/agent-library.mjs'

const REPO = 'affaan-m/ECC'
const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const SHA = arg('sha') ?? '90606c8315ee09cafc303515c5f33024db6b9882'
// ECC agents are mostly code; these go elsewhere (ADR-090)
const CATEGORY = {
  'chief-of-staff': 'plan', planner: 'plan', 'gan-planner': 'plan', 'spec-miner': 'plan',
  'marketing-agent': 'content', 'seo-specialist': 'content',
  'a11y-architect': 'design'
}
const TIER = { opus: 'strong', sonnet: 'balanced', haiku: 'fast' }

// the office home as the server finds it: OFFICE_HOME, a .office above cwd, ~/.agent-office
function officeHome() {
  if (process.env.OFFICE_HOME) return process.env.OFFICE_HOME
  for (let d = process.cwd(); ; d = dirname(d)) {
    if (existsSync(join(d, '.office', 'office.db'))) return join(d, '.office')
    if (dirname(d) === d) return join(homedir(), '.agent-office')
  }
}
const agentsOut = join(dirname(fileURLToPath(import.meta.url)), '..', 'templates', 'library', 'ecc')
const skillsOut = resolve(arg('dir') ?? join(officeHome(), 'library', 'skills'))
const blob = path => `https://github.com/${REPO}/blob/${SHA}/${path}`

const src = mkdtempSync(join(tmpdir(), 'ecc-'))
try {
  execFileSync('git', ['clone', '-q', `https://github.com/${REPO}`, src], { stdio: 'inherit' })
  execFileSync('git', ['checkout', '-q', SHA], { cwd: src })

  rmSync(agentsOut, { recursive: true, force: true })
  mkdirSync(agentsOut, { recursive: true })
  let agents = 0
  for (const file of readdirSync(join(src, 'agents')).filter(f => f.endsWith('.md')).sort()) {
    const m = readFileSync(join(src, 'agents', file), 'utf8').match(/^---\n([\s\S]*?)\n---\n([\s\S]*)$/)
    if (!m) { console.warn('skip (no frontmatter):', file); continue }
    const fm = Object.fromEntries(m[1].split('\n').map(l => l.match(/^(\w+):\s*(.*)$/)).filter(Boolean)
      .map(([, k, v]) => [k, v.replace(/^["']|["']$/g, '').trim()]))
    // a key office accepts: lowercase, starts with a letter, ≤41 chars
    const key = file.replace(/\.md$/, '').toLowerCase()
      .replace(/[^a-z0-9-]+/g, '-').replace(/^[^a-z]+/, '').slice(0, 41).replace(/-+$/, '')
    if (!key || !fm.name || !fm.description) { console.warn('skip (key, name or description):', file); continue }
    const body = m[2]
      .replace(/^## Prompt Defense Baseline\n[\s\S]*?(?=^## )/m, '')
      .replace(/\n{3,}/g, '\n\n').trim()
    const head = {
      key, name: fm.name, role: '', description: agentDescription('ecc', key, fm.description),
      ...(agentDescriptionVi('ecc', key) ? { description_vi: agentDescriptionVi('ecc', key) } : {}),
      category: CATEGORY[key] ?? 'code', emoji: '', model_tier: TIER[fm.model] ?? 'balanced', level: 'read',
      source: blob(`agents/${file}`)
    }
    const yaml = Object.entries(head).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join('\n')
    writeFileSync(join(agentsOut, `${key}.md`), `---\n${yaml}\n---\n\n${body}\n`)
    agents++
  }

  let skills = 0, kept = 0
  for (const name of readdirSync(join(src, 'skills')).sort()) {
    const from = join(src, 'skills', name)
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(name) || !existsSync(join(from, 'SKILL.md'))) continue
    const to = join(skillsOut, name)
    if (existsSync(join(to, 'SKILL.md'))) {
      const source = frontmatter(readFileSync(join(to, 'SKILL.md'), 'utf8')).match(/^source:\s*(.*)$/m)?.[1] ?? ''
      if (!source.includes(REPO)) { kept++; continue }
    }
    rmSync(to, { recursive: true, force: true })
    cpSync(from, to, { recursive: true, dereference: true })
    const md = readFileSync(join(to, 'SKILL.md'), 'utf8')
    if (!md.startsWith('---')) { rmSync(to, { recursive: true, force: true }); console.warn('skip (no frontmatter):', name); continue }
    const source = `source: ${blob(`skills/${name}`)}`
    writeFileSync(join(to, 'SKILL.md'), md.replace(/^---\s*\n([\s\S]*?)\n---/, (_, fm) =>
      `---\n${fm.split('\n').filter(l => !/^source:/.test(l)).join('\n')}\n${source}\n---`))
    skills++
  }
  console.log(`${SHA.slice(0, 7)}: ${agents} agents → ${agentsOut}; ${skills} skills → ${skillsOut}` +
    (kept ? `, ${kept} of the same name kept (not from ECC)` : ''))
} finally {
  rmSync(src, { recursive: true, force: true })
}

function frontmatter(md) {
  return md.match(/^---\s*\n([\s\S]*?)\n---/)?.[1] ?? ''
}
