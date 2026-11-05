#!/usr/bin/env node
// Imports the skills of "7 skill và công cụ cho Claude" (https://nhatanh.ai/skill-cho-claude/)
// into the office's machine library (<office>/library/skills), not into the repo.
// The page points at six repos; every published skill of each is taken, whole
// folder (references/, scripts/), SKILL.md marked `source:`, `category` and
// `tags: [<repo tag>]`:
//   - nateherkai/scroll-craft (MIT): scroll-craft
//   - heygen-com/hyperframes (Apache-2.0): skills/* (HTML → video); not the
//     repo's own dev skills (.claude, .agents) nor its registry blocks
//   - mvanhorn/last30days-skill (MIT): last30days
//   - coreyhaines31/marketingskills (MIT): skills/*
//   - mattpocock/skills (MIT): engineering, productivity, misc; not in-progress
//   - vercel-labs/skills (MIT): find-skills
// The page's MCPs (fal, Apify) and tools (Playwright CLI, Motion Studio) are not
// skills and are left out. A skill of the same name already there is replaced
// when it comes from this repo or is an unpinned copy (`source: community`, as
// antigravity-awesome-skills gathered them); a skill with no source (the
// person's own) only when its description is the same; one of another source
// is left alone.
//
//   node scripts/sync-nhatanh-library.mjs [--dir=<office>/library/skills] [--dry-run]
// To move a repo to a newer commit, change its sha below.
import { cpSync, existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const dryRun = process.argv.includes('--dry-run')

// dirs: folders whose subfolders are skills; category: one the office library knows (SkillArea)
const SOURCES = [
  { repo: 'nateherkai/scroll-craft', sha: '75d81f74e83692add18cd7a8a8e078b8a887a579', tag: 'scroll-craft',
    dirs: { 'plugins/nateherk-design/skills': 'design' } },
  { repo: 'heygen-com/hyperframes', sha: '33c17df5aeb7bc45f717e7091e81ea0e0ebe6e23', tag: 'hyperframes',
    dirs: { skills: 'video' } },
  { repo: 'mvanhorn/last30days-skill', sha: 'e9c31b868ab9d284c1a988e528c3391c51ebf944', tag: 'last30days',
    dirs: { skills: 'research' } },
  { repo: 'coreyhaines31/marketingskills', sha: '88c79f6528a9347420e57ceb1fff9a60de651768', tag: 'marketingskills',
    dirs: { skills: 'marketing' } },
  { repo: 'mattpocock/skills', sha: '4ae2317ddb22b046e26455e4f9c9b3aaaf62212d', tag: 'mattpocock',
    dirs: { 'skills/engineering': 'development', 'skills/productivity': 'productivity', 'skills/misc': 'development' } },
  { repo: 'vercel-labs/skills', sha: 'e80e8255bb89ce3288edc34d6cead20cbb7cc521', tag: 'vercel-skills',
    dirs: { skills: 'skill-authoring' } }
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
for (const { repo, sha, tag, dirs } of SOURCES) {
  const src = mkdtempSync(join(tmpdir(), 'nhatanh-'))
  try {
    execFileSync('git', ['clone', '-q', '--filter=blob:none', `https://github.com/${repo}`, src], { stdio: 'inherit' })
    execFileSync('git', ['checkout', '-q', sha], { cwd: src })
    let added = 0, updated = 0
    const kept = []
    for (const [dir, category] of Object.entries(dirs)) {
      for (const name of readdirSync(join(src, dir)).sort()) {
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
