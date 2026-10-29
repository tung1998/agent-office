// A project file named in rendered markdown (useMarkdown's data-md-act="file")
// opens in the project's Files tab; the project is the chat's (data-project)
// or the page's.
export default defineNuxtPlugin(() => {
  document.addEventListener('click', (e) => {
    const el = (e.target as HTMLElement | null)?.closest<HTMLElement>('[data-md-act="file"]')
    if (!el?.dataset.path) return
    const route = useRoute()
    const project = el.closest<HTMLElement>('[data-project]')?.dataset.project
      ?? (route.path.startsWith('/projects/') ? String(route.params.id ?? '') : '')
    if (!project) return
    e.preventDefault()
    navigateTo({ path: `/projects/${project}`, query: { tab: 'files', file: el.dataset.path } })
  })
})
