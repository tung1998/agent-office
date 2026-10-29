#!/usr/bin/env node
// Builds a throwaway demo office (its own data folder and ports, never your
// real one), fills it with sample data, and screenshots every screen of the
// dashboard into docs/guide/images/ for the guide (docs/guide/index.html).
//
//   make build && make ui-build      # bin/office and dashboard/.output
//   node docs/guide/capture.mjs
//   node docs/guide/capture.mjs --mobile [--lang=vi] [--out=dir]   # phone shots for a UX review
//   node docs/guide/capture.mjs --mobile --reuse --only=13-burn,26-jobs     # some of the screens
//   (04a-new-project-idea opens the "New project" modal with the "idea" scope card selected)
//   node docs/guide/capture.mjs --mobile --reuse --path=/costs --path='{project}/burn/reviews'   # any route, one by one
//
// Env: OFFICE_BIN, OFFICE_UI_DIR, CHROME (paths), KEEP=1 (leave it running).
import { spawn, execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, rmSync, writeFileSync, readdirSync, appendFileSync, readFileSync } from 'node:fs'
import { join, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { homedir, tmpdir, hostname } from 'node:os'

const here = dirname(fileURLToPath(import.meta.url))
const worktreeRoot = resolve(here, '..', '..')
// a git worktree has no build of its own: use the main checkout's
const mainRoot = (() => {
  try {
    const common = execFileSync('git', ['rev-parse', '--path-format=absolute', '--git-common-dir'], { cwd: worktreeRoot }).toString().trim()
    return dirname(common)
  } catch { return worktreeRoot }
})()
const pick = (...c) => c.find(p => p && existsSync(p))
const BIN = pick(process.env.OFFICE_BIN, join(worktreeRoot, 'bin/office'), join(mainRoot, 'bin/office'))
const UI_ENTRY = [process.env.OFFICE_UI_DIR && join(process.env.OFFICE_UI_DIR, '.output/server/index.mjs'),
  join(worktreeRoot, 'dashboard/.output/server/index.mjs'), join(mainRoot, 'dashboard/.output/server/index.mjs')].find(p => p && existsSync(p))
const CHROME = pick(process.env.CHROME, ...chromeCandidates())
if (!BIN || !UI_ENTRY || !CHROME) {
  console.error('Need bin/office (make build), dashboard/.output (make ui-build) and Chrome/Chromium.', { BIN, UI_ENTRY, CHROME })
  process.exit(1)
}

const HOME = join(tmpdir(), 'agent-office-demo')
const API = '127.0.0.1:18787'
const UI = 'http://127.0.0.1:12704'
// --lang=vi: Vietnamese dashboard and sample data, shots in images/vi/ (for vi.html)
const LANG = (process.argv.find(a => a.startsWith('--lang=')) ?? '--lang=en').slice(7) === 'vi' ? 'vi' : 'en'
const T = (en, vi) => (LANG === 'vi' ? vi : en)
// --mobile: every screen at phone size (390×844), the whole page, into a folder
// of its own (--out=dir, default the temp dir's agent-office-mobile/), never the
// guide's images: for a UX review (burn/hunt-ux.md), not for the guide
const MOBILE = process.argv.includes('--mobile')
const outArg = (process.argv.find(a => a.startsWith('--out=')) ?? '').slice(6)
const OUT = outArg ? resolve(outArg) : MOBILE ? join(tmpdir(), 'agent-office-mobile', LANG)
  : LANG === 'vi' ? join(here, 'images', 'vi') : join(here, 'images')
const CONTENT = T('Content & Planning', 'Kế hoạch & Nội dung')
// --only-missing (or ONLY_MISSING=1) keeps the shots already there
// --retake=08-permissions,19-bot shoots those again too
const onlyMissing = process.argv.includes('--only-missing') || !!process.env.ONLY_MISSING
const retake = (process.argv.find(a => a.startsWith('--retake=')) ?? '').slice(9).split(',').filter(Boolean)
// --only=13-burn,26-jobs shoots just those screens; --path=<route> (again for
// more) shoots just those routes, {project} standing for the demo project's
// /projects/<id>: a review going screen by screen (with --reuse, seconds each)
const only = (process.argv.find(a => a.startsWith('--only=')) ?? '').slice(7).split(',').filter(Boolean)
const paths = process.argv.filter(a => a.startsWith('--path=')).map(a => a.slice(7)).filter(Boolean)
const EMAIL = 'demo@agent-office.dev'
const PASSWORD = 'demo-office-2026!'
const children = []
const sleep = ms => new Promise(r => setTimeout(r, ms))
// also into a file next to the demo office: a run that gets killed still leaves its trail
const log = (...a) => {
  console.log('›', ...a)
  try { appendFileSync(join(tmpdir(), 'agent-office-demo.log'), `${new Date().toISOString()} ${a.join(' ')}\n`) } catch {}
}

function chromeCandidates() {
  const pw = join(homedir(), 'Library/Caches/ms-playwright')
  const out = []
  if (existsSync(pw)) {
    for (const d of readdirSync(pw).filter(d => d.startsWith('chromium-')).sort().reverse()) {
      out.push(join(pw, d, 'chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing'))
      out.push(join(pw, d, 'chrome-mac/Chromium.app/Contents/MacOS/Chromium'))
      out.push(join(pw, d, 'chrome-linux/chrome'))
    }
  }
  out.push('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/chromium', '/usr/bin/google-chrome')
  return out
}

function office(args, input) {
  return execFileSync(BIN, ['--home', HOME, ...args], { input, cwd: HOME, stdio: ['pipe', 'pipe', 'pipe'] }).toString()
}
function start(name, cmd, args, opts = {}) {
  const c = spawn(cmd, args, { ...opts, stdio: ['ignore', 'pipe', 'pipe'], detached: true })
  c.stderr.on('data', d => process.env.VERBOSE && process.stderr.write(`[${name}] ${d}`))
  c.stdout.on('data', d => process.env.VERBOSE && process.stdout.write(`[${name}] ${d}`))
  children.push(c)
  return c
}
function stopAll() {
  for (const c of children.reverse()) {
    try { process.kill(-c.pid, 'SIGTERM') } catch {}
  }
}
process.on('exit', stopAll)
process.on('SIGINT', () => process.exit(130))

// ---- the API, logged in as the demo admin ----
let cookie = ''
async function api(method, path, body) {
  const res = await fetch(`http://${API}${path}`, {
    method,
    headers: { cookie, ...(body ? { 'content-type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined
  })
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text.slice(0, 300)}`)
  return text ? JSON.parse(text) : {}
}
const soft = async (what, fn) => { try { return await fn() } catch (e) { log(`(skip) ${what}: ${e.message}`) } }

function sql(statements) {
  const db = join(HOME, 'office.db')
  for (const s of statements) {
    try { execFileSync('sqlite3', [db, s], { stdio: ['ignore', 'pipe', 'pipe'] }) } catch (e) { log('(skip) sql:', String(e.stderr || e.message).trim().slice(0, 200)) }
  }
}
const q = s => `'${String(s).replace(/'/g, "''")}'`
const ts = minutesAgo => new Date(Date.now() - minutesAgo * 60000).toISOString().replace(/\.\d+Z$/, '.000000000Z')

// ---- 1. a fresh demo office ----
function killPorts(ports) {
  for (const port of ports) {
    try {
      for (const pid of execFileSync('lsof', ['-ti', `tcp:${port}`, '-sTCP:LISTEN']).toString().split(/\s+/).filter(Boolean)) process.kill(Number(pid), 'SIGTERM')
    } catch {}
  }
}
async function login() {
  const res = await fetch(`http://${API}/api/auth/login`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ email: EMAIL, password: PASSWORD }) })
  if (!res.ok) throw new Error('login: ' + res.status + ' ' + await res.text())
  cookie = res.headers.getSetCookie().map(c => c.split(';')[0]).join('; ')
}
async function build() {
  // leftovers of a run that was killed: demo server, dashboard, docs server, browser
  killPorts([18787, 12704, 18900, 9339])
  await sleep(1000)
  // go's module cache (an agent ran go under this HOME) is read-only: rmSync fails with EACCES
  if (existsSync(HOME)) execFileSync('chmod', ['-R', 'u+w', HOME])
  rmSync(HOME, { recursive: true, force: true, maxRetries: 5, retryDelay: 300 })
  mkdirSync(HOME, { recursive: true })
  log('demo office in', HOME)
  office(['user', 'create', '--email', EMAIL, '--name', 'Alex (demo)', '--role', 'admin', '--password-stdin'], PASSWORD + '\n')
  try { office(['provider', 'add', '--kind', 'claude_cli', '--name', 'Claude Code']) } catch (e) { log('(skip) provider:', String(e.stderr).trim()) }

  // a small second repo so the projects list has variety
  const shop = join(HOME, 'demo-shop')
  mkdirSync(join(shop, 'src'), { recursive: true })
  writeFileSync(join(shop, 'package.json'), JSON.stringify({ name: 'demo-shop', private: true, scripts: { dev: 'vite', build: 'vite build', test: 'vitest run' } }, null, 2))
  writeFileSync(join(shop, 'README.md'), '# demo-shop\n\nA tiny storefront used to demo agent-office.\n')
  writeFileSync(join(shop, 'src/cart.ts'), 'export function total(items: { price: number, qty: number }[]) {\n  return items.reduce((s, i) => s + i.price * i.qty, 0)\n}\n')
  execFileSync('git', ['init', '-q', '-b', 'main'], { cwd: shop })
  execFileSync('git', ['-c', 'user.email=demo@example.com', '-c', 'user.name=demo', 'add', '.'], { cwd: shop })
  execFileSync('git', ['-c', 'user.email=demo@example.com', '-c', 'user.name=demo', 'commit', '-qm', 'init'], { cwd: shop })

  // a clone, not the real checkout: the library lists the git repos next to known projects
  const aoCopy = join(HOME, 'agent-office')
  execFileSync('git', ['clone', '-q', '--depth', '1', `file://${mainRoot}`, aoCopy])
  // shots show "main" whatever branch the real checkout is on
  execFileSync('git', ['checkout', '-q', '-B', 'main'], { cwd: aoCopy })
  // agents and workflows picked from the library (ADR-166)
  const lib = ids => ids.flatMap(id => ['--agent', `office/${id}`])
  office(['project', 'add', aoCopy, '--name', 'agent-office', ...lib(['team-lead', 'product-manager', 'architect', 'project-coordinator', 'qa-tester', 'backend-dev', 'frontend-dev', 'code-reviewer', 'devops']),
    '--workflow', 'handoff,council,advisor,feature,bugfix,review-pr,fix-tests'])
  office(['project', 'add', shop, '--name', 'demo-shop', ...lib(['planner', 'executor', 'auditor', 'backend-dev', 'data-analyst', 'devops']), '--workflow', 'council-3,handoff,advisor,bugfix'])
  office(['project', 'add', '--name', CONTENT])

  // the server scans ~/.claude for skills and MCP servers: give it a home of its own,
  // so the screenshots never show the real machine's
  const fakeHome = join(HOME, 'home')
  const skill = (name, description, body) => {
    mkdirSync(join(fakeHome, '.claude/skills', name), { recursive: true })
    writeFileSync(join(fakeHome, '.claude/skills', name, 'SKILL.md'), `---\nname: ${name}\ndescription: ${description}\n---\n\n${body}\n`)
  }
  skill('release-notes', T('Use when preparing a release: collect merged commits since the last tag and write grouped release notes.', 'Dùng khi chuẩn bị phát hành: gom các commit từ tag trước và viết release notes theo nhóm.'), '1. `git log <last-tag>..HEAD --oneline`\n2. Group by feat/fix/docs\n3. Write RELEASE_NOTES.md')
  skill('pr-review', T('Use when reviewing a pull request: correctness first, then tests, then naming and simplicity.', 'Dùng khi review pull request: đúng trước, rồi test, rồi đặt tên và độ gọn.'), 'Read the diff, run the tests, comment with file:line.')
  start('server', BIN, ['--home', HOME, 'serve', '--api', API, '--allowed-origin', UI, '--allowed-origin', UI.replace('127.0.0.1', 'localhost')], { cwd: HOME, env: { ...process.env, HOME: fakeHome } })
  start('dashboard', 'node', [UI_ENTRY], { cwd: dirname(dirname(dirname(UI_ENTRY))), env: { ...process.env, PORT: '12704', HOST: '127.0.0.1', NUXT_OFFICE_API_BASE: `http://${API}` } })
  for (let i = 0; i < 60; i++) {
    try { await fetch(`http://${API}/api/auth/me`); await fetch(UI + '/login'); break } catch { await sleep(500) }
  }
  await login()
}

