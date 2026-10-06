// Every page except /login needs a session. Pages with meta.admin need role admin.
export default defineNuxtRouteMiddleware(async (to) => {
  const { user, loaded, fetchMe } = useAuth()
  if (!loaded.value) {
    await fetchMe()
  }
  if (to.path === '/login') {
    return user.value ? navigateTo(safeRedirect(to.query.redirect)) : undefined
  }
  if (!user.value) {
    return navigateTo({ path: '/login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} })
  }
  // the default admin (admin / admin) sets its real email and password first
  if (user.value.must_change) {
    return to.path === '/setup-account' ? undefined : navigateTo('/setup-account')
  }
  if (to.path === '/setup-account') {
    return navigateTo('/')
  }
  if (to.meta.admin && user.value.role !== 'admin') {
    return navigateTo('/')
  }
})

// Only same-app paths: "/x" is fine, "//evil.com" or "https://..." is not.
function safeRedirect(r: unknown): string {
  return typeof r === 'string' && r.startsWith('/') && !r.startsWith('//') ? r : '/'
}
