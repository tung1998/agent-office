#!/usr/bin/env node
// Lint of the project wiki (skill tri-thuc): no tokens, only git and files.
// Run in the repo root: node .claude/skills/tri-thuc/lint.mjs [--init] [--dir docs/wiki]
// Prints the problems, one a line, and exits 1 when there is one; "~" lines
// are reminders (the wiki is behind the code) and do not fail.
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'

const args = process.argv.slice(2)
const dirArg = args.indexOf('--dir')
const wiki = dirArg >= 0 ? args[dirArg + 1] : 'docs/wiki'
const today = new Date().toISOString().slice(0, 10)

const skeleton = {
  'index.md': `# Wiki

Mục lục: mỗi trang một dòng. Đọc trang này, \`trang-thai.md\` và các quyết định liên quan trước khi quyết (skill \`tri-thuc\`).

- [Trạng thái](trang-thai.md): mốc đang làm, đã xong (commit đã gộp), hỏng
- [Quyết định](quyet-dinh.md): quyết định nào thay quyết định nào, vì sao
- [Nhật ký](log.md): dòng thời gian của wiki

## Hiểu biết
<!-- - [Chủ đề](hieu-biet/chu-de.md): một câu -->
`,
  'trang-thai.md': `# Trạng thái

## Đang làm
<!-- - <mốc>: <ai làm>, <còn thiếu gì>; code ở worktree/nhánh nào -->

## Đã xong
<!-- - <mốc>: <kết quả dùng thật> — commit abc1234, soát: <ai, PASS> -->

## Hỏng
<!-- - <mốc>: <vì sao>, <việc sửa> -->
`,
  'quyet-dinh.md': `# Quyết định

Mới nhất trên cùng. Mỗi mục: Lời gốc, Quyết, Vì sao, Thay, Bỏ, Soát.

<!--
### YYYY-MM-DD <tiêu đề>
- Lời gốc: "<nguyên văn>"
- Quyết: …
- Vì sao: … (nguồn)
- Thay: <tiêu đề quyết định cũ>
- Bỏ: <hướng, việc không làm nữa>
- Soát: <ai, kết luận>
-->
`,
  'log.md': `# Nhật ký

## [${today}] init | Tạo wiki
`,
}

function git(...a) {
  return gitIn('.', ...a)
}

// gitIn runs git in repo (a unit's repo, for a milestone done there).
function gitIn(repo, ...a) {
  try {
    return execFileSync('git', ['-C', repo, ...a], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
  } catch {
    return null
  }
}

if (args.includes('--init')) {
  mkdirSync(join(wiki, 'hieu-biet'), { recursive: true })
  for (const [name, body] of Object.entries(skeleton)) {
    const p = join(wiki, name)
    if (!existsSync(p)) {
      writeFileSync(p, body)
      console.log(`created ${p}`)
    }
  }
  process.exit(0)
}

if (!existsSync(wiki)) {
  console.log(`- [wiki] chưa có ${wiki}: chạy với --init rồi điền trạng thái, quyết định hiện tại`)
  process.exit(1)
}

const problems = []
const notes = []
const say = (kind, msg) => problems.push(`- [${kind}] ${msg}`)

function pages(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name)
    if (e.isDirectory()) return pages(p)
    return e.name.endsWith('.md') ? [p] : []
  })
}
const all = pages(wiki)
const read = (p) => readFileSync(p, 'utf8').replace(/<!--[\s\S]*?-->/g, '')