// the demo projects and agents the screens point at
async function lookup() {
  const me = (await api('GET', '/api/auth/me')).user
  const { projects } = await api('GET', '/api/projects')
  const byName = n => projects.find(p => p.name === n)
  const ao = (await api('GET', `/api/projects/${byName('agent-office').id}`)).project
  const agents = ao.agents ?? []
  const lead = agents.find(a => a.id === ao.default_agent_id) ?? agents[0]
  const dev = agents.find(a => /dev|engineer|backend|frontend/i.test(a.key + a.name)) ?? agents[1] ?? lead
  const qa = agents.find(a => /qa|test|review/i.test(a.key + a.name)) ?? agents[2] ?? lead
  return { me, ao, shop: byName('demo-shop'), content: byName(CONTENT), lead, dev, qa }
}

// ---- 2. sample data ----
async function seed() {
  const { me, ao, shop, content, lead, dev, qa } = await lookup()

  await soft('describe', () => api('PATCH', `/api/projects/${ao.id}`, { description: T('Personal AI office: Go backend + Nuxt dashboard. Code first.', 'Văn phòng AI cá nhân: backend Go + dashboard Nuxt. Ưu tiên code.') }))
  await soft('describe', () => api('PATCH', `/api/projects/${shop.id}`, { description: T('Storefront demo, run by a council of agents (vote + veto).', 'Cửa hàng demo, do hội đồng agent điều hành (biểu quyết + phủ quyết).') }))
  await soft('describe', () => api('PATCH', `/api/projects/${content.id}`, { description: T('No folder: a helper for plans, posts and notes.', 'Không thư mục: trợ lý cho kế hoạch, bài đăng và ghi chú.') }))

  // operations: processes and monitors
  const proc = body => api('POST', `/api/projects/${ao.id}/processes`, body)
  const docs = await soft('process', () => proc({ name: 'docs: static server', command: 'python3 -m http.server 18900 --bind 127.0.0.1', cwd: 'docs', kind: 'service', autorestart: true }))
  const gitlog = await soft('process', () => proc({ name: 'git log', command: 'git log --oneline -25', kind: 'job' }))
  await soft('process', () => proc({ name: 'go vet', command: 'go vet ./...', kind: 'job' }))
  await soft('process', () => proc({ name: 'dashboard: typecheck', command: 'pnpm --dir dashboard typecheck', kind: 'job' }))
  for (const p of [docs, gitlog]) if (p?.id) await soft('start', () => api('POST', `/api/processes/${p.id}/start`))
  await sleep(2500)
  const mon = body => api('POST', `/api/projects/${ao.id}/monitors`, body)
  // one per process, turned on from its row (process and container monitors only)
  const mons = [
    docs?.id && await soft('monitor', () => mon({ type: 'process', target: docs.id, interval_s: 60, ai_enabled: true }))
  ]
  for (let i = 0; i < 4; i++) {
    for (const m of mons) if (m?.id) await soft('check', () => api('POST', `/api/monitors/${m.id}/check`))
    await sleep(300)
  }

  // automations (none of them fire while the screenshots are taken)
  const auto = body => soft('automation ' + body.name, () => api('POST', `/api/projects/${ao.id}/automations`, body))
  await auto({
    name: T('Morning brief', 'Tóm tắt buổi sáng'), source: 'schedule', action: 'script', config: { cron: '0 8 * * 1-5', timezone: 'Asia/Ho_Chi_Minh' },
    script: { lang: 'bash', body: 'git log --since=yesterday --oneline\necho "@@agent: ' + T('summarise yesterday\'s commits for the team', 'tóm tắt commit hôm qua cho cả nhóm') + '"', timeout_s: 60 },
    escalate: { when: 'signal', action: 'chat', agent_id: lead?.id, prompt: T('Write a 5-line brief from:', 'Viết bản tóm tắt 5 dòng từ:') + '\n{{output}}' }
  })
  // this one doubles as 38-automation-detail/39-automation-edit's seeded automation: must exist, so not soft()
  await api('POST', `/api/projects/${ao.id}/automations`, { name: T('Sentry alert → triage', 'Cảnh báo Sentry → phân loại'), source: 'webhook', action: 'chat', agent_id: dev?.id, prompt: T('A Sentry alert arrived:\n{{payload}}\nFind the root cause and propose a fix.', 'Có cảnh báo Sentry:\n{{payload}}\nTìm nguyên nhân và đề xuất cách sửa.'), limits: { max_runs_per_hour: 6, debounce_seconds: 120 } })
  await auto({ name: T('Weekly dependency check', 'Kiểm tra thư viện hằng tuần'), source: 'schedule', action: 'chat', agent_id: qa?.id, config: { cron: '0 9 * * 1', timezone: 'Asia/Ho_Chi_Minh' }, prompt: T('List outdated Go and npm dependencies, flag risky upgrades.', 'Liệt kê thư viện Go và npm đã cũ, đánh dấu bản nâng cấp rủi ro.') })
  // the Burn: off, turning on by itself on weekday nights until 7:00 (13-burn, 13b-burn-settings)
  await soft('burn plan', () => api('PUT', `/api/projects/${ao.id}/burn`, { focus: T('checkout first, skip docs', 'ưu tiên checkout, bỏ qua docs'), plan: { end: 'clock', clock: '07:00', auto: true, days: [1, 2, 3, 4, 5], at: '22:00' } }))

  // a bot (disabled, so it never calls Telegram for real) for 42-bot-detail/43-bot-edit — not soft(): must exist
  await api('POST', `/api/projects/${ao.id}/channels`, { kind: 'telegram', name: T('Support bot', 'Bot hỗ trợ'), token: '123:demo-bot-token', agent_id: lead?.id, allow: ['*'], enabled: false })

  // long-term memory
  for (const text of LANG === 'vi'
    ? ['Repo dùng pnpm; chạy pnpm typecheck trước khi báo xong việc sửa giao diện.', 'Làm thẳng trên main, không dùng nhánh feature.', 'Mọi chữ hiển thị trên dashboard đi qua t() (vi + en).']
    : ['Repo uses pnpm; run pnpm typecheck before saying a UI change is done.', 'Work directly on main, no feature branches.', 'Every visible string in the dashboard goes through t() (vi + en).']) {
    if (lead) await soft('memory', () => api('POST', `/api/projects/${ao.id}/agents/${lead.id}/memories`, { text }))
  }

  // chats, a pending diff, and usage history (straight into the demo DB)
  const conv = (id, agent, title, minutesAgo) => `INSERT INTO conversations (id, project_id, agent_id, agent_name, title, created_by, created_at, updated_at) VALUES (${q(id)}, ${q(ao.id)}, ${q(agent.id)}, ${q(agent.name)}, ${q(title)}, ${q(me.id)}, ${q(ts(minutesAgo + 5))}, ${q(ts(minutesAgo))})`
  const msg = (id, cid, role, author, content, minutesAgo, tools = []) => `INSERT INTO messages (id, conversation_id, role, content, tools, author, created_at) VALUES (${q(id)}, ${q(cid)}, ${q(role)}, ${q(content)}, ${q(JSON.stringify(tools))}, ${q(author)}, ${q(ts(minutesAgo))})`
  const diff = `diff --git a/dashboard/app/components/ThemeToggle.vue b/dashboard/app/components/ThemeToggle.vue
new file mode 100644
--- /dev/null
+++ b/dashboard/app/components/ThemeToggle.vue
@@ -0,0 +1,12 @@
+<script setup lang="ts">
+const { t } = useLang()
+const mode = useColorMode()
+const dark = computed(() => mode.value === 'dark')
+</script>
+
+<template>
+  <UButton
+    :icon="dark ? 'i-lucide-sun' : 'i-lucide-moon'" color="neutral" variant="ghost"
+    :aria-label="t('common.toggleTheme')" @click="mode.preference = dark ? 'light' : 'dark'"
+  />
+</template>
diff --git a/dashboard/app/layouts/default.vue b/dashboard/app/layouts/default.vue
--- a/dashboard/app/layouts/default.vue
+++ b/dashboard/app/layouts/default.vue
@@ -42,6 +42,7 @@
         <UserMenu />
+        <ThemeToggle />
       </div>`
  const user = me.name || 'Alex'
  sql([
    conv('cnv_demo_theme', dev, T('Add a dark mode toggle to the header', 'Thêm nút chuyển giao diện tối ở header'), 12),
    msg('msg_demo_t1', 'cnv_demo_theme', 'user', user, T('Add a dark/light toggle next to the user menu in the header. Keep it i18n-clean.', 'Thêm nút chuyển sáng/tối cạnh menu người dùng trên header. Nhớ đi qua i18n.'), 16),
    msg('msg_demo_t2', 'cnv_demo_theme', 'assistant', dev.name, T(
      'Done. I added a small `ThemeToggle` component and placed it next to `UserMenu` in the default layout.\n\n- Uses Nuxt color mode, so the choice is remembered per browser\n- The label goes through `t()`; I added `common.toggleTheme` to both `vi` and `en`\n- `pnpm typecheck` and `check-i18n` pass in my worktree\n\nReview the diff below and approve to merge it into the project.',
      'Xong. Mình thêm component `ThemeToggle` nhỏ, đặt cạnh `UserMenu` trong layout mặc định.\n\n- Dùng Nuxt color mode nên lựa chọn được nhớ theo trình duyệt\n- Nhãn đi qua `t()`; đã thêm `common.toggleTheme` cho cả `vi` và `en`\n- `pnpm typecheck` và `check-i18n` đều đạt trong worktree của mình\n\nBạn xem diff bên dưới, duyệt để gộp vào project.'), 12,
      [{ name: 'Read', summary: 'dashboard/app/layouts/default.vue' }, { name: 'Write', summary: 'dashboard/app/components/ThemeToggle.vue' }, { name: 'Bash', summary: 'pnpm --dir dashboard typecheck ✓' }, { name: 'Bash', summary: 'node dashboard/scripts/check-i18n.mjs ✓' }]),
    `INSERT INTO patches (id, conversation_id, message_id, diff, files, status, created_at) VALUES ('pch_demo_theme', 'cnv_demo_theme', 'msg_demo_t2', ${q(diff)}, ${q(JSON.stringify(['dashboard/app/components/ThemeToggle.vue', 'dashboard/app/layouts/default.vue']))}, 'pending', ${q(ts(12))})`,
    conv('cnv_demo_build', lead, T('Why did the nightly build fail?', 'Vì sao build đêm qua lỗi?'), 95),
    msg('msg_demo_b1', 'cnv_demo_build', 'user', user, T('The nightly build went red. What happened?', 'Build đêm qua đỏ rồi. Có chuyện gì vậy?'), 100),
    msg('msg_demo_b2', 'cnv_demo_build', 'assistant', lead.name, T(
      '**Cause:** `go vet` flagged a copied `sync.Mutex` in `internal/ops/manager.go:214` after yesterday\'s refactor.\n\n**Fix:** pass `*Manager` instead of `Manager` to `sampleOnce`. I delegated it to @' + dev.name + ', who is on it now.\n\n| Step | Result |\n|---|---|\n| `make build` | ok |\n| `go vet ./...` | 1 error |\n| `go test ./...` | ok |',
      '**Nguyên nhân:** `go vet` báo copy `sync.Mutex` ở `internal/ops/manager.go:214` sau đợt refactor hôm qua.\n\n**Cách sửa:** truyền `*Manager` thay vì `Manager` vào `sampleOnce`. Mình đã giao cho @' + dev.name + ', đang làm.\n\n| Bước | Kết quả |\n|---|---|\n| `make build` | ok |\n| `go vet ./...` | 1 lỗi |\n| `go test ./...` | ok |'), 95,
      [{ name: 'process_logs', summary: T('go vet (last 200 lines)', 'go vet (200 dòng cuối)') }, { name: 'Grep', summary: 'sampleOnce' }, { name: 'delegate', summary: dev.name }]),
    conv('cnv_demo_plan', lead, T('Plan next week: release + docs', 'Kế hoạch tuần sau: phát hành + tài liệu'), 600),
    msg('msg_demo_p1', 'cnv_demo_plan', 'user', user, T('Plan next week. Goal: ship the single-binary release and write the user guide.', 'Lên kế hoạch tuần sau. Mục tiêu: phát hành bản một file chạy và viết hướng dẫn sử dụng.'), 605),
    msg('msg_demo_p2', 'cnv_demo_plan', 'assistant', lead.name, T(
      'Here is a plan the team can run in parallel:\n\n1. **Mon–Tue** · embed the dashboard into the binary (backend)\n2. **Tue–Wed** · release workflow + checksums (devops)\n3. **Wed–Thu** · user guide with screenshots (docs)\n4. **Fri** · `office doctor` smoke test on 3 machines (QA)\n\nRisks: the embed doubles binary size; I suggest gzip assets.',
      'Kế hoạch để cả nhóm làm song song:\n\n1. **T2–T3** · gộp dashboard vào file chạy (backend)\n2. **T3–T4** · quy trình phát hành + checksum (devops)\n3. **T4–T5** · hướng dẫn sử dụng có ảnh (tài liệu)\n4. **T6** · chạy thử `office doctor` trên 3 máy (QA)\n\nRủi ro: gộp dashboard làm file chạy nặng gấp đôi; nên nén gzip.'), 600),
    `INSERT INTO conversation_tags (conversation_id, tag, created_at) VALUES ('cnv_demo_theme', 'ui', ${q(ts(12))}), ('cnv_demo_build', 'bug', ${q(ts(95))}), ('cnv_demo_plan', ${q(T('planning', 'kế-hoạch'))}, ${q(ts(600))})`
  ])
  // the build question came from the Discord bot, from the demo admin's own linked id (05-chat-answer, 28-users)
  await soft('discord id', () => api('PATCH', `/api/users/${me.id}`, { discord_id: '402881937615273984' }))
  const sender = { kind: 'discord', id: '402881937615273984', username: 'alex_dev', name: 'Alex N.', private: false, link: 'https://discord.com/channels/1/2/3', account_id: me.id, account: user }
  sql([`UPDATE messages SET author = 'discord:alex_dev', sender = ${q(JSON.stringify(sender))} WHERE id = 'msg_demo_b1'`])
  const runs = []
  const jobs = []
  for (let d = 0; d < 14; d++) {
    for (let k = 0; k < 3 + (d * 7) % 5; k++) {
      const a = [lead, dev, qa][k % 3]
      const cost = (0.04 + ((d * 13 + k * 7) % 23) / 100).toFixed(4)
      const at = ts(d * 1440 + k * 97 + 30)
      runs.push(`('run_demo_${d}_${k}', 'chat', ${q(ao.id)}, ${q(a.id)}, 'Claude Code', 'claude-sonnet-5-5', 'ok', ${1200 + k * 300}, ${400 + k * 90}, ${cost}, 'provider', ${20000 + k * 4000}, 'human:${EMAIL}', ${q(at)})`)
      jobs.push(`('job_demo_${d}_${k}', ${q(ao.id)}, 'chat_turn', ${k === 2 ? "'automation'" : "'user'"}, 'done', ${q(a.id)}, ${q((LANG === 'vi' ? ['Sửa test chập chờn', 'Review PR #42', 'Tóm tắt buổi sáng', 'Refactor ops sampler', 'Trả lời: build lỗi'] : ['Fix flaky test', 'Review PR #42', 'Morning brief', 'Refactor ops sampler', 'Answer: build failed'])[k % 5])}, ${cost}, ${1200 + k * 300}, ${400 + k * 90}, ${20000 + k * 4000}, ${q(at)}, ${q(at)}, ${q(at)})`)
    }
  }
  sql([
    `INSERT INTO runs (id, kind, project_id, agent_id, provider_name, model, status, input_tokens, output_tokens, cost_usd, cost_source, duration_ms, actor, created_at) VALUES ${runs.join(',')}`,
    `INSERT INTO jobs (id, project_id, kind, origin, status, agent_id, title, cost_usd, input_tokens, output_tokens, duration_ms, created_at, started_at, finished_at) VALUES ${jobs.join(',')}`
  ])

  // a finished run of the shop's first council workflow, for 44-workflow-run — not soft(): must exist
  const shopWorkflow = (await api('GET', `/api/projects/${shop.id}/workflows`)).workflows?.[0]
  if (!shopWorkflow) throw new Error('seed: demo-shop has no workflow (its council-3 workflow should be installed) — 41-workflow-edit/44-workflow-run need it')
  sql([conv('cnv_demo_council', lead, T('Council: new return policy', 'Hội đồng: chính sách đổi trả mới'), 40)])
  sql([`INSERT INTO workflow_runs (id, project_id, conversation_id, workflow_id, workflow_key, workflow_name, input, status, turns, cost_usd, result, roles, gates, log, actor, started_at, finished_at) VALUES ('run_demo_council', ${q(shop.id)}, 'cnv_demo_council', ${q(shopWorkflow.id)}, ${q(shopWorkflow.key)}, ${q(shopWorkflow.name)}, ${q(T('Should we extend the return window to 30 days?', 'Có nên kéo dài thời hạn đổi trả lên 30 ngày?'))}, 'done', 3, 0.0412, ${q(T('Approved: extend to 30 days, with a 10% restocking fee over 14 days.', 'Đồng ý: kéo dài lên 30 ngày, phí nhập lại 10% nếu quá 14 ngày.'))}, '[]', '[]', '[]', ${q('human:' + EMAIL)}, ${q(ts(40))}, ${q(ts(35))})`])
  // linked projects (ADR-155): agent-office hands work to demo-shop, hears back,
  // and a card waiting over there shows in its own chat (45-linked-chat)
  await soft('linked', async () => {
    const pol = await api('GET', `/api/projects/${ao.id}/policy`)
    await api('PUT', `/api/projects/${ao.id}/policy`, { ...pol.policy, version: pol.version, linked_projects: [shop.id] })
    const shopAgent = (await api('GET', `/api/projects/${shop.id}`)).project.agents?.[0]
    const there = 'cnv_demo_shop_req'
    const req = T('Add a "mockups" admin page: a list (preview, template, status, AI-generated or uploaded) and a New button (upload or generate).', 'Làm trang quản trị "mockup": danh sách (ảnh, template, trạng thái, AI tạo hay tải lên) và nút Thêm mới (tải lên hoặc generate).')
    const done = T('Done: `pages/mockups/index.vue` lists mockups with filters, and **New** opens upload or generate.\n\n- `pnpm test` passes in my worktree\n- Waiting for approval to run the e2e check', 'Xong: `pages/mockups/index.vue` liệt kê mockup có bộ lọc, nút **Thêm mới** mở tải lên hoặc generate.\n\n- `pnpm test` đạt trong worktree của mình\n- Đang chờ duyệt để chạy kiểm tra e2e')
    const from = { relayed_from: 'cnv_demo_linked', relayed_from_title: T('Mockup admin page', 'Trang quản trị mockup'), relayed_by_agent: lead.name, relayed_from_project: 'agent-office', relay_hops: 1 }
    const back = { relayed_from: there, relayed_from_title: T('Mockup admin page', 'Trang quản trị mockup'), relayed_by_agent: shopAgent.name, relayed_from_project: 'demo-shop', relayed_from_project_id: shop.id, relay_hops: 2, relay_report: true }
    const ctxMsg = (id, cid, role, author, content, ctx, minutesAgo) => `INSERT INTO messages (id, conversation_id, role, content, tools, author, context, created_at) VALUES (${q(id)}, ${q(cid)}, ${q(role)}, ${q(content)}, '[]', ${q(author)}, ${q(ctx ? JSON.stringify(ctx) : '')}, ${q(ts(minutesAgo))})`
    sql([
      conv('cnv_demo_linked', lead, T('Mockup admin page', 'Trang quản trị mockup'), 6),
      `INSERT INTO conversations (id, project_id, agent_id, agent_name, title, created_by, created_at, updated_at) VALUES (${q(there)}, ${q(shop.id)}, ${q(shopAgent.id)}, ${q(shopAgent.name)}, ${q(T('Mockup admin page', 'Trang quản trị mockup'))}, ${q('human:' + EMAIL)}, ${q(ts(9))}, ${q(ts(7))})`,
      msg('msg_demo_l1', 'cnv_demo_linked', 'user', me.name || 'Alex', T('Have demo-shop build the mockup admin page we agreed on.', 'Nhờ bên demo-shop làm trang quản trị mockup như đã bàn.'), 10),
      msg('msg_demo_l2', 'cnv_demo_linked', 'assistant', lead.name, T('I opened a chat in **demo-shop** with the full request; its answer will come back here.', 'Mình đã mở chat bên **demo-shop** kèm yêu cầu đầy đủ; câu trả lời sẽ tự về đây.'), 9),
      `INSERT INTO actions (id, project_id, conversation_id, message_id, kind, target, target_id, reason, status, detail, proposed_by, created_at, args) VALUES ('act_demo_linked_send', ${q(ao.id)}, 'cnv_demo_linked', 'msg_demo_l2', 'send_message', ${q('demo-shop · ' + T('new chat', 'chat mới'))}, ${q(there)}, '', 'done', '', ${q(lead.name)}, ${q(ts(9))}, ${q(JSON.stringify({ message: req, project: shop.id }))})`,
      ctxMsg('msg_demo_s1', there, 'user', 'human:' + EMAIL, req, from, 9),
      ctxMsg('msg_demo_s2', there, 'assistant', shopAgent.name, done, null, 7),
      `INSERT INTO actions (id, project_id, conversation_id, message_id, kind, target, reason, status, proposed_by, created_at) VALUES ('act_demo_shop_e2e', ${q(shop.id)}, ${q(there)}, 'msg_demo_s2', 'run_command', 'pnpm run e2e', ${q(T('Check the new page end to end', 'Kiểm tra trang mới từ đầu đến cuối'))}, 'pending', ${q(shopAgent.name)}, ${q(ts(7))})`,
      ctxMsg('msg_demo_l3', 'cnv_demo_linked', 'user', 'human:' + EMAIL, T('↩ Report from **demo-shop** (', '↩ Báo lại từ **demo-shop** (') + shopAgent.name + T('), [its chat](', '), [chat bên đó](') + `/projects/${shop.id}?tab=chat&c=${there}):\n\n` + done, back, 6),
      msg('msg_demo_l4', 'cnv_demo_linked', 'assistant', lead.name, T('demo-shop finished the page. One card waits for you below: the e2e check over there.', 'Bên demo-shop đã làm xong trang. Còn một thẻ chờ bạn duyệt bên dưới: chạy kiểm tra e2e bên đó.'), 5)
    ])
  })
  // proposals approved in order (47-approval-order): #1 from an older answer ("old"), #2 and #3 from the last one
  await soft('order', () => {
    const act = (id, mid, kind, target, reason, minutesAgo) => `INSERT INTO actions (id, project_id, conversation_id, message_id, kind, target, reason, status, proposed_by, created_at) VALUES (${q(id)}, ${q(ao.id)}, 'cnv_demo_order', ${q(mid)}, ${q(kind)}, ${q(target)}, ${q(reason)}, 'pending', ${q(lead.name)}, ${q(ts(minutesAgo))})`
    sql([
      conv('cnv_demo_order', lead, T('Release notes for v0.9', 'Ghi chú phát hành v0.9'), 3),
      msg('msg_demo_o1', 'cnv_demo_order', 'user', me.name || 'Alex', T('Write the v0.9 release notes and check the dashboard still builds.', 'Viết ghi chú phát hành v0.9 và kiểm dashboard vẫn build được.'), 8),
      msg('msg_demo_o2', 'cnv_demo_order', 'assistant', lead.name, T('Notes drafted in `CHANGELOG.md`. To be sure the dashboard builds, I need to run the build:', 'Đã soạn ghi chú trong `CHANGELOG.md`. Để chắc dashboard build được, mình cần chạy build:'), 7),
      act('act_demo_o1', 'msg_demo_o2', 'run_command', 'pnpm --dir dashboard build', T('Check the dashboard builds', 'Kiểm dashboard build được'), 7),
      msg('msg_demo_o3', 'cnv_demo_order', 'user', me.name || 'Alex', T('Also commit it and restart the dev server.', 'Commit luôn và chạy lại dev server.'), 5),
      msg('msg_demo_o4', 'cnv_demo_order', 'assistant', lead.name, T('Two more cards, in this order. They go after #1: approve the build first.', 'Thêm hai thẻ, theo đúng thứ tự. Chúng đứng sau #1: duyệt build trước.'), 4),
      act('act_demo_o2', 'msg_demo_o4', 'git_commit', 'docs: release notes v0.9', T('Commit the notes', 'Commit ghi chú'), 4),
      act('act_demo_o3', 'msg_demo_o4', 'restart_process', 'dev', T('Pick up the new build', 'Nhận bản build mới'), 4)
    ])
  })
  return { ao, shop, content, lead, dev }
}

