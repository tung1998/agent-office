#!/usr/bin/env node
// Imports a chosen few skills of headcount (cbrock84/headcount, MIT) into the
// office's machine library (<office>/library/skills), not into the repo. Only
// those the library had no match for are picked (PICK); the rest of its 172
// overlap what the library has or do not fit a personal office. Each skill
// folder is copied whole (references/, scripts/), its SKILL.md marked
// `source:`, `category` (PICK) and `tags: [<department>]`. A skill of the same
// name already there that is not from this repo is left alone.
//
//   node scripts/sync-headcount-library.mjs                  # the pinned commit, the office of cwd
//   node scripts/sync-headcount-library.mjs --sha=<commit> --dir=<office>/library/skills   # (then pin it here)
import { cpSync, existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

const REPO = 'cbrock84/headcount'
const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const SHA = arg('sha') ?? '7d3ddf170a85a67565cbbdcda4e9539d32e5a2c4'
// skill → a category the office library knows (SkillArea)
const PICK = {
  'agent-hierarchy': 'agent-orchestration',
  'saas-idea-validator': 'business-strategy',
  'unit-economics': 'finance',
  'capital-allocation': 'finance',
  'scenario-planning': 'business-strategy',
  'pricing-and-packaging': 'business-strategy',
  'contract-review': 'legal',
  'data-migration': 'database'
}

// the office home as the server finds it: OFFICE_HOME, a .office above cwd, ~/.agent-office
function officeHome() {
  if (process.env.OFFICE_HOME) return process.env.OFFICE_HOME
  for (let d = process.cwd(); ; d = dirname(d)) {
    if (existsSync(join(d, '.office', 'office.db'))) return join(d, '.office')
    if (dirname(d) === d) return join(homedir(), '.agent-office')
  }
}
const skillsOut = resolve(arg('dir') ?? join(officeHome(), 'library', 'skills'))
const blob = path => `https://github.com/${REPO}/blob/${SHA}/${path}`

const src = mkdtempSync(join(tmpdir(), 'headcount-'))
try {
  execFileSync('git', ['clone', '-q', '--filter=blob:none', `https://github.com/${REPO}`, src], { stdio: 'inherit' })
  execFileSync('git', ['checkout', '-q', SHA], { cwd: src })

  // skill → its department folder
  const dept = {}
  for (const d of readdirSync(join(src, 'plugins')).sort()) {
    if (!existsSync(join(src, 'plugins', d, 'skills'))) continue
    for (const s of readdirSync(join(src, 'plugins', d, 'skills'))) dept[s] ??= d
  }

  let skills = 0, kept = 0
  for (const [name, category] of Object.entries(PICK)) {
    if (!dept[name]) { console.warn('missing upstream:', name); continue }
    const path = `plugins/${dept[name]}/skills/${name}`
    const from = join(src, path)
    const to = join(skillsOut, name)
    if (existsSync(join(to, 'SKILL.md'))) {
      const source = frontmatter(readFileSync(join(to, 'SKILL.md'), 'utf8')).match(/^source:\s*(.*)$/m)?.[1] ?? ''
      if (!source.includes(REPO)) { kept++; continue }
    }
    const md = readFileSync(join(from, 'SKILL.md'), 'utf8')
    if (!md.startsWith('---')) { console.warn('skip (no frontmatter):', path); continue }
    rmSync(to, { recursive: true, force: true })
    cpSync(from, to, { recursive: true, dereference: true })
    writeFileSync(join(to, 'SKILL.md'), withMeta(md, { category, tags: [dept[name]], source: blob(`${path}/SKILL.md`) }))
    skills++
  }
  console.log(`${SHA.slice(0, 7)}: ${skills} skills → ${skillsOut}` +
    (kept ? `, ${kept} of the same name kept (not from ${REPO})` : ''))
} finally {
  rmSync(src, { recursive: true, force: true })
}

function frontmatter(md) {
  return md.match(/^---\s*\n([\s\S]*?)\n---/)?.[1] ?? ''
}

// withMeta sets `category`, `tags: [a, b]` and `source` in the frontmatter,
// dropping the ones there (tags may be a YAML block list).
function withMeta(md, meta) {
  const lines = []
  let skipping = false
  for (const line of frontmatter(md).split('\n')) {
    if (/^(category|tags|source):/.test(line)) { skipping = true; continue }
    if (skipping && /^(\s+\S|-(\s|$))/.test(line)) continue
    skipping = false
    lines.push(line)
  }
  const clean = s => String(s).replace(/[\[\],:#"']/g, '').trim()
  lines.push(`category: ${clean(meta.category)}`, `tags: [${meta.tags.map(clean).join(', ')}]`, `source: ${meta.source}`)
  return md.replace(/^---\s*\n[\s\S]*?\n---/, `---\n${lines.join('\n')}\n---`)
}
