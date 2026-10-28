// The copy button of a code block in rendered markdown (useMarkdown): one
// listener for the whole page; the button shows a check for a moment.
export default defineNuxtPlugin(() => {
  document.addEventListener('click', async (e) => {
    const btn = (e.target as HTMLElement | null)?.closest<HTMLElement>('[data-md-act="copy"]')
    const code = btn?.closest('.md-code')?.querySelector('code')
    if (!btn || !code) return
    // copyText falls back to execCommand over plain HTTP (no navigator.clipboard)
    if (!await copyText(code.textContent ?? '')) return
    btn.classList.add('md-copied')
    setTimeout(() => btn.classList.remove('md-copied'), 1500)
  })
})
