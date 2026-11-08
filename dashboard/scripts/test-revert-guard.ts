// Regression test for useProjectMenu.ts's save() rollback clobbering a
// concurrent successful save: hide a project (call A, slow PUT) then reorder
// (call B, fast PUT, succeeds) before A's PUT rejects. A's catch must not
// revert to the snapshot from before both calls, which would silently
// discard B's already-persisted change. Imports the real createRevertGuard
// (not a reimplementation), run with node --experimental-strip-types.
import assert from 'node:assert/strict'
import { createRevertGuard } from '../app/composables/useProjectMenu.ts'

function delay(ms: number): Promise<void> {
  return new Promise(r => setTimeout(r, ms))
}

type Prefs = { order: string[], hidden: string[] }

async function save(guard: ReturnType<typeof createRevertGuard>, state: { value: Prefs }, next: Prefs, putDelayMs: number, putFails: boolean) {
  const before = state.value
  const ticket = guard.begin()
  state.value = next
  await delay(putDelayMs)
  if (putFails) {
    if (guard.shouldRevert(ticket)) state.value = before
    return
  }
}

{
  const guard = createRevertGuard()
  const state = { value: { order: [], hidden: [] } as Prefs }
  const callA = save(guard, state, { order: [], hidden: ['p1'] }, 30, true) // hide p1, slow, fails
  await delay(5)
  const callB = save(guard, state, { order: ['p2', 'p1'], hidden: ['p1'] }, 5, false) // reorder, fast, succeeds
  await Promise.all([callA, callB])
  assert.deepEqual(state.value, { order: ['p2', 'p1'], hidden: ['p1'] }, 'B\'s reorder must survive A\'s later failed revert')
}

// without overlap, a failed save still reverts normally
{
  const guard = createRevertGuard()
  const state = { value: { order: [], hidden: [] } as Prefs }
  await save(guard, state, { order: [], hidden: ['p1'] }, 0, true)
  assert.deepEqual(state.value, { order: [], hidden: [] }, 'a lone failed save reverts')
}

console.log('test-revert-guard: ok')
