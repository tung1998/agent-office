export interface OfficeUser {
  id: string
  email: string
  name: string
  role: 'admin' | 'manager' | 'member' // manager: a project admin (ADR-224)
  projects?: string[] // the projects a manager or member sees
  disabled: boolean
  must_change?: boolean // the default admin, until it sets a real email and password
  discord_id?: string // its own ids on the bots' platforms: a message to a bot from one is shown as this account's
  telegram_id?: string
  created_at: string
  last_login_at: string | null
}

/** The office build that is running (from /api/auth/me). */
export interface OfficeBuild {
  version: string
  revision?: string
  time?: string
  dirty: boolean
  subject?: string
}

/** Error message from the Go API ({ error: "..." }) or a generic fallback. */
export function apiError(e: unknown, fallback?: string): string {
  const data = (e as { data?: { error?: string } })?.data
  return data?.error ?? fallback ?? useLang().t('pages.genericError')
}

export function useAuth() {
  const user = useState<OfficeUser | null>('auth:user', () => null)
  const loaded = useState('auth:loaded', () => false)
  const build = useState<OfficeBuild | null>('auth:build', () => null)

  async function fetchMe() {
    try {
      const res = await $fetch<{ user: OfficeUser, build?: OfficeBuild }>('/api/auth/me')
      user.value = res.user
      build.value = res.build ?? null
    } catch {
      user.value = null
    } finally {
      loaded.value = true
    }
    return user.value
  }

  async function login(email: string, password: string) {
    const res = await $fetch<{ user: OfficeUser }>('/api/auth/login', {
      method: 'POST',
      body: { email, password }
    })
    user.value = res.user
    loaded.value = true
  }

  async function loginPasskey() {
    const res = await usePasskey().login()
    user.value = res.user
    loaded.value = true
  }

  async function logout() {
    await $fetch('/api/auth/logout', { method: 'POST', body: {} }).catch(() => {})
    stopLive()
    user.value = null
    await navigateTo('/login')
  }

  // isAdmin: may manage what the page shows: an admin anywhere, a manager
  // on a page of one of its projects (ADR-224). isOfficeAdmin: the whole
  // office's settings (users, AI connections, MCP…).
  const route = useRoute()
  const isOfficeAdmin = computed(() => user.value?.role === 'admin')
  function manages(projectId: string) {
    const u = user.value
    return u?.role === 'admin' || (u?.role === 'manager' && !!u.projects?.includes(projectId))
  }
  const isAdmin = computed(() => isOfficeAdmin.value || (route.path.startsWith('/projects/') && manages(String(route.params.id ?? ''))))
  const canCreateProject = computed(() => user.value?.role === 'admin' || user.value?.role === 'manager')

  return { user, loaded, build, isAdmin, isOfficeAdmin, canCreateProject, manages, fetchMe, login, loginPasskey, logout }
}
