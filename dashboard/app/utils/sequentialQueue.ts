// Runs pushed ops strictly one after another (FIFO), used by CliUpdates.vue
// to send a series of auto_update PUTs in the order the user made the edits
// -- never concurrently, so a later PUT's response can't land and get
// treated as "confirmed" before an earlier PUT (still in flight) finishes.
// An op that throws never breaks the chain for ops pushed after it; callers
// that care about a rejection should catch inside their own op.
export class SequentialQueue {
  private chain: Promise<unknown> = Promise.resolve()

  push(op: () => Promise<void>): Promise<void> {
    const run = this.chain.then(op, op)
    this.chain = run.catch(() => {})
    return run
  }
}