// ---- 3. a headless browser over the DevTools protocol ----
async function browser() {
  const port = 9339
  const profile = join(HOME, 'chrome-profile')
  start('chrome', CHROME, [`--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, '--headless=new', '--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--force-device-scale-factor=1', 'about:blank'])
  let target
  for (let i = 0; i < 60 && !target; i++) {
    try { target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find(t => t.type === 'page') } catch { await sleep(250) }
  }
  let ws, seq = 0, viewport = { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false }
  const pending = new Map()
  const send = (method, params = {}) => new Promise((ok, fail) => {
    const id = ++seq
    const timer = setTimeout(() => { pending.delete(id); fail(new Error(`${method} timed out`)) }, 20000)
    pending.set(id, { ok: v => { clearTimeout(timer); ok(v) }, fail: e => { clearTimeout(timer); fail(e) } })
    ws.send(JSON.stringify({ id, method, params }))
  })
  async function attach(t) {
    ws = new WebSocket(t.webSocketDebuggerUrl)
    await new Promise((ok, fail) => { ws.onopen = ok; ws.onerror = fail })
    ws.onmessage = e => {
      const m = JSON.parse(e.data)
      if (m.id && pending.has(m.id)) {
        const { ok, fail } = pending.get(m.id)
        pending.delete(m.id)
        m.error ? fail(new Error(m.error.message)) : ok(m.result)
      }
      // a dialog blocks the page and every command after it; "leave page?" (beforeunload) must be accepted
      if (m.method === 'Page.javascriptDialogOpening') send('Page.handleJavaScriptDialog', { accept: m.params?.type === 'beforeunload' }).catch(() => {})
    }
    await send('Page.enable')
    await send('Runtime.enable')
    await send('Emulation.setDeviceMetricsOverride', viewport)
    target = t
  }
  await attach(target)
  const page = {
    size: (width, height, mobile = false) => {
      viewport = { width, height, deviceScaleFactor: mobile ? 2 : 1, mobile }
      return send('Emulation.setDeviceMetricsOverride', viewport)
    },
    // a page that stopped answering: drop the tab, carry on in a new one (same profile, still logged in)
    reset: async () => {
      const old = target
      try { ws.close() } catch {}
      const t = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json()
      await fetch(`http://127.0.0.1:${port}/json/close/${old.id}`).catch(() => {})
      await attach(t)
      log('(new tab)')
    },
    eval: async expr => (await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true })).result?.value,
    // until the page body (UDashboardPanel's) shows real content, no skeletons; at most 8s
    go: async (path, settle = 900) => {
      log('open', path)
      await send('Page.navigate', { url: UI + path })
      const t0 = Date.now()
      await sleep(800)
      while (Date.now() - t0 < 15000) {
        const ready = await page.eval(`(() => {
          const main = [...document.querySelectorAll('[data-slot="body"]')].pop() || document.body
          const busy = main.querySelector('[aria-busy="true"], .animate-pulse')
          const text = main.innerText
          return document.readyState === 'complete' && !busy && text.trim().length > 60 && !/Scanning|Đang quét|Loading/.test(text)
        })()`).catch(() => false)
        if (ready) break
        await sleep(300)
      }
      await sleep(settle)
    },
    shot: async (name, full = false) => {
      // the machine's own name and paths never reach a screenshot
      const reps = [[join('/private', HOME), '/tmp/agent-office-demo'], [HOME, '/tmp/agent-office-demo'], [mainRoot, '~/agent-office'], [homedir(), '~'],
        [hostname(), 'my-machine'], [hostname().replace(/\.local$/, ''), 'my-machine']]
      await page.eval(`(() => {
        const reps = ${JSON.stringify(reps)}
        const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
        for (let n; (n = walk.nextNode());) {
          let t = n.nodeValue
          for (const [a, b] of reps) t = t.split(a).join(b)
          if (t !== n.nodeValue) n.nodeValue = t
        }
        return true
      })()`).catch(() => {})
      const opts = { format: 'png' }
      if (full) {
        const { cssContentSize } = await send('Page.getLayoutMetrics')
        opts.captureBeyondViewport = true
        opts.clip = { x: 0, y: 0, width: cssContentSize.width, height: Math.min(cssContentSize.height, 2400), scale: 1 }
      }
      const { data } = await send('Page.captureScreenshot', opts)
      writeFileSync(join(OUT, name + '.png'), Buffer.from(data, 'base64'))
      log('shot', name)
    },
    click: text => page.eval(`(() => { const el = [...document.querySelectorAll('button,a,[role=tab],[role=menuitem]')].find(e => e.textContent.trim() === ${JSON.stringify(text)}); el?.click(); return !!el })()`)
  }
  return page
}

