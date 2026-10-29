import DOMPurify from 'dompurify'
import { marked } from 'marked'

marked.setOptions({ gfm: true, breaks: true })

/**
 * Render agent Markdown to safe HTML. Diff blocks are replaced by a note
 * because proposed changes are shown as their own cards with approve buttons.
 */
// the HTML of the latest texts: a chat re-renders every message on each
// streamed token, and parsing + sanitizing them all again each time is heavy
const cache = new Map<string, string>()
const MAX = 300

// a code block gets a bar: copy (plugins/md-copy), and for a form's draft
// (```workflow, ```skill, ```automation) apply, shown where a chat can fill
// that form (ChatPanel's md-apply-<lang> class) and handled there
const APPLY = new Set(['workflow', 'skill', 'automation'])
const COPY_ICON = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>'
function codeBars(html: string, copy: string, apply: string): string {
  return html.replace(/<pre>(<code(?: class="language-([\w-]+)")?>)/g, (_, code: string, lang?: string) => {
    const ap = lang && APPLY.has(lang) ? `<button type="button" class="md-apply" data-md-act="apply" data-lang="${lang}">${apply}</button>` : ''
    return `<div class="md-code"><div class="md-code-bar">${lang ? `<span class="md-lang">${lang}</span>` : ''}${ap}<button type="button" class="md-copy" data-md-act="copy" title="${copy}" aria-label="${copy}">${COPY_ICON}</button></div><pre>${code}`
  }).replace(/<\/pre>/g, '</pre></div>')
}

// a project file the text names (`internal/x.go:12`, or a relative link to it)
// opens in the Files tab (plugins/md-file): marked here, the click goes there
const FILE_REF = /^(?:\.\/)?[\w@-][\w.@-]*(?:\/[\w.@[\]-]+)*\.[A-Za-z][\w-]{0,9}(?::\d+(?:-\d+)?)?$/
const fileAttrs = (path: string) => `data-md-act="file" data-path="${path.replace(/^\.\//, '').replace(/:\d+(?:-\d+)?$/, '')}"`
function fileRefs(html: string): string {
  return html
    .replace(/(?<!<pre>)<code>([^<]+)<\/code>/g, (m, p: string) => FILE_REF.test(p) && p.includes('/') ? `<code class="md-file" ${fileAttrs(p)}>${p}</code>` : m)
    .replace(/<a href="([^"]+)"/g, (m, h: string) => FILE_REF.test(h) ? `<a href="${h}" ${fileAttrs(h)}` : m)
}

export function renderMarkdown(text: string, hideDiffs = true): string {
  const { t, lang } = useLang()
  const key = lang.value + (hideDiffs ? '1' : '0') + text
  const hit = cache.get(key)
  if (hit !== undefined) return hit
  let src = text
  if (hideDiffs) {
    src = src.replace(/```(?:diff|patch)[ \t]*\n[\s\S]*?```/g, t('md.diffHidden'))
  }
  const html = fileRefs(codeBars(DOMPurify.sanitize(marked.parse(src, { async: false }) as string), t('md.copy'), t('md.apply')))
  if (cache.size >= MAX) cache.delete(cache.keys().next().value!)
  cache.set(key, html)
  return html
}
