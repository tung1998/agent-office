// Regression test for public/sw.js's request(act, x): notification action
// buttons for 'retrying' incidents (useApprovals.ts incidentActs()) must map
// to the same API call useApprovals.ts's actOnIncident() makes, including
// method (cancelRetry is DELETE, not POST).
import assert from 'node:assert/strict'
import vm from 'node:vm'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const dir = dirname(fileURLToPath(import.meta.url))
const src = readFileSync(join(dir, '../public/sw.js'), 'utf8')

const sandbox: Record<string, unknown> = { self: { addEventListener: () => {} } }
vm.createContext(sandbox)
vm.runInContext(src + '\nglobalThis.__request = request', sandbox)
const request = sandbox.__request as (act: string, x: Record<string, unknown>) => [string, string, unknown] | null

{
  const req = request('retryNow', { id: 'c1' })
  assert.ok(req, 'retryNow must produce a request (was falling through to opening the chat tab)')
  assert.equal(req![0], '/api/conversations/c1/retry-now')
  assert.equal(req![1], 'POST')
}

{
  const req = request('cancelRetry', { id: 'c1' })
  assert.ok(req, 'cancelRetry must produce a request (was falling through to opening the chat tab)')
  assert.equal(req![0], '/api/conversations/c1/retry')
  assert.equal(req![1], 'DELETE', 'cancelRetry must use DELETE, matching useApprovals.ts actOnIncident(), not POST')
}

console.log('test-sw-request: ok')
