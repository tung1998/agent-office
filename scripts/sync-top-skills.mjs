#!/usr/bin/env node
// Imports the skills of "10 Claude Code Skills được yêu thích nhất" into the
// office's machine library (<office>/library/skills), not into the repo.
// Checked against each repo's LICENSE before import:
//   - obra/superpowers (MIT): skills/* — process skills (brainstorming, TDD,
//     systematic-debugging, …)
//   - multica-ai/andrej-karpathy-skills (MIT, declared in README only, no
//     LICENSE file in the repo): skills/karpathy-guidelines
//   - anthropics/skills: only the Apache-2.0 skills (each one's own
//     LICENSE.txt); not imported: skills/docx, skills/pdf, skills/pptx,
//     skills/xlsx (license: Proprietary, source-available only) and
//     skills/doc-coauthoring (no license field, no LICENSE.txt)
//   - nextlevelbuilder/ui-ux-pro-max-skill (MIT): .claude/skills/*
//   - addyosmani/agent-skills (MIT): skills/*
//   - Leonxlnx/taste-skill (MIT): skills/taste-skill
// mattpocock/skills is already in scripts/sync-nhatanh-library.mjs at the
// same commit as upstream HEAD — no sha bump needed.
// Left out, not skill repos (CLI tools/apps that happen to ship a skill
// wrapper), noted in docs/research/references.md instead:
//   - DietrichGebert/ponytail — CLI/Docker review tool with per-assistant
//     extensions (pi-extension, gemini-extension.json, opencode.json);
//     skills/ is a thin wrapper around the CLI
//   - safishamsi/graphify — Python CLI/library (pyproject.toml, tools/,
//     benchmarks/); a single skill.md wraps the CLI
//   - JuliusBrussee/caveman — CLI framework with per-assistant generated/
//     output, benchmarks, deploy scripts; skills/ wraps the CLI's verbs
//
//   node scripts/sync-top-skills.mjs [--dir=<office>/library/skills] [--dry-run]
// To move a repo to a newer commit, change its sha below.
import { cpSync, existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const dryRun = process.argv.includes('--dry-run')

// dirs: folders whose subfolders are skills; category: one the office library knows (SkillArea)
// exclude: skill folder names under that dir to skip, with a reason logged
const SOURCES = [
  { repo: 'obra/superpowers', sha: '22f0b463e7a5e579904dd6211a520321035526f7', tag: 'superpowers',
    dirs: { skills: 'process' } },
  { repo: 'multica-ai/andrej-karpathy-skills', sha: '2c606141936f1eeef17fa3043a72095b4765b9c2', tag: 'karpathy-skills',
    dirs: { skills: 'development' } },
  { repo: 'anthropics/skills', sha: 'ca45fc496a3436251f191354091d557da31c15b5', tag: 'anthropic-skills',
    dirs: { skills: 'development' },
    exclude: {
      docx: 'license: Proprietary (source-available only)', pdf: 'license: Proprietary (source-available only)',
      pptx: 'license: Proprietary (source-available only)', xlsx: 'license: Proprietary (source-available only)',
      'doc-coauthoring': 'no license field, no LICENSE.txt'
    } },
  { repo: 'nextlevelbuilder/ui-ux-pro-max-skill', sha: 'b7d5353e0130feb8c5574ef1251f8a1db1873ccb', tag: 'ui-ux-pro-max',
    dirs: { '.claude/skills': 'design' } },
  { repo: 'addyosmani/agent-skills', sha: '301b02b12bff98e4688e6984af6556802322bbde', tag: 'agent-skills',
    dirs: { skills: 'development' } },
  { repo: 'Leonxlnx/taste-skill', sha: '9b4bf937a1f69348126959c15d2f99a73e65699b', tag: 'taste-skill',
    dirs: { skills: 'design' } }
]

// the office home as the server finds it: OFFICE_HOME, a .office above cwd, ~/.agent-office
function officeHome() {
  if (process.env.OFFICE_HOME) return process.env.OFFICE_HOME
  for (let d = process.cwd(); ; d = dirname(d)) {
    if (existsSync(join(d, '.office', 'office.db'))) return join(d, '.office')
    if (dirname(d) === d) return join(homedir(), '.agent-office')
  }
}
const skillsOut = resolve(arg('dir') ?? join(officeHome(), 'library', 'skills'))

const seen = new Set()
for (const { repo, sha, tag, dirs, exclude } of SOURCES) {
  const src = mkdtempSync(join(tmpdir(), 'top-skills-'))
  try {
    execFileSync('git', ['clone', '-q', '--filter=blob:none', `https://github.com/${repo}`, src], { stdio: 'inherit' })
    execFileSync('git', ['checkout', '-q', sha], { cwd: src })
    let added = 0, updated = 0
    const kept = []
    for (const [dir, category] of Object.entries(dirs)) {
      for (const name of readdirSync(join(src, dir)).sort()) {
        if (exclude?.[name]) { console.log(`skip ${repo} ${dir}/${name}: ${exclude[name]}`); continue }
        const path = `${dir}/${name}`
        const from = join(src, path)
        if (!existsSync(join(from, 'SKILL.md'))) continue
        if (seen.has(name)) { console.warn(`skip ${repo} ${path}: name taken by an earlier source`); continue }
        seen.add(name)
        const md = readFileSync(join(from, 'SKILL.md'), 'utf8')
        if (!md.startsWith('---')) { console.warn('skip (no frontmatter):', repo, path); continue }
        const to = join(skillsOut, name)
        const exists = existsSync(join(to, 'SKILL.md'))
        if (exists && !replaceable(readFileSync(join(to, 'SKILL.md'), 'utf8'), md, repo)) { kept.push(name); continue }
        exists ? updated++ : added++
        if (dryRun) continue
        rmSync(to, { recursive: true, force: true })
        cpSync(from, to, { recursive: true, dereference: true })
        const source = `https://github.com/${repo}/blob/${sha}/${path}/SKILL.md`
        writeFileSync(join(to, 'SKILL.md'), withMeta(md, { category, tags: [tag], source }))
      }
    }
    console.log(`${repo}@${sha.slice(0, 7)}: ${added} new, ${updated} updated` +
      (kept.length ? `, kept (other source): ${kept.join(' ')}` : '') + (dryRun ? ' (dry run)' : ''))
  } finally {
    rmSync(src, { recursive: true, force: true })
  }
}

function frontmatter(md) {
  return md.match(/^---\s*\n([\s\S]*?)\n---/)?.[1] ?? ''
}

function field(md, key) {
  return (frontmatter(md).match(new RegExp(`^${key}:\\s*(.*)$`, 'm'))?.[1] ?? '').replace(/^["']|["']$/g, '').trim()
}

// replaceable: the skill there is this repo's, an unpinned copy (source
// "community"), or has no source but the same start of description.
function replaceable(have, upstream, repo) {
  const source = field(have, 'source')
  if (source.includes(repo) || source === 'community') return true
  if (source) return false
  const norm = s => s.toLowerCase().replace(/[^a-z0-9]+/g, ' ').trim().slice(0, 40)
  return norm(field(have, 'description')) === norm(field(upstream, 'description'))
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