// Links: every relative link points at a file that exists.
const linked = new Set()
for (const p of all) {
  for (const m of read(p).matchAll(/\]\(([^)\s]+)\)/g)) {
    const target = m[1]
    if (/^[a-z]+:/i.test(target) || target.startsWith('#') || target.startsWith('/')) continue
    const file = resolve(dirname(p), target.replace(/#.*$/, '').replace(/:\d+(-\d+)?$/, ''))
    if (p === join(wiki, 'index.md')) linked.add(file)
    if (!existsSync(file)) say('link', `${p}: ${target} không tồn tại`)
  }
}

// Orphans: every page is in the index.
for (const p of all) {
  if (p === join(wiki, 'index.md')) continue
  if (!linked.has(resolve(p))) say('index', `${p} không có trong index.md`)
}

// Done = merged: each line under "Đã xong" names a commit on HEAD, of this
// repo or of the one after "@" (a unit's: commit abc1234 @ ../theme-studio).
const statusFile = join(wiki, 'trang-thai.md')
if (existsSync(statusFile)) {
  const done = read(statusFile).split(/^## /m).find((s) => /^Đã xong/.test(s)) ?? ''
  for (const line of done.split('\n').filter((l) => /^\s*-\s+\S/.test(l))) {
    const short = line.trim().slice(0, 80)
    const commits = [...line.matchAll(/\bcommit\s+`?([0-9a-f]{7,40})`?(?:\s+@\s+`?([^\s,;`]+))?/gi)]
    if (commits.length === 0) {
      say('xong', `"${short}": không có commit làm bằng chứng`)
      continue
    }
    for (const [, h, repo = '.'] of commits) {
      const where = repo === '.' ? 'repo' : repo
      if (gitIn(repo, 'cat-file', '-e', `${h}^{commit}`) === null) say('xong', `"${short}": commit ${h} không có trong ${where}`)
      else if (gitIn(repo, 'merge-base', '--is-ancestor', h, 'HEAD') === null) say('xong', `"${short}": commit ${h} chưa gộp vào nhánh chính của ${where}`)
    }
  }
}

// Decisions: each has its original words and its why; what it replaces exists.
const decisionsFile = join(wiki, 'quyet-dinh.md')
if (existsSync(decisionsFile)) {
  const entries = read(decisionsFile)
    .split(/^### /m)
    .slice(1)
    .map((s) => ({ title: s.split('\n')[0].trim(), body: s }))
  const field = (body, name) => body.match(new RegExp(`^\\s*-?\\s*\\**${name}\\**:\\s*(.+)$`, 'mi'))?.[1].trim()
  for (const e of entries) {
    if (!/^\d{4}-\d{2}-\d{2}\s/.test(e.title)) say('quyet-dinh', `"${e.title}": tiêu đề phải bắt đầu bằng ngày YYYY-MM-DD`)
    for (const f of ['Lời gốc', 'Quyết', 'Vì sao']) {
      if (!field(e.body, f)) say('quyet-dinh', `"${e.title}": thiếu ${f}`)
    }
    const replaced = field(e.body, 'Thay')
    if (replaced && !/^(không|-)$/i.test(replaced)) {
      const old = entries.find((o) => o !== e && o.title.includes(replaced.replace(/^"|"$/g, '')))
      if (!old) say('quyet-dinh', `"${e.title}": Thay "${replaced}" không khớp quyết định nào`)
      else if (!field(old.body, 'Bị thay bởi')) say('quyet-dinh', `"${old.title}": đã bị thay bởi "${e.title}" mà chưa ghi Bị thay bởi`)
    }
  }
}

// Log: entries are "## [YYYY-MM-DD] kind | title".
const logFile = join(wiki, 'log.md')
if (existsSync(logFile)) {
  for (const l of read(logFile).split('\n').filter((l) => l.startsWith('## '))) {
    if (!/^## \[\d{4}-\d{2}-\d{2}\] \S+ \| \S/.test(l)) say('log', `"${l}": phải là ## [YYYY-MM-DD] loại | tiêu đề`)
  }
}

// Behind the code: commits since the wiki last changed.
const last = git('log', '-1', '--format=%H', '--', wiki)
if (last) {
  const behind = git('log', '--format=%h %s', `${last}..HEAD`, '--', '.', `:!${relative('.', wiki)}`)
  if (behind) {
    const lines = behind.split('\n')
    notes.push(`~ [cu] wiki chưa cập nhật từ ${lines.length} commit: ${lines.slice(0, 5).join('; ')}`)
  }
}

for (const l of [...problems, ...notes]) console.log(l)
if (problems.length === 0) console.log(`OK: ${all.length} trang`)
process.exit(problems.length ? 1 : 0)
