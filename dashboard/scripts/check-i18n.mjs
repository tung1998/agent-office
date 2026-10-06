// Fails when Vietnamese text is written directly in the dashboard instead of
// app/locales (so the English UI never shows untranslated strings).
// A line may opt out with a trailing "i18n-ignore" comment.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

const VI = /[àáạảãâầấậẩẫăằắặẳẵèéẹẻẽêềếệểễìíịỉĩòóọỏõôồốộổỗơờớợởỡùúụủũưừứựửữỳýỵỷỹđ]/i
const root = new URL('../app', import.meta.url).pathname
const bad = []

function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      if (name !== 'locales') walk(p)
    } else if (/\.(vue|ts)$/.test(name)) {
      readFileSync(p, 'utf8').split('\n').forEach((line, i) => {
        const code = line.replace(/\/\/.*$/, '').replace(/<!--.*?-->/g, '')
        if (VI.test(code) && !line.includes('i18n-ignore')) bad.push(`${p.slice(root.length + 1)}:${i + 1}: ${line.trim().slice(0, 100)}`)
      })
    }
  }
}
walk(root)
if (bad.length) {
  console.error(`i18n: ${bad.length} dòng còn chữ tiếng Việt viết thẳng (chuyển vào app/locales):\n` + bad.join('\n'))
  process.exit(1)
}
console.log('i18n: ok')
