// Tests for auditDiff (ADR-043): run with node --experimental-strip-types.
import assert from 'node:assert/strict'
import { auditDiff } from '../app/composables/useAudit.ts'

assert.deepEqual(auditDiff({ name: 'a', n: 1, same: true }, { name: 'b', n: 1, same: true, added: 'x' }),
  [{ key: 'added', before: undefined, after: 'x' }, { key: 'name', before: 'a', after: 'b' }])
assert.deepEqual(auditDiff(null, { a: 1 }), [{ key: 'a', before: undefined, after: 1 }])
assert.deepEqual(auditDiff({ a: 1 }, null), [{ key: 'a', before: 1, after: undefined }])
assert.deepEqual(auditDiff({ cfg: { cron: '1' } }, { cfg: { cron: '2' } }), [{ key: 'cfg.cron', before: '1', after: '2' }])
assert.deepEqual(auditDiff({ list: [1, 2] }, { list: [1, 2] }), [])
assert.deepEqual(auditDiff({ list: [1] }, { list: [1, 2] }), [{ key: 'list', before: [1], after: [1, 2] }])
console.log('audit diff ok')
