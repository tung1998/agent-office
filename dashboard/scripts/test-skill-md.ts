import assert from 'node:assert/strict'
import { parseSkillMd, writeSkillMd } from '../app/utils/skillMd.ts'

// a plain skill round-trips unchanged
{
  const md = '---\nname: tra-don\ndescription: Tra đơn\n---\n\nBody.\n'
  const p = parseSkillMd(md)
  assert.equal(p.name, 'tra-don')
  assert.equal(p.description, 'Tra đơn')
  assert.equal(p.body, 'Body.')
  assert.equal(writeSkillMd(p), md)
}
// review I6: a block description (| or >), other keys in their order
{
  const md = '---\nname: x\ndescription: >\n  Dòng một\n  dòng hai\nallowed-tools: Read, Grep\nmodel: sonnet\n---\n# B\n'
  const p = parseSkillMd(md)
  assert.equal(p.description, 'Dòng một dòng hai')
  p.description = 'Mới'
  const out = writeSkillMd(p)
  assert.equal(out, '---\nname: x\ndescription: Mới\nallowed-tools: Read, Grep\nmodel: sonnet\n---\n\n# B\n')
}
// CRLF files are read (no second frontmatter)
{
  const p = parseSkillMd('---\r\nname: y\r\ndescription: Z\r\n---\r\nBody\r\n')
  assert.equal(p.name, 'y')
  assert.equal(p.description, 'Z')
  assert.equal(p.body, 'Body')
}
// quoted values: unescaped once, saved stable (no growing escapes)
{
  const md = '---\nname: q\ndescription: "Nói \\"xin chào\\": lịch sự"\n---\nB\n'
  const p = parseSkillMd(md)
  assert.equal(p.description, 'Nói "xin chào": lịch sự')
  const once = writeSkillMd(p)
  assert.equal(writeSkillMd(parseSkillMd(once)), once)
  assert.equal(parseSkillMd(once).description, 'Nói "xin chào": lịch sự')
}
// no frontmatter: the whole file is the body
{
  const p = parseSkillMd('# Chỉ nội dung\n')
  assert.equal(p.body, '# Chỉ nội dung')
  assert.ok(writeSkillMd({ ...p, name: 'n', description: 'd' }).startsWith('---\nname: n\ndescription: d\n---\n'))
}
console.log('skillMd: ok')
