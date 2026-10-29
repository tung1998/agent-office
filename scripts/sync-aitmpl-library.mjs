#!/usr/bin/env node
// Imports Claude Code Templates (davila7/claude-code-templates, MIT, the
// catalog of https://aitmpl.com): its agents into templates/library/aitmpl/
// (ADR-154, one Markdown file each, office frontmatter on top, the long
// "<example>" blocks cut from the description) and its skills into the
// office's machine library (<office>/library/skills), not into the repo, each
// SKILL.md marked `source:` with `category` and `tags`. A skill of the same
// name already there that is not from this repo is left alone; skills under a
// source-available license (Anthropic's docx/pdf/pptx/xlsx) are not copied.
// Commands, hooks, settings, MCPs, sandbox and mods are not imported (ADR-216).
//
//   node scripts/sync-aitmpl-library.mjs                  # the pinned commit, the office of cwd
//   node scripts/sync-aitmpl-library.mjs --sha=<commit> --dir=<office>/library/skills   # (then pin it here)
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { agentDescription, agentDescriptionVi } from './lib/agent-library.mjs'

const REPO = 'davila7/claude-code-templates'
const BASE = 'cli-tool/components'
const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const SHA = arg('sha') ?? 'aff83062f3d0e6a882f23e52611d5811539e6047'
// agent folder → office category (ADR-090); the rest are code
const AGENT_CATEGORY = {
  accessibility: 'design', 'ui-analysis': 'design',
  'business-marketing': 'plan', 'deep-research-team': 'plan', finance: 'plan', 'obsidian-ops-team': 'plan',
  documentation: 'content', 'ffmpeg-clip-team': 'content', 'podcast-creator-team': 'content'
}
const AGENT_KEY_CATEGORY = {
  'customer-support': 'customer', 'customer-success-manager': 'customer', 'sales-automator': 'customer', 'sales-engineer': 'customer',
  'content-marketer': 'content', 'seo-specialist': 'content', 'se-ux-ui-designer': 'design', 'ux-researcher': 'design',
  plan: 'plan', planner: 'plan', 'implementation-plan': 'plan', specification: 'plan', 'refine-issue': 'plan',
  'atlassian-requirements-to-jira': 'plan', 'legal-advisor': 'plan', 'payment-integration': 'code', 'fintech-engineer': 'code'
}
const TIER = { opus: 'strong', sonnet: 'balanced', haiku: 'fast' }
// skill folder → a category the office library knows (SkillArea), the folder kept as a tag
const SKILL_CATEGORY = {
  'ai-maestro': 'ai-agents', analytics: 'data', 'business-marketing': 'marketing', career: 'productivity',
  'creative-design': 'design', curviate: 'growth', 'design-to-code': 'frontend', doordash: 'shopping',
  'enterprise-communication': 'collaboration', git: 'developer-tools', 'gmod-addon-maker': 'game-development',
  'open-banking-io': 'finance', pocketbase: 'backend', railway: 'devops', scientific: 'science', sentry: 'devops',
  sports: 'data', utilities: 'developer-tools', 'web-data': 'data', 'workflow-automation': 'automation'
}
const NOT_OPEN = /proprietary|all rights reserved/i

// the office home as the server finds it: OFFICE_HOME, a .office above cwd, ~/.agent-office
function officeHome() {
  if (process.env.OFFICE_HOME) return process.env.OFFICE_HOME
  for (let d = process.cwd(); ; d = dirname(d)) {
    if (existsSync(join(d, '.office', 'office.db'))) return join(d, '.office')
    if (dirname(d) === d) return join(homedir(), '.agent-office')
  }
}
const agentsOut = join(dirname(fileURLToPath(import.meta.url)), '..', 'templates', 'library', 'aitmpl')
const skillsOut = resolve(arg('dir') ?? join(officeHome(), 'library', 'skills'))
const blob = path => `https://github.com/${REPO}/blob/${SHA}/${BASE}/${path}`

