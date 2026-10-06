// Tests for mergeDraft (ADR-042): run with node --experimental-strip-types.
import assert from 'node:assert/strict'
import { automationBody, draftFrom, emptyDraft, mergeDraft } from '../app/composables/useAutomations.ts'

// nested fields: known keys of the right type only
{
  const d = emptyDraft()
  const changed = mergeDraft(d, { script: { body: null, language: 'bash', lang: 'ruby' }, limits: { max_runs_per_hour: '5' }, escalate: { when: 'sometimes' } })
  assert.equal(d.script.body, '', 'null body ignored')
  assert.equal(d.script.lang, 'bash', 'unknown language ignored')
  assert.equal('language' in d.script, false, 'unknown key ignored')
  assert.equal(d.limits.max_runs_per_hour, 0, 'string for a number ignored')
  assert.equal(d.escalate.when, 'never', 'unknown enum ignored')
  assert.deepEqual(changed, [], 'nothing changed')
}
// a good patch applies
{
  const d = emptyDraft()
  const changed = mergeDraft(d, { name: 'Đếm lỗi', script: { lang: 'python', body: 'print(1)' }, escalate: { when: 'signal' } })
  assert.equal(d.name, 'Đếm lỗi')
  assert.equal(d.script.lang, 'python')
  assert.equal(d.escalate.when, 'never', 'a script calls no agent in (ADR-057)')
  assert.deepEqual(changed.sort(), ['name', 'script'])
}
// cron and every_minutes replace each other
{
  const d = emptyDraft()
  mergeDraft(d, { config: { every_minutes: 15 } })
  assert.equal(d.config.every_minutes, 15)
  assert.equal(d.config.cron, '', 'every N minutes clears the cron')
  mergeDraft(d, { config: { cron: '0 9 * * *' } })
  assert.equal(d.config.cron, '0 9 * * *')
  assert.equal(d.config.every_minutes, 0, 'a cron clears every N minutes')
}
// Giao Việc is gone (ADR-057): a schedule sends to an agent; an old task opens as that
{
  const d = emptyDraft()
  mergeDraft(d, { action: 'chat' })
  assert.equal(d.action, 'chat', 'a schedule may send to an agent')
  const old = { ...emptyDraft(), id: 'aut_1', project_id: 'p', action: 'task', agent_id: 'agt_1' }
  const conv = draftFrom(old as never)
  assert.equal(conv.action, 'chat', 'an old task automation opens as sending to its agent')
  assert.equal(conv.agent_id, 'agt_1', 'the same agent')
}
// a bot's messages (ADR-049): the agent replies in the chat; the token is never filled from the chat
{
  const d = emptyDraft()
  mergeDraft(d, { source: 'discord', action: 'chat', config: { keywords: ['mã đơn'], scope: 'đơn hàng' }, bot: { token: 'leaked', allow: ['42'], refusal: 'Chỉ hỗ trợ đơn hàng' } })
  assert.equal(d.source, 'discord')
  assert.equal(d.action, 'chat', 'a bot rule may reply in the chat')
  assert.deepEqual(d.config.keywords, ['mã đơn'])
  assert.deepEqual(d.bot.allow, ['42'])
  assert.equal(d.bot.token, '', 'the token never comes from the chat')
  const body = automationBody({ ...d, bot: { ...d.bot, token: 'pasted' } })
  assert.equal(body.bot?.token, 'pasted', 'a pasted token is sent')
  assert.equal(automationBody({ ...emptyDraft(), bot: { token: 'x', allow: [], refusal: '' } }).bot, undefined, 'no bot for a schedule')
}
console.log('mergeDraft: ok')