// ---- 4. the screens ----
async function capture({ ao, shop, lead }) {
  mkdirSync(OUT, { recursive: true })
  const page = await browser()
  if (MOBILE) await page.size(390, 844, true)
  else await page.size(1440, 900)
  await page.go('/login', 1500)
  await page.eval(`document.cookie = 'office-lang=${LANG}; path=/; max-age=31536000'; localStorage.setItem('nuxt-color-mode', 'light'); true`)
  await page.go('/login', 2000)
  if ((!onlyMissing || !existsSync(join(OUT, '01-login.png'))) && (!only.length || only.includes('01-login')) && !paths.length) await page.shot('01-login')
  const ok = await page.eval(`fetch('/api/auth/login', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ email: ${JSON.stringify(EMAIL)}, password: ${JSON.stringify(PASSWORD)} }) }).then(r => r.ok)`)
  if (!ok) throw new Error('browser login failed')

  const P = `/projects/${ao.id}`
  // 38-automation-detail, 39-automation-edit, 41-workflow-edit, 42/43-bot-*, 44-workflow-run: daily-use screens that
  // need real seeded fixtures. Fetch them for real (no soft()) and throw by name if missing, instead of silently
  // dropping the screen while the run still reports success — that silent-skip was the whole gap this closes.
  // Skipped when --path is used (fixtures unused there) or --only names none of these screens.
  const wantsFixture = !paths.length && (!only.length || only.some(n => /^(38|39|41|42|43|44)-/.test(n)))
  let webhookAuto, shopWorkflow, bot, councilRun
  if (wantsFixture) {
    const automations = (await api('GET', `/api/projects/${ao.id}/automations`)).automations
    webhookAuto = automations?.find(a => a.source === 'webhook') ?? automations?.[0]
    if (!webhookAuto) throw new Error('capture: no automation found on ao project — 38-automation-detail/39-automation-edit need one')
    shopWorkflow = (await api('GET', `/api/projects/${shop.id}/workflows`)).workflows?.[0]
    if (!shopWorkflow) throw new Error('capture: demo-shop has no workflow — 41-workflow-edit needs one')
    bot = (await api('GET', `/api/projects/${ao.id}/channels`)).channels?.[0]
    if (!bot) throw new Error('capture: no channel/bot found on ao project — 42-bot-detail/43-bot-edit need one')
    councilRun = (await api('GET', `/api/projects/${shop.id}/workflow-runs?workflow=${shopWorkflow.id}&limit=1`)).runs?.[0]
    if (!councilRun) throw new Error('capture: demo-shop workflow has no run — 44-workflow-run needs one')
  }
  // opens the "New project" modal and selects the "idea" scope card, for 04a-new-project-idea
  const openIdeaModal = async () => {
    const opened = await page.click(T('Add project', 'Thêm project'))
    if (!opened) throw new Error('new-project button not found')
    for (let i = 0; i < 20; i++) {
      if (await page.eval(`!!document.querySelector('[role=dialog]')`).catch(() => false)) break
      await sleep(200)
    }
    const ideaTitle = T('From an idea', 'Từ ý tưởng')
    const picked = await page.eval(`(() => { const el = [...document.querySelectorAll('button')].find(b => b.querySelector('p')?.textContent.trim() === ${JSON.stringify(ideaTitle)}); el?.click(); return !!el })()`)
    if (!picked) throw new Error('idea scope card not found')
    await sleep(400)
  }
  const searchLibrary = async (query, pick = '') => {
    const found = await page.eval(`(() => {
      const input = document.querySelector('input[placeholder=${JSON.stringify(T('Search agents…', 'Tìm agent…'))}]')
      if (!input) return false
      input.value = ${JSON.stringify(query)}
      input.dispatchEvent(new Event('input', { bubbles: true }))
      return true
    })()`)
    if (!found) throw new Error('agent library search not found')
    await sleep(400)
    if (pick && !await page.eval(`(() => {
      const button = [...document.querySelectorAll('button')].find(b => b.querySelector('p')?.textContent.trim() === ${JSON.stringify(pick)})
      button?.click()
      return !!button
    })()`)) throw new Error('library agent not found: ' + pick)
    await sleep(400)
  }
  const requirePackNotice = async () => {
    await page.eval(`(() => { const style = document.createElement('style'); style.textContent = '*{animation:none!important;transition:none!important}'; document.head.append(style); return true })()`)
    if (!await page.eval(`document.body.textContent.includes(${JSON.stringify(T('Requires @agentoffice/languages', 'Cần gói @agentoffice/languages'))})`)) throw new Error('agent pack requirement notice not found')
  }
  const screens = [
    ['48-agent-variants', '/agents', async () => {
      await searchLibrary('code-architect')
      if (!await page.eval(`document.body.textContent.includes('code-architect (1)') && document.body.textContent.includes('code-architect (2)')`)) throw new Error('agent variants missing')
    }],
    ['49-agent-pack', '/agents', async () => {
      await searchLibrary('teacher', 'Giáo viên')
      await requirePackNotice()
    }],
    ['50-agent-pack-add', `${P}?tab=agents&add=office/teacher`, requirePackNotice],
    ['02-overview', '/'],
    ['03-projects', '/projects'],
    ['04a-new-project-idea', '/projects', openIdeaModal],
    ['04-chat', `${P}?tab=chat&c=cnv_demo_theme`],
    ['05-chat-answer', `${P}?tab=chat&c=cnv_demo_build`],
    // linked projects (ADR-155): a report back (opened) and the other side's card, decided here
    ['45-linked-chat', `${P}?tab=chat&c=cnv_demo_linked`, async () => {
      await page.eval(`(() => { document.querySelector('[id="m-msg_demo_l3"] summary')?.click(); return true })()`)
      await sleep(400)
    }],
    // the header bell: what needs you, each with its buttons (ADR-156)
    ['46-bell', '/', async () => {
      // a headless tab runs no animation: the list would stay half faded in
      const opened = await page.eval(`(() => { const st = document.createElement('style'); st.textContent = '*{animation:none!important;transition:none!important}'; document.head.append(st); const b = [...document.querySelectorAll('button[aria-label]')].find(e => /need you|cần xử lý/.test(e.getAttribute('aria-label'))); b?.click(); return !!b })()`)
      if (!opened) throw new Error('bell not found')
      await sleep(800)
    }],
    // a phone: the box hides behind a button until asked for; open it to see its toolbar
    ...(MOBILE ? [['05b-chat-compose', `${P}?tab=chat&c=cnv_demo_build`, async () => {
      const opened = await page.eval(`(() => { const b = document.querySelector('button[aria-label="Viết tin nhắn"], button[aria-label="Write a message"]'); b?.click(); return !!b })()`)
      if (!opened) throw new Error('compose button not found')
      await sleep(400)
    }]] : []),
    ['06-team-model', `${P}?tab=agents`],
    ['07-agent', `${P}/agents/${lead?.id}`],
    ['08-permissions', `${P}?tab=perm`],
    ['09-automations', `${P}?tab=automations`],
    ['10-automation-builder', `${P}/automations/new`],
    ['11-operations', `${P}?tab=ops`],
    ['12-files', `${P}?tab=files`],
    ['13-burn', `${P}?tab=burn`],
    ['13b-burn-settings', `${P}?tab=burn`, async () => { await page.click(T('Settings', 'Cài đặt')); await sleep(900) }],
    ['14-skills', `${P}?tab=skill`],
    ['15-mcp', `${P}?tab=mcp`],
    ['16-change-log', `${P}?tab=log`],
    ['17-project-info', `${P}?tab=info`],
    ['18-setup-ai', `${P}/setup`],
    ['19-bot', `${P}/bots/new`],
    ['20-council', `/projects/${shop.id}?tab=workflows`],
    ['21-assistant', '/assistant'],
    // a grid dashboard of widgets (kept with the account): figures, an AI connection, a chat, a chart, what waits for you
    ['22-watch', '/watch', async () => {
      const box = (widget, x, y, w, h, extra = {}) => ({ id: `w${x}_${y}`, widget, projectId: ao.id, x, y, w, h, ...extra })
      const board = { id: 'demo', name: T('On call', 'Ca trực'), projectId: ao.id, items: [
        // one widget, several sizes: a badge, a strip, a card
        box('stat', 0, 0, 2, 2, { config: { metric: 'running' } }),
        box('agent', 2, 0, 2, 2),
        box('provider', 4, 0, 2, 2, { projectId: '' }),
        box('approvals', 6, 0, 1, 1, { projectId: '' }),
        box('automations', 7, 0, 1, 1),
        box('agent', 6, 1, 1, 1),
        box('stat', 7, 1, 1, 1, { config: { metric: 'pending' } }),
        box('note', 8, 0, 4, 2, { projectId: '', config: { text: T('Release Friday: watch the nightly build and the shop API.', 'Phát hành thứ Sáu: để mắt build đêm và API của shop.') } }),
        box('chat', 0, 2, 4, 6, { conversationId: 'cnv_demo_build' }),
        box('agent', 4, 2, 4, 1),
        box('provider', 8, 2, 4, 1, { projectId: '' }),
        box('chart', 4, 3, 4, 3),
        box('approvals', 8, 3, 4, 3, { projectId: '' }),
        box('agent', 4, 6, 4, 3),
        box('automations', 8, 6, 4, 1),
        box('git', 8, 7, 2, 2),
        box('breakdown', 10, 7, 2, 2, { projectId: '' })
      ] }
      await api('PUT', '/api/me/prefs/watch', { value: { active: 'demo', boards: [board] } })
      await page.go('/watch', 3500)
    }],
    // proposals are numbered and approved in order; a card from an older answer is marked "old"
    ['47-approval-order', `${P}?tab=chat&c=cnv_demo_order`],
    ['23-providers', '/providers'],
    ['24-templates', '/workflows'],
    ['25-library', '/skills'],
    ['26-jobs', '/jobs'],
    ['27-stats', '/?tab=stats'],
    ['37-machine', '/?tab=machine'],
    ['28-users', '/admin/users'],
    ['29-audit', '/admin/audit'],
    ['30-data', '/admin/data'],
    ['31-transfer', '/admin/transfer'],
    ['33-account', '/account'],
    ['40-skill-edit', `${P}/skills/edit?name=release-notes&scope=user`],
    // these 6 need wantsFixture's lookups above, which throw by name if the fixture is missing — never silently
    // dropped here like the conditional `...(x ? [...] : [])` spreads this replaced
    ...(wantsFixture ? [
      ['38-automation-detail', `${P}/automations/${webhookAuto.id}`], ['39-automation-edit', `${P}/automations/${webhookAuto.id}/edit`],
      ['41-workflow-edit', `/projects/${shop.id}/workflows/edit?w=${shopWorkflow.id}`],
      ['42-bot-detail', `${P}/bots/${bot.id}`], ['43-bot-edit', `${P}/bots/${bot.id}/edit`],
      ['44-workflow-run', `/projects/${shop.id}/workflows/runs/${councilRun.id}`]
    ] : [])
  ]
  const want = name => (!only.length || only.includes(name)) && (!onlyMissing || retake.includes(name) || !existsSync(join(OUT, name + '.png')))
  // 38-44 must come out with real content — fail the whole run rather than report success with gaps
  const REQUIRED = /^(38|39|40|41|42|43|44|48|49|50)-/
  const take = async (name, path, prep) => {
    if (!want(name)) return
    for (let attempt = 1; attempt <= 2; attempt++) {
      try { await page.go(path); if (prep) await prep(); await page.shot(name, MOBILE); return } catch (e) {
        log(`(${attempt === 1 ? 'retry' : 'skip'}) ${name}: ${e.message}`)
        if (attempt === 2 && REQUIRED.test(name)) throw new Error(`capture: ${name} failed twice, giving up: ${e.message}`)
        await page.reset().catch(err => log('(reset failed)', err.message))
      }
    }
  }
  if (paths.length) {
    for (const path of paths) {
      const route = path.replaceAll('{project}', P)
      await take('path' + (route.replace(/[^a-zA-Z0-9]+/g, '-').replace(/-+$/, '') || '-'), route)
    }
    return
  }
  for (const [name, path, prep] of screens) await take(name, path, prep)
  if (MOBILE) return // the guide's extras (dark, the update page) are not for a phone review

  // dark mode and phone
  const mode = m => page.eval(`localStorage.setItem('nuxt-color-mode', '${m}'); true`).catch(() => {})
  await mode('dark')
  await take('34-dark-chat', `${P}?tab=chat&c=cnv_demo_theme`)
  await take('35-dark-overview', '/')
  await mode('light')
  await page.size(390, 844, true)
  await take('36-mobile-chat', `${P}?tab=chat&c=cnv_demo_build`)
  // last: this page streams the build log and has hung the tab before
  await page.size(1440, 900)
  await take('32-update', '/admin/update')
}