const src = mkdtempSync(join(tmpdir(), 'aitmpl-'))
try {
  execFileSync('git', ['clone', '-q', '--filter=blob:none', `https://github.com/${REPO}`, src], { stdio: 'inherit' })
  execFileSync('git', ['checkout', '-q', SHA], { cwd: src })
  const root = join(src, BASE)

  rmSync(agentsOut, { recursive: true, force: true })
  mkdirSync(agentsOut, { recursive: true })
  const seen = new Set()
  let agents = 0
  for (const dir of readdirSync(join(root, 'agents')).sort()) {
    if (!existsSync(join(root, 'agents', dir)) || dir.includes('.')) continue
    for (const file of readdirSync(join(root, 'agents', dir)).filter(f => f.endsWith('.md')).sort()) {
      const m = readFileSync(join(root, 'agents', dir, file), 'utf8').match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n([\s\S]*)$/)
      if (!m) { console.warn('skip (no frontmatter):', `${dir}/${file}`); continue }
      const fm = parseFrontmatter(m[1])
      // a key office accepts: lowercase, starts with a letter, ≤41 chars
      const key = file.replace(/\.md$/, '').toLowerCase()
        .replace(/[^a-z0-9-]+/g, '-').replace(/^[^a-z]+/, '').slice(0, 41).replace(/-+$/, '')
      const description = shortDescription(fm.description ?? '')
      if (!key || !description) { console.warn('skip (key or description):', `${dir}/${file}`); continue }
      if (seen.has(key)) { console.warn('skip (same key in another folder):', `${dir}/${file}`); continue }
      seen.add(key)
      const head = {
        key, name: fm.name || key, role: '', description: agentDescription('aitmpl', key, description),
        ...(agentDescriptionVi('aitmpl', key) ? { description_vi: agentDescriptionVi('aitmpl', key) } : {}),
        category: AGENT_KEY_CATEGORY[key] ?? AGENT_CATEGORY[dir] ?? 'code', emoji: '',
        model_tier: TIER[(fm.model ?? '').toLowerCase()] ?? 'balanced', level: 'read',
        source: blob(`agents/${dir}/${file}`)
      }
      const yaml = Object.entries(head).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join('\n')
      writeFileSync(join(agentsOut, `${key}.md`), `---\n${yaml}\n---\n\n${m[2].replace(/\n{3,}/g, '\n\n').trim()}\n`)
      agents++
    }
  }

  let skills = 0, kept = 0, closed = 0
  const copied = new Set()
  for (const cat of readdirSync(join(root, 'skills')).sort()) {
    if (cat.includes('.')) continue
    for (const { name, path } of skillDirs(join(root, 'skills', cat), cat)) {
      if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(name) || copied.has(name)) continue
      const from = join(root, 'skills', path)
      if (readdirSync(from).some(f => /^LICEN[CS]E/i.test(f) && NOT_OPEN.test(readFileSync(join(from, f), 'utf8')))) { closed++; continue }
      const to = join(skillsOut, name)
      if (existsSync(join(to, 'SKILL.md'))) {
        const source = frontmatter(readFileSync(join(to, 'SKILL.md'), 'utf8')).match(/^source:\s*(.*)$/m)?.[1] ?? ''
        if (!source.includes(REPO)) { kept++; continue }
      }
      const md = readFileSync(join(from, 'SKILL.md'), 'utf8')
      if (!md.startsWith('---')) { console.warn('skip (no frontmatter):', path); continue }
      rmSync(to, { recursive: true, force: true })
      cpSync(from, to, { recursive: true, dereference: true })
      writeFileSync(join(to, 'SKILL.md'), withMeta(md, {
        category: SKILL_CATEGORY[cat] ?? cat, tags: [cat], source: blob(`skills/${path}`)
      }))
      copied.add(name)
      skills++
    }
  }
  console.log(`${SHA.slice(0, 7)}: ${agents} agents → ${agentsOut}; ${skills} skills → ${skillsOut}` +
    (kept ? `, ${kept} of the same name kept (not from ${REPO})` : '') + (closed ? `, ${closed} not open source skipped` : ''))
} finally {
  rmSync(src, { recursive: true, force: true })
}

// skillDirs lists the skills of a category folder: <cat>/<name>/SKILL.md, or
// one level deeper for a folder grouping several (scientific/document-skills/xlsx).
function skillDirs(dir, rel, deeper = true) {
  const out = []
  for (const name of readdirSync(dir, { withFileTypes: true }).filter(d => d.isDirectory()).map(d => d.name).sort()) {
    if (existsSync(join(dir, name, 'SKILL.md'))) out.push({ name, path: `${rel}/${name}` })
    else if (deeper) out.push(...skillDirs(join(dir, name), `${rel}/${name}`, false))
  }
  return out
}

// parseFrontmatter reads the flat `key: value` lines, a quoted value
// unescaped, a block value (`|`, `>`) joined.
function parseFrontmatter(text) {
  const out = {}
  const lines = text.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/^([\w-]+):\s*(.*)$/)
    if (!m) continue
    let v = m[2].replace(/\s+#.*$/, '').trim()
    if (/^[|>][-+]?$/.test(v)) {
      const block = []
      while (i + 1 < lines.length && /^(\s|$)/.test(lines[i + 1])) block.push(lines[++i].trim())
      v = block.join('\n')
    } else if (v.startsWith('"')) {
      try { v = JSON.parse(v) } catch { v = v.replace(/^"|"$/g, '') }
    } else v = v.replace(/^'|'$/g, '')
    out[m[1]] = v
  }
  return out
}

// shortDescription keeps what comes before the examples (an escaped "\n"
// in the source is a newline), at most ~400 chars.
function shortDescription(d) {
  let s = d.replace(/\\n/g, '\n').split(/<example>|\n\s*\n|\bExamples?:/)[0].replace(/\s+/g, ' ').trim()
    .replace(/\s*(Specifically|For example|Examples?)\s*:?$/i, '')
  if (s.length > 400) s = (s.slice(0, 400).match(/^[\s\S]*[.!?](?=\s)/)?.[0] ?? s.slice(0, 400)).trim()
  return s.replace(/[:\s]+$/, '')
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
