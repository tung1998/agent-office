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
// opens in the Files tab (plugins/md-file): marked here, the click goes there.
// An audio file plays right there: a player under the name, a project file
// through the Files tab's preview (needs the project), a URL straight
const FILE_REF = /^(?:\.\/)?[\w@-][\w.@-]*(?:\/[\w.@[\]-]+)*\.[A-Za-z][\w-]{0,9}(?::\d+(?:-\d+)?)?$/
const AUDIO = /\.(?:mp3|wav|ogg|oga|m4a|aac|flac|opus)(?=$|[?#&])/i
const filePath = (p: string) => p.replace(/^\.\//, '').replace(/:\d+(?:-\d+)?$/, '')
const fileAttrs = (path: string) => `data-md-act="file" data-path="${filePath(path)}"`
const player = (src: string) => `<audio class="md-audio" controls preload="metadata" src="${src}"></audio>`
const previewSrc = (project: string, path: string) => `/api/projects/${encodeURIComponent(project)}/file/preview?path=${encodeURIComponent(filePath(path))}`
function fileRefs(html: string, project: string): string {
  return html
    .replace(/(?<!<pre>)<code>([^<]+)<\/code>/g, (m, p: string) => {
      if (!FILE_REF.test(p)) return m
      const audio = project && AUDIO.test(p)
      if (!audio && !p.includes('/')) return m
      return `<code class="md-file" ${fileAttrs(p)}>${p}</code>${audio ? player(previewSrc(project, p)) : ''}`
    })
    .replace(/<a href="([^"]+)"([^>]*)>([\s\S]*?)<\/a>/g, (m, h: string, rest: string, body: string) => {
      const file = FILE_REF.test(h)
      const audio = AUDIO.test(h) ? (file ? project && previewSrc(project, h) : /^(?:https?:\/\/|\/api\/)/.test(h) && h) : ''
      if (!file && !audio) return m
      return `<a href="${h}"${file ? ' ' + fileAttrs(h) : ''}${rest}>${body}</a>${audio ? player(audio) : ''}`
    })
}

// project: the chat's project, so its audio files play in place
export function renderMarkdown(text: string, hideDiffs = true, project = ''): string {
  const { t, lang } = useLang()
  const key = lang.value + (hideDiffs ? '1' : '0') + project + '\n' + text
  const hit = cache.get(key)
  if (hit !== undefined) return hit
  let src = text
  if (hideDiffs) {
    src = src.replace(/```(?:diff|patch)[ \t]*\n[\s\S]*?```/g, t('md.diffHidden'))
  }
  const html = fileRefs(codeBars(DOMPurify.sanitize(marked.parse(src, { async: false }) as string), t('md.copy'), t('md.apply')), project)
  if (cache.size >= MAX) cache.delete(cache.keys().next().value!)
  cache.set(key, html)
  return html
}
