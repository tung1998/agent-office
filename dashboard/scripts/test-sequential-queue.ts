// Regression tests for the FIFO queue used by CliUpdates.vue's auto_update
// saves. Imports the real app/utils/sequentialQueue.ts (not a
// reimplementation), so a change to that file is what this test exercises.
import assert from 'node:assert/strict'
import { SequentialQueue } from '../app/utils/sequentialQueue.ts'

function delay<T>(ms: number, value: T): Promise<T> {
  return new Promise(r => setTimeout(() => r(value), ms))
}

// (a) a slow op pushed first must still finish before a fast op pushed
// right after it starts -- order of completion follows order of push(),
// not which op is quicker.
{
  const queue = new SequentialQueue()
  const order: string[] = []
  queue.push(async () => { await delay(20, null); order.push('slow') })
  queue.push(async () => { await delay(0, null); order.push('fast') })
  await delay(30, null)
  assert.deepEqual(order, ['slow', 'fast'], 'ops run in push order even when a later one would resolve first on its own')
}

// (b) an op that throws must not break the chain: ops pushed after it still
// run, and in order.
{
  const queue = new SequentialQueue()
  const order: string[] = []
  queue.push(async () => { throw new Error('boom') })
  queue.push(async () => { order.push('after-throw') })
  await delay(10, null)
  assert.deepEqual(order, ['after-throw'], 'a later op still runs after an earlier one throws')
}

console.log('test-sequential-queue: ok')
