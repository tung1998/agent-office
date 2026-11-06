#!/usr/bin/env node
// Imports the skills of "10 Claude Code Skills được yêu thích nhất" into the
// office's machine library (<office>/library/skills), not into the repo.
// Checked against each repo's LICENSE before import:
//   - obra/superpowers (MIT): skills/* — process skills (brainstorming, TDD,
//     systematic-debugging, …)
//   - multica-ai/andrej-karpathy-skills (MIT, declared in README only, no
//     LICENSE file in the repo): skills/karpathy-guidelines
//   - anthropics/skills: only the Apache-2.0 skills (each one's own
//     LICENSE.txt); not imported: skills/doc-coauthoring (no license field,
//     no LICENSE.txt). docx/pdf/pptx/xlsx were Proprietary (source-available
//     only) at the first pin (ca45fc4); upstream moved them to Apache-2.0 on
//     2026-11-06 (anthropics/skills#2146) and the pin was bumped to 2c5958e
//     to pick them up.
//   - nextlevelbuilder/ui-ux-pro-max-skill (MIT): .claude/skills/*
//   - addyosmani/agent-skills (MIT): skills/*
//   - Leonxlnx/taste-skill (MIT): skills/taste-skill
// mattpocock/skills is already in scripts/sync-nhatanh-library.mjs at the
// same commit as upstream HEAD — no sha bump needed.
//
// "Stack của tôi với Claude" (ADR: quest nối tiếp bài "10 Claude Code Skills"):
//   - garrytan/gstack (MIT): các thư mục bậc 1 có SKILL.md trực tiếp (router
//     ở SKILL.md gốc và các skill lồng sâu hơn — browser-skills/, capy/,
//     openclaw/, qm/, test/fixtures/ — không nhập, chỉ bậc 1).
//   - anthropics/financial-services (Apache-2.0): plugin dạng
//     <plugin>/skills/<skill>/SKILL.md, nhập qua pluginDirs (3 nhóm:
//     vertical-plugins, agent-plugins, partner-built). Chỉ lấy skills/, bỏ
//     agents/, references/ của mỗi plugin.
//   - anthropics/claude-for-legal (Apache-2.0): cùng dạng plugin
//     <practice-area>/skills/<skill>/SKILL.md ở gốc repo. Không nhập
//     external_plugins/ (plugin bên thứ ba cocounsel-legal, không theo mẫu
//     <area>/skills/), managed-agent-cookbooks/, references/ (không phải
//     skill plugin).
//   - social-media-skills/skills (MIT): skills/* — thẳng mẫu dirs sẵn có.
//   - AgriciDaniel/claude-seo (MIT): skills/* — thẳng mẫu dirs sẵn có.
//   - blader/humanizer (MIT): một skill ở gốc repo (SKILL.md ngay root),
//     nhập qua root; không nhập agents/ (subagent dò lại, là agent chứ
//     không phải skill).
// Không nhập, tìm không ra repo đúng hoặc cấu trúc không phải "kho skill"
// sạch (ghi lại ở docs/research/references.md):
//   - "claude-skills" (263+ skill đa nền tảng): ứng viên gần nhất,
//     alirezarezvani/claude-skills, lồng 1-4 cấp không nhất quán trong cùng
//     một thư mục (vd engineering/caveman/skills/caveman/SKILL.md lẫn
//     engineering/skills/chaos-engineering/SKILL.md), trùng lặp nhiều với
//     nguồn đã có (caveman đã cố tình không nhập, karpathy-coder/write-a-skill
//     trùng andrej-karpathy-skills/skill-creator) — không khớp mẫu dirs/
//     pluginDirs/root hiện có, bỏ.
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
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { homedir, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'

const arg = name => process.argv.find(a => a.startsWith(`--${name}=`))?.slice(name.length + 3)
const dryRun = process.argv.includes('--dry-run')

// dirs: folders whose subfolders are skills; category: one the office library knows (SkillArea)
// pluginDirs: folders whose subfolders are plugins, each plugin's own
//   skills/ subfolder holding the actual skills (for plugin suites that mix
//   skills with agents/commands/hooks at the plugin's own top level)
// root: { name, category } when the repo itself is a single skill (SKILL.md at repo root)
// exclude: skill folder names under that dir to skip, with a reason logged
const SOURCES = [
  { repo: 'obra/superpowers', sha: '22f0b463e7a5e579904dd6211a520321035526f7', tag: 'superpowers',
    dirs: { skills: 'process' } },
  { repo: 'multica-ai/andrej-karpathy-skills', sha: '2c606141936f1eeef17fa3043a72095b4765b9c2', tag: 'karpathy-skills',
    dirs: { skills: 'development' } },
  { repo: 'anthropics/skills', sha: '2c5958ecac7cee2b583967a2c4981d0f4a75c579', tag: 'anthropic-skills',
    dirs: { skills: 'development' },
    exclude: {
      'doc-coauthoring': 'no license field, no LICENSE.txt'
    } },
  { repo: 'nextlevelbuilder/ui-ux-pro-max-skill', sha: 'b7d5353e0130feb8c5574ef1251f8a1db1873ccb', tag: 'ui-ux-pro-max',
    dirs: { '.claude/skills': 'design' } },
  { repo: 'addyosmani/agent-skills', sha: '301b02b12bff98e4688e6984af6556802322bbde', tag: 'agent-skills',
    dirs: { skills: 'development' } },
  { repo: 'Leonxlnx/taste-skill', sha: '9b4bf937a1f69348126959c15d2f99a73e65699b', tag: 'taste-skill',
    dirs: { skills: 'design' } },
  { repo: 'garrytan/gstack', sha: 'e325b6d160179488f848f3d1f86c0df985baad87', tag: 'gstack',
    dirs: { '.': 'development' } },
  { repo: 'anthropics/financial-services', sha: '60565064557ca77b12f62aa7877f365d111c129d', tag: 'financial-services',
    pluginDirs: { 'plugins/vertical-plugins': 'finance', 'plugins/agent-plugins': 'finance', 'plugins/partner-built': 'finance' } },
  { repo: 'anthropics/claude-for-legal', sha: '4a6c651889c97cc9140580363c73e0eb17379c2b', tag: 'claude-for-legal',
    pluginDirs: { '.': 'legal' } },
  { repo: 'social-media-skills/skills', sha: 'f28cef2b7067ec64e79c9d85421c022c06659e03', tag: 'social-media-skills',
    dirs: { skills: 'content' } },
  { repo: 'AgriciDaniel/claude-seo', sha: '6be93f45064ffbadced572b5d5c95f55fd263af0', tag: 'claude-seo',
    dirs: { skills: 'content' } },
  { repo: 'blader/humanizer', sha: '0ef9fe5bc3572734fd3ae6c448fe00a30cd5dcf0', tag: 'humanizer',
    root: { name: 'humanizer', category: 'content' } }
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
for (const { repo, sha, tag, dirs, pluginDirs, root, exclude } of SOURCES) {
  const src = mkdtempSync(join(tmpdir(), 'top-skills-'))
  try {
    execFileSync('git', ['clone', '-q', '--filter=blob:none', `https://github.com/${repo}`, src], { stdio: 'inherit' })
    execFileSync('git', ['checkout', '-q', sha], { cwd: src })
    const counts = { added: 0, updated: 0 }
    const kept = []
    const doImport = (name, path, category) =>
      importOne({ repo, sha, tag, src, name, path, category, exclude, kept, counts })

    for (const [dir, category] of Object.entries(dirs ?? {})) {
      for (const name of readdirSync(join(src, dir)).sort()) {
        doImport(name, dir === '.' ? name : `${dir}/${name}`, category)
      }
    }
    // pluginDirs: dir's children are plugins; only each plugin's own skills/
    // subfolder is imported (the rest — agents/, hooks/, references/ — stays out)
    for (const [dir, category] of Object.entries(pluginDirs ?? {})) {
      for (const plugin of readdirSync(join(src, dir)).sort()) {
        const pluginPath = dir === '.' ? plugin : `${dir}/${plugin}`
        const skillsDir = join(src, pluginPath, 'skills')
        if (!existsSync(skillsDir)) continue
        for (const name of readdirSync(skillsDir).sort()) {
          doImport(name, `${pluginPath}/skills/${name}`, category)
        }
      }
    }
    if (root && existsSync(join(src, 'SKILL.md'))) {
      importOne({ repo, sha, tag, src, name: root.name, path: '.', category: root.category, exclude, kept, counts, rootFile: true })
    }

    console.log(`${repo}@${sha.slice(0, 7)}: ${counts.added} new, ${counts.updated} updated` +
      (kept.length ? `, kept (other source): ${kept.join(' ')}` : '') + (dryRun ? ' (dry run)' : ''))
  } finally {
    rmSync(src, { recursive: true, force: true })
  }
}

// importOne brings one skill (a folder with SKILL.md, or — with rootFile —
// the repo root itself) into skillsOut, applying the exclude/dedupe/replaceable rules.
function importOne({ repo, sha, tag, src, name, path, category, exclude, kept, counts, rootFile }) {
  if (exclude?.[name]) { console.log(`skip ${repo} ${path}: ${exclude[name]}`); return }
  const from = rootFile ? src : join(src, path)
  const mdPath = join(from, 'SKILL.md')
  if (!existsSync(mdPath)) return
  if (seen.has(name)) { console.warn(`skip ${repo} ${path}: name taken by an earlier source`); return }
  seen.add(name)
  const md = readFileSync(mdPath, 'utf8')
  if (!md.startsWith('---')) { console.warn('skip (no frontmatter):', repo, path); return }
  const to = join(skillsOut, name)
  const exists = existsSync(join(to, 'SKILL.md'))
  if (exists && !replaceable(readFileSync(join(to, 'SKILL.md'), 'utf8'), md, repo)) { kept.push(name); return }
  exists ? counts.updated++ : counts.added++
  if (dryRun) return
  rmSync(to, { recursive: true, force: true })
  if (rootFile) { mkdirSync(to, { recursive: true }) } else { cpSync(from, to, { recursive: true, dereference: true }) }
  const source = `https://github.com/${repo}/blob/${sha}/${rootFile ? '' : path + '/'}SKILL.md`
  writeFileSync(join(to, 'SKILL.md'), withMeta(md, { category, tags: [tag], source }))
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
