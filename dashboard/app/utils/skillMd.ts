// SKILL.md as office's skill editor reads and writes it: the name and the
// description of the frontmatter, the body; every other frontmatter line is
// kept as it was, in its place (review I6: a save never breaks the YAML).

interface Entry { key: string, lines: string[] } // one frontmatter key with its continuation lines
export interface SkillMd { name: string, description: string, body: string, entries: Entry[] }

// a YAML scalar as written after "key:" (plain, quoted, or a | / > block)
function readScalar(first: string, more: string[]): string {
  const v = first.trim()
  if (/^[|>][-+]?$/.test(v)) {
    const lines = more.map(l => l.replace(/^\s+/, ''))
    return v.startsWith('|') ? lines.join('\n').trim() : lines.join(' ').replace(/\s+/g, ' ').trim()
  }
  if (v.startsWith('"')) {
    try {
      return JSON.parse(v) as string
    } catch { return v.slice(1, -1) }
  }
  if (v.startsWith('\'')) return v.slice(1, -1).replace(/''/g, '\'')
  return v
}

// a value as a YAML scalar: plain when that is safe, else double quoted
function writeScalar(v: string): string {
  const s = v.trim()
  return s === '' || /^[\s#&*!|>'"%@`{[\]?,-]|: | #|\n|\s$/.test(s) ? JSON.stringify(s) : s
}

export function parseSkillMd(md: string): SkillMd {
  const text = md.replace(/\r\n/g, '\n')
  const m = text.match(/^---\n([\s\S]*?)\n---\n?([\s\S]*)$/)
  if (!m) return { name: '', description: '', body: text.trim(), entries: [] }
  const entries: Entry[] = []
  for (const line of m[1]!.split('\n')) {
    const key = line.match(/^([A-Za-z0-9_-]+):/)
    if (key || !entries.length) entries.push({ key: key?.[1] ?? '', lines: [line] })
    else entries[entries.length - 1]!.lines.push(line) // an indented continuation, or a blank inside a block
  }
  const value = (k: string) => {
    const e = entries.find(x => x.key === k)
    return e ? readScalar(e.lines[0]!.slice(k.length + 1), e.lines.slice(1)) : ''
  }
  return { name: value('name'), description: value('description'), body: m[2]!.trim(), entries }
}

export function writeSkillMd(s: Pick<SkillMd, 'name' | 'description' | 'body'> & { entries?: Entry[] }): string {
  const entries = [...(s.entries ?? [])]
  const set = (k: string, v: string, at: number) => {
    const line = `${k}: ${writeScalar(v)}`
    const i = entries.findIndex(e => e.key === k)
    if (i >= 0) entries[i] = { key: k, lines: [line] }
    else entries.splice(at, 0, { key: k, lines: [line] })
  }
  set('name', s.name, 0)
  set('description', s.description, 1)
  return ['---', ...entries.flatMap(e => e.lines), '---', '', s.body.trim(), ''].join('\n')
}
