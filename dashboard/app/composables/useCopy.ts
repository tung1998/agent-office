// Copying text (a link, an id, a login code) from the dashboard.
//
// navigator.clipboard only exists in a secure context: served over plain HTTP
// from an IP (an office on the LAN, a Tailscale address) every browser leaves
// it undefined, so the modern call alone silently copies nothing. The old
// execCommand path still works there and is the fallback.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch { /* blocked or denied: try the fallback below */ }
  const back = document.activeElement as HTMLElement | null
  const ta = document.createElement('textarea')
  try {
    ta.value = text
    ta.setAttribute('readonly', '')
    // off-screen, but focusable: iOS needs it in the document and visible-ish
    ta.style.cssText = 'position:fixed;top:0;left:-9999px;opacity:0'
    document.body.appendChild(ta)
    // focused, or a menu's focus trap keeps the focus and the selection is lost
    ta.focus({ preventScroll: true })
    ta.select()
    ta.setSelectionRange(0, ta.value.length)
    // Chrome answers true even with nothing selected: check what is selected
    const selected = ta.selectionEnd - ta.selectionStart === ta.value.length
    return document.execCommand('copy') && selected && document.activeElement === ta
  } catch {
    return false
  } finally {
    ta.remove()
    back?.focus?.({ preventScroll: true })
  }
}

// Copy text with a toast. When nothing could be copied the text itself is
// shown, so it can be selected by hand instead of a false "copied".
export function useCopy() {
  const toast = useToast()
  const { t } = useLang()
  return async (text: string) => {
    // called from a menu item: let the menu close and give the focus back first
    await new Promise(r => setTimeout(r, 0))
    if (await copyText(text)) toast.add({ title: t('common.copied'), color: 'success' })
    else toast.add({ title: text, color: 'neutral', duration: 0 })
  }
}
