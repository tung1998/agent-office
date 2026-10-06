#!/usr/bin/env node
// Builds a throwaway demo office (its own data folder and ports, never your
// real one), fills it with sample data, and screenshots every screen of the
// dashboard into docs/guide/images/ for the guide (docs/guide/index.html).
//
//   make build && make ui-build      # bin/office and dashboard/.output
//   node docs/guide/capture.mjs
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
const OUT = LANG === 'vi' ? join(here, 'images', 'vi') : join(here, 'images')
const CONTENT = T('Content & Planning', 'Kế hoạch & Nội dung')
// --only-missing (or ONLY_MISSING=1) keeps the shots already there
// --retake=08-permissions,19-bot shoots those again too
const onlyMissing = process.argv.includes('--only-missing') || !!process.env.ONLY_MISSING
const retake = (process.argv.find(a => a.startsWith('--retake=')) ?? '').slice(9).split(',').filter(Boolean)
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
  office(['project', 'add', aoCopy, '--name', 'agent-office', '--template', 'team'])
  office(['project', 'add', shop, '--name', 'demo-shop', '--template', 'council'])
  office(['project', 'add', '--name', CONTENT, '--template', 'solo'])

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
  const agents = ao.model?.agents ?? []
  const lead = agents.find(a => a.tier === 'lead') ?? agents[0]
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
  const mons = [
    await soft('monitor', () => mon({ name: T('Docs site', 'Trang tài liệu'), type: 'http', target: 'http://127.0.0.1:18900/', interval_s: 60 })),
    await soft('monitor', () => mon({ name: 'Office API', type: 'tcp', target: API, interval_s: 60 })),
    await soft('monitor', () => mon({ name: T('Nightly backup', 'Sao lưu hằng đêm'), type: 'heartbeat', interval_s: 86400 }))
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
  await auto({ name: T('Sentry alert → triage', 'Cảnh báo Sentry → phân loại'), source: 'webhook', action: 'chat', agent_id: dev?.id, prompt: T('A Sentry alert arrived:\n{{payload}}\nFind the root cause and propose a fix.', 'Có cảnh báo Sentry:\n{{payload}}\nTìm nguyên nhân và đề xuất cách sửa.'), limits: { max_runs_per_hour: 6, debounce_seconds: 120 } })
  await auto({ name: T('Weekly dependency check', 'Kiểm tra thư viện hằng tuần'), source: 'schedule', action: 'chat', agent_id: qa?.id, config: { cron: '0 9 * * 1', timezone: 'Asia/Ho_Chi_Minh' }, prompt: T('List outdated Go and npm dependencies, flag risky upgrades.', 'Liệt kê thư viện Go và npm đã cũ, đánh dấu bản nâng cấp rủi ro.') })

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
  await page.size(1440, 900)
  await page.go('/login', 1500)
  await page.eval(`document.cookie = 'office-lang=${LANG}; path=/; max-age=31536000'; localStorage.setItem('nuxt-color-mode', 'light'); true`)
  await page.go('/login', 2000)
  if (!onlyMissing || !existsSync(join(OUT, '01-login.png'))) await page.shot('01-login')
  const ok = await page.eval(`fetch('/api/auth/login', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ email: ${JSON.stringify(EMAIL)}, password: ${JSON.stringify(PASSWORD)} }) }).then(r => r.ok)`)
  if (!ok) throw new Error('browser login failed')

  const P = `/projects/${ao.id}`
  const screens = [
    ['02-overview', '/'],
    ['03-projects', '/projects'],
    ['04-chat', `${P}?tab=chat&c=cnv_demo_theme`],
    ['05-chat-answer', `${P}?tab=chat&c=cnv_demo_build`],
    ['06-team-model', `${P}?tab=model`],
    ['07-agent', `${P}/agents/${lead?.id}`],
    ['08-permissions', `${P}?tab=perm`],
    ['09-automations', `${P}?tab=automations`],
    ['10-automation-builder', `${P}/automations/new`],
    ['11-operations', `${P}?tab=ops`],
    ['12-files', `${P}?tab=files`],
    ['13-burn', `${P}?tab=burn`],
    ['14-skills', `${P}?tab=skill`],
    ['15-mcp', `${P}?tab=mcp`],
    ['16-change-log', `${P}?tab=log`],
    ['17-project-info', `${P}?tab=info`],
    ['18-setup-ai', `${P}/setup`],
    ['19-bot', `${P}/bots/new`],
    ['20-council', `/projects/${shop.id}?tab=model`],
    ['21-assistant', '/assistant'],
    ['22-watch', '/watch'],
    ['23-providers', '/providers'],
    ['24-templates', '/templates'],
    ['25-library', '/library'],
    ['26-jobs', '/jobs'],
    ['27-stats', '/?tab=stats'],
    ['37-machine', '/?tab=machine'],
    ['28-users', '/admin/users'],
    ['29-audit', '/admin/audit'],
    ['30-data', '/admin/data'],
    ['31-transfer', '/admin/transfer'],
    ['33-account', '/account']
  ]
  const want = name => !onlyMissing || retake.includes(name) || !existsSync(join(OUT, name + '.png'))
  const take = async (name, path) => {
    if (!want(name)) return
    for (let attempt = 1; attempt <= 2; attempt++) {
      try { await page.go(path); await page.shot(name); return } catch (e) {
        log(`(${attempt === 1 ? 'retry' : 'skip'}) ${name}: ${e.message}`)
        await page.reset().catch(err => log('(reset failed)', err.message))
      }
    }
  }
  for (const [name, path] of screens) await take(name, path)

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
