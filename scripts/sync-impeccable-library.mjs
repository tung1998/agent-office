#!/usr/bin/env node
// Imports Impeccable (pbakaus/impeccable, Apache 2.0) into the office's machine
// library (<office>/library/skills), not into the repo: the one `impeccable`
// skill as the repo builds it for Claude Code (.claude/skills/impeccable), with
// its 24 commands in `reference/` (critique, audit, polish, bolder, harden…)
// and the `scripts/impeccable` launcher. The launcher runs the slop detector
// binary, downloaded once into ~/.impeccable/bin and checked against the
// pinned engine-checksums.txt. LICENSE and NOTICE.md come along. SKILL.md is
// marked `source:`, `category: design` and `tags: [design, frontend, ui]`. A
// skill of the same name already there that is not from this repo is left alone.
// Not imported: the four subagents (the skill falls back to its
// `reference/degraded/` passes without them), the design hook, the CLI, the
// Chrome and VS Code extensions.
//
//   node scripts/sync-impeccable-library.mjs                  # the pinned commit, the office of cwd
//   node scripts/sync-impeccable-library.mjs --sha=<commit> --dir=<office>/library/skills   # (then pin it here)
import { cpSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

const REPO = 'pbakaus/impeccable'
const NAME = 'impeccable'
const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const SHA = arg('sha') ?? '5e745ace7e151feb2a93d360d0281fe39448b857'

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

const src = mkdtempSync(join(tmpdir(), 'impeccable-'))
try {
  execFileSync('git', ['clone', '-q', '--filter=blob:none', `https://github.com/${REPO}`, src], { stdio: 'inherit' })
  execFileSync('git', ['checkout', '-q', SHA], { cwd: src })
  const from = join(src, '.claude', 'skills', NAME)
  const to = join(skillsOut, NAME)
  if (existsSync(join(to, 'SKILL.md'))) {
    const source = frontmatter(readFileSync(join(to, 'SKILL.md'), 'utf8')).match(/^source:\s*(.*)$/m)?.[1] ?? ''
    if (!source.includes(REPO)) {
      console.log(`${NAME} kept: the one in ${skillsOut} is not from ${REPO}`)
      process.exit(0)
    }
  }
  const md = readFileSync(join(from, 'SKILL.md'), 'utf8')
  if (!md.startsWith('---')) throw new Error('SKILL.md has no frontmatter')
  rmSync(to, { recursive: true, force: true })
  cpSync(from, to, { recursive: true, dereference: true })
  for (const file of ['LICENSE', 'NOTICE.md']) cpSync(join(src, file), join(to, file))
  writeFileSync(join(to, 'SKILL.md'), withMeta(md, {
    category: 'design', tags: ['design', 'frontend', 'ui'], source: blob(`.claude/skills/${NAME}/SKILL.md`)
  }))
  console.log(`${SHA.slice(0, 7)}: ${NAME} → ${to}`)
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
