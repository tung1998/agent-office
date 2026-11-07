// Queued optimistic save for a single full-replace value (e.g. a PUT that
// takes a whole map, like CliUpdates.vue's auto_update), shared by
// CliUpdates.vue and its tests (no Vue). `getState`/`setState` are plain
// closures the caller provides over its own reactive ref, so this class
// never owns the display value itself -- assigning through setState() is
// what makes the UI re-render, which an internal ref/field here could not
// guarantee once wrapped behind get()/set() methods on a plain class.
import { SequentialQueue } from './sequentialQueue.ts'

export class AutoSaveQueue<T> {
  private confirmed: T
  private dirty = 0
  private readonly chain = new SequentialQueue()
  private readonly getState: () => T
  private readonly setState: (value: T) => void
  private readonly save: (value: T) => Promise<void>

  constructor(initial: T, getState: () => T, setState: (value: T) => void, save: (value: T) => Promise<void>) {
    this.confirmed = initial
    this.getState = getState
    this.setState = setState
    this.save = save
  }

  // Call after a fresh read of server state (e.g. a page poll). Ignored
  // while an edit is queued or in flight: a read that started before that
  // edit must not land after it and clobber it back to the pre-edit value.
  sync(value: T) {
    if (this.dirty) return
    this.confirmed = value
    this.setState(value)
  }

  // Call right after the caller has already applied an edit via setState --
  // reads that applied state back out, so a save queued while an earlier one
  // is still in flight is built from every edit made so far, not a stale
  // snapshot taken before this call.
  queue() {
    this.dirty++
    const value = this.getState()
    this.chain.push(async () => {
      try {
        await this.save(value)
        this.confirmed = value
      } catch {
        // only snap back to the server's last known-good value if nothing
        // newer is still queued -- a later edit must not lose its own change
        // just because an earlier, now-superseded save happened to fail
        if (this.dirty === 1) this.setState(this.confirmed)
      } finally {
        this.dirty--
      }
    })
  }
}