// --reuse: use the demo office a previous --reuse run left running (same --lang), and
// leave it running; shoots in batches when one run has a time limit. --stop: shut it down.
const reuse = process.argv.includes('--reuse')
// one run at a time: two runs share the demo folder and ports, and the second wipes the first
const LOCK = join(tmpdir(), 'agent-office-demo.lock')
function lock() {
  try {
    const pid = Number(readFileSync(LOCK, 'utf8'))
    if (pid && pid !== process.pid) {
      try { process.kill(pid, 0); console.error(`another capture is running (pid ${pid}); wait for it or kill it`); process.exit(2) } catch {}
    }
  } catch {}
  writeFileSync(LOCK, String(process.pid))
  process.on('exit', () => { try { if (readFileSync(LOCK, 'utf8') === String(process.pid)) rmSync(LOCK) } catch {} })
}
if (!process.argv.includes('--stop')) lock()
try {
  if (process.argv.includes('--stop')) {
    killPorts([18787, 12704, 18900, 9339])
    log('demo office stopped')
    process.exit(0)
  }
  let ctx
  const alive = reuse && await fetch(`http://${API}/api/auth/me`).then(() => fetch(UI + '/login')).then(() => true, () => false)
  if (alive) {
    log('reusing the demo office at', UI)
    await login()
    ctx = await lookup()
  } else {
    await build()
    ctx = await seed()
  }
  if (reuse) children.splice(0) // the server and dashboard outlive this run
  killPorts([9339]) // a browser a killed run left behind
  await sleep(500)
  await capture(ctx)
  log('done →', OUT)
  if (process.env.KEEP) {
    log(`demo office still running: ${UI} (${EMAIL} / ${PASSWORD}); Ctrl-C to stop`)
    await new Promise(() => {})
  }
} catch (e) {
  console.error(e)
  process.exitCode = 1
} finally {
  if (!process.env.KEEP) stopAll()
}
