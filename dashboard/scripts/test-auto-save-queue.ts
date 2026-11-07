// Regression test for the race CliUpdates.vue setToolMode()/mode had:
// changing tool A's mode, then tool B's mode before A's PUT response lands,
// used to build B's "tools" map from the stale pre-A snapshot, so whichever
// response arrived last silently dropped the other's change. Imports the
// real app/utils/autoSaveQueue.ts (not a reimplementation of its logic), so
// a change to that file is what this test exercises. `getState`/`setState`
// here stand in for a Vue ref's getter/setter, as CliUpdates.vue uses them.
import assert from 'node:assert/strict'
import { AutoSaveQueue } from '../app/utils/autoSaveQueue.ts'

function delay<T>(ms: number, value: T): Promise<T> {
  return new Promise(r => setTimeout(() => r(value), ms))
}

type Auto = { mode: string, tools: Record<string, string> }

// the exact CliUpdates scenario: setToolMode('codex', 'auto') fires a slow
// PUT; before it resolves, setToolMode('claude', 'off') fires a fast PUT.
async function run(order: 'slow-first' | 'fast-first') {
  const state = { value: { mode: 'notify', tools: {} } as Auto }
  let calls = 0
  const queue = new AutoSaveQueue<Auto>(
    state.value,
    () => state.value,
    v => { state.value = v },
    async () => {
      calls++
      const mine = calls
      await delay(order === 'slow-first' && mine === 1 ? 20 : 0, null)
    }
  )
  // mirrors CliUpdates.vue's setToolMode(): mutate the ref, then queue the save
  const edit = (id: string, v: string) => {
    state.value = { mode: state.value.mode, tools: { ...state.value.tools, [id]: v } }
    queue.queue()
  }
  edit('codex', 'auto')
  edit('claude', 'off')
  await delay(30, null)
  return state.value.tools
}

{
  const tools = await run('slow-first')
  assert.deepEqual(tools, { codex: 'auto', claude: 'off' }, 'both edits survive when the first request is the slow one')
}
{
  const tools = await run('fast-first')
  assert.deepEqual(tools, { codex: 'auto', claude: 'off' }, 'both edits survive regardless of response order')
}

// (b) setState (the UI-facing ref) reflects an edit immediately, even while
// its save is still in flight -- this is the actual UI-freeze bug: a naive
// implementation that only updates state from the PUT response would leave
// a mode select stuck on the old value until the response lands.
{
  const state = { value: { mode: 'notify', tools: {} } as Auto }
  const queue = new AutoSaveQueue<Auto>(state.value, () => state.value, v => { state.value = v }, async () => { await delay(20, null) })
  state.value = { mode: 'auto', tools: {} }
  queue.queue()
  assert.equal(state.value.mode, 'auto', 'the UI-facing state reflects the edit synchronously, not only after the save resolves')
}

// (c) a failed save rolls the UI-facing state back to the server's last
// confirmed value (not to whatever the failed PUT attempted), but only when
// it is the last edit still pending -- a newer edit queued after the failing
// one must not be wiped out by that rollback.
{
  const state = { value: { mode: 'notify', tools: {} } as Auto }
  let calls = 0
  const queue = new AutoSaveQueue<Auto>(
    state.value,
    () => state.value,
    v => { state.value = v },
    async () => {
      calls++
      if (calls === 1) { await delay(5, null); throw new Error('network error') }
    }
  )
  state.value = { mode: 'auto', tools: {} } // edit #1: will fail
  queue.queue()
  await delay(10, null) // let #1 fail and roll back; nothing else queued
  assert.equal(state.value.mode, 'notify', 'a failed save with nothing queued after it rolls the UI back to the last confirmed value')

  state.value = { mode: 'auto', tools: {} } // edit #2: will fail
  queue.queue()
  state.value = { mode: 'off', tools: {} } // edit #3: queued before #2's rollback can run
  queue.queue()
  await delay(10, null)
  assert.equal(state.value.mode, 'off', 'edit #3 survives even though edit #2 (queued before it) failed and would otherwise roll back over it')
}

// (d) a save that throws must not break the chain: a save queued after a
// failing one still runs.
{
  const state = { value: { mode: 'notify', tools: {} } as Auto }
  let secondRan = false
  const queue = new AutoSaveQueue<Auto>(
    state.value,
    () => state.value,
    v => { state.value = v },
    async (v) => {
      if (v.mode === 'auto') throw new Error('boom')
      secondRan = true
    }
  )
  state.value = { mode: 'auto', tools: {} }
  queue.queue()
  state.value = { mode: 'off', tools: {} }
  queue.queue()
  await delay(10, null)
  assert.ok(secondRan, 'a save queued after a failing one still runs')
}

// (e) sync() is ignored while an edit is queued or in flight, so a server
// read (e.g. a page poll) started before the edit can't land after it and
// clobber it back to the pre-edit value.
{
  const state = { value: { mode: 'notify', tools: {} } as Auto }
  const queue = new AutoSaveQueue<Auto>(state.value, () => state.value, v => { state.value = v }, async () => { await delay(20, null) })
  state.value = { mode: 'auto', tools: {} }
  queue.queue() // edit in flight
  queue.sync({ mode: 'notify', tools: {} }) // a poll that started before the edit
  assert.equal(state.value.mode, 'auto', 'sync() does not clobber an edit that is queued or in flight')
  await delay(25, null)
  queue.sync({ mode: 'notify', tools: {} }) // safe once nothing is pending
  assert.equal(state.value.mode, 'notify', 'sync() applies once nothing is queued or in flight')
}

console.log('test-auto-save-queue: ok')
