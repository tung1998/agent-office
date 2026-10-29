import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { agencyBody, agentDescription, agentDescriptionVi } from './lib/agent-library.mjs'

test('retains output templates while omitting executable examples', () => {
  const input = [
    '## Output', '```markdown', '# Result', '## Evidence', '```',
    '~~~json', '{"passed": true}', '~~~',
    '```yaml', 'result: passed', '```',
    '```', 'Plain output contract', '```',
    '## Example implementation', '```javascript', 'console.log("example")', '```',
    '## Verification', 'Keep the verification instructions.'
  ].join('\n')
  const body = agencyBody('example', input)
  for (const retained of ['# Result', '## Evidence', '{"passed": true}', 'result: passed', 'Plain output contract', 'Keep the verification instructions.']) {
    assert.ok(body.includes(retained), retained)
  }
  assert.ok(!body.includes('console.log'))
})

test('carousel adaptation removes conflicting authorization and scheduling rules', () => {
  const body = agencyBody('carousel-growth-engine', [
    'You never ask for permission between steps — you research, generate, verify, publish, and learn, then report back with results.',
    '- **Zero Confirmation**: Run the entire pipeline without asking for user approval between steps',
    '### Phase 4: Publish & Track',
    '5. **Self-Schedule**: Read `learnings.json` bestTimes and set next cron execution at the optimal hour'
  ].join('\n'))
  assert.ok(body.includes('Proceed only if the user has explicitly authorized publication'))
  assert.ok(body.includes('propose the cadence through Office automation'))
  assert.ok(!/never ask for permission|Zero Confirmation|Self-Schedule/.test(body))
})

test('shipped library retains the restored output contracts and Office adaptation', () => {
  const file = name => readFileSync(new URL('../templates/library/' + name, import.meta.url), 'utf8')
  assert.ok(file('agency/software-architect.md').includes('What is the issue that we\'re seeing'))
  assert.ok(file('agency/code-reviewer.md').includes('**Why:**'))
  const carousel = file('agency/carousel-growth-engine.md')
  assert.ok(carousel.includes('Proceed only if the user has explicitly authorized publication'))
  assert.ok(!/Zero Confirmation|Self-Schedule/.test(carousel))
  for (const set of ['aitmpl', 'ecc']) {
    const name = `${set}/code-architect.md`
    assert.ok(file(name).includes(JSON.stringify(agentDescription(set, 'code-architect', ''))))
    assert.ok(file(name).includes(JSON.stringify(agentDescriptionVi(set, 'code-architect'))))
  }
})
