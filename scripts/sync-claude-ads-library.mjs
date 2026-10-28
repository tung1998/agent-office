#!/usr/bin/env node
// Imports the paid-media skills of Claude Ads (AgriciDaniel/claude-ads, MIT)
// into the office's machine library (<office>/library/skills), not into the
// repo: one skill per platform or workflow (ads-google, ads-meta, ads-audit…),
// read-only by default, each finding with its evidence. The skills read
// `ads/references/*.md` from the plugin root; each skill gets the references it
// names in its own `references/` and the paths are rewritten, and the steps
// that read the `ads` conductor point at its thinking framework instead, so it
// works installed alone. Each SKILL.md is marked `source:`, `category: marketing` and
// `tags: [ads]`. A skill of the same name already there that is not from this
// repo is left alone.
// Not imported: the `ads` conductor and the `agents/` workers (they need the
// repo's Python core and JSON schemas), ads-research (maintains the repo
// itself), ads-landing (needs the repo's guarded fetcher), the Cockpit mod.
//
//   node scripts/sync-claude-ads-library.mjs                  # the pinned commit, the office of cwd
//   node scripts/sync-claude-ads-library.mjs --sha=<commit> --dir=<office>/library/skills   # (then pin it here)
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

const REPO = 'AgriciDaniel/claude-ads'
const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const SHA = arg('sha') ?? 'fc3908e786c318776df89a3a098333da377364be'
const SKIP = new Set(['ads-research', 'ads-landing'])
const ROOT_NOTE = / \(resolve it from the plugin root[^)]*\)/g

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

const src = mkdtempSync(join(tmpdir(), 'claude-ads-'))
try {
  execFileSync('git', ['clone', '-q', '--filter=blob:none', `https://github.com/${REPO}`, src], { stdio: 'inherit' })
  execFileSync('git', ['checkout', '-q', SHA], { cwd: src })
  const refs = join(src, 'ads', 'references')

  let skills = 0, kept = 0
  for (const name of readdirSync(join(src, 'skills')).sort()) {
    const from = join(src, 'skills', name)
    if (SKIP.has(name) || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(name) || !existsSync(join(from, 'SKILL.md'))) continue
    const to = join(skillsOut, name)
    if (existsSync(join(to, 'SKILL.md'))) {
      const source = frontmatter(readFileSync(join(to, 'SKILL.md'), 'utf8')).match(/^source:\s*(.*)$/m)?.[1] ?? ''
      if (!source.includes(REPO)) { kept++; continue }
    }
    const md = standalone(readFileSync(join(from, 'SKILL.md'), 'utf8'))
    if (!md.startsWith('---')) { console.warn('skip (no frontmatter):', name); continue }
    if (md.includes('main `ads`')) console.warn(`${name}: still points at the ads conductor`)
    rmSync(to, { recursive: true, force: true })
    cpSync(from, to, { recursive: true, dereference: true })
    const named = [...new Set([...md.matchAll(/ads\/references\/([A-Za-z0-9_.-]+\.md)/g)].map(m => m[1]))]
    for (const file of named) {
      if (!existsSync(join(refs, file))) { console.warn(`${name}: missing ads/references/${file}`); continue }
      mkdirSync(join(to, 'references'), { recursive: true })
      cpSync(join(refs, file), join(to, 'references', file))
    }
    const body = md.replace(ROOT_NOTE, '').replace(/ads\/references\//g, 'references/')
    writeFileSync(join(to, 'SKILL.md'), withMeta(body, {
      category: 'marketing', tags: ['ads'], source: blob(`skills/${name}/SKILL.md`)
    }))
    skills++
  }
  console.log(`${SHA.slice(0, 7)}: ${skills} skills → ${skillsOut}` +
    (kept ? `, ${kept} of the same name kept (not from ${REPO})` : ''))
} finally {
  rmSync(src, { recursive: true, force: true })
}

// standalone drops the steps that read the `ads` conductor, which is not
// imported; its thinking framework comes along as a reference.
function standalone(md) {
  return md
    .replace('Read the main `ads` operating contract and thinking framework.', 'Read `ads/references/thinking-framework.md`.')
    .replace('Read the main `ads` contract and ', 'Read the ')
    .replace(/Read the main `ads` contract\. ?/, '')
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
