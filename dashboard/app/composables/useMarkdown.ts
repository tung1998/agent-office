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

export function renderMarkdown(text: string, hideDiffs = true): string {
  const { t, lang } = useLang()
  const key = lang.value + (hideDiffs ? '1' : '0') + text
  const hit = cache.get(key)
  if (hit !== undefined) return hit
  let src = text
  if (hideDiffs) {
    src = src.replace(/```(?:diff|patch)[ \t]*\n[\s\S]*?```/g, t('md.diffHidden'))
  }
  const html = DOMPurify.sanitize(marked.parse(src, { async: false }) as string)
  if (cache.size >= MAX) cache.delete(cache.keys().next().value!)
  cache.set(key, html)
  return html
}
