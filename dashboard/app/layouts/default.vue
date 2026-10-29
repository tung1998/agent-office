<script setup lang="ts">
import type { DropdownMenuItem, NavigationMenuItem } from '@nuxt/ui'

const route = useRoute()
const { user, build, viewedBy, isOfficeAdmin, canCreateProject, manages, logout, endViewAs } = useAuth()
const { t, when: fmtWhen } = useLang()

// which office is running, above the user (from /api/auth/me)
const buildLabel = computed(() => {
  const b = build.value
  if (!b) return ''
  return b.revision ? `${b.version} · ${b.revision.slice(0, 7)}` : b.version
})
const buildText = computed(() => build.value?.dirty ? `${buildLabel.value} · ${t('build.dirty')}` : buildLabel.value)
const buildTime = computed(() => {
  const at = build.value?.time
  return at ? t('build.time', { time: fmtWhen(at) }) : ''
})
const buildInfoOpen = ref(false)
// a dot when the source moved past the running build (admins, once per load)
const hasUpdate = ref(false)
watch(isOfficeAdmin, async (admin) => {
  if (!admin || !import.meta.client) return
  try {
    hasUpdate.value = (await $fetch<{ changed: boolean }>('/api/system/update/changes')).changed
  } catch { /* no source to compare with */ }
}, { immediate: true })

const bareLayout = computed(() => route.path === '/login' || route.path === '/setup-account')

// sidebar: the projects of the account's menu (useProjectMenu: how many, in
// which order, hidden ones out), each with its sections (refetched on
// navigation and on a change, so added/renamed projects show up)
const projectList = ref<Project[]>([])
const { menu, single, showsGroup } = useProjectMenu()
const recentProjects = computed(() => menu(projectList.value, route.params.id as string | undefined))
async function loadProjects() {
  if (bareLayout.value) return
  try {
    projectList.value = (await $fetch<{ projects: Project[] }>('/api/projects')).projects
  } catch { /* signed out: the auth middleware redirects */ }
}
watch(() => route.path, loadProjects, { immediate: true })

// which projects show their sections: opening one keeps the others open, only
// a project's own arrow closes it, unless the account chose one at a time
// (kept in this browser across reloads)
const OPEN_KEY = 'office:nav-open'
const LIBRARY_PATHS = ['/agents', '/skills', '/mcp', '/automations/templates', '/packs', '/workflows']
const navOpen = ref<string[]>([])
onMounted(() => {
  try {
    navOpen.value = JSON.parse(localStorage.getItem(OPEN_KEY) ?? '[]')
  } catch { /* broken or private mode: start closed */ }
  watch([() => route.params.id as string | undefined, single], ([id, one]) => {
    if (!id) return
    const key = `project-${id}`
    if (one) navOpen.value = [...navOpen.value.filter(x => !x.startsWith('project-')), key]
    else if (!navOpen.value.includes(key)) navOpen.value = [...navOpen.value, key]
  }, { immediate: true })
  watch(() => route.path === '/watch', (on) => {
    if (on && !navOpen.value.includes('watch')) navOpen.value = [...navOpen.value, 'watch']
  }, { immediate: true })
  // a library page opens the library entry, to show where it is
  watch(() => LIBRARY_PATHS.some(p => route.path.startsWith(p)), (on) => {
    if (on && !navOpen.value.includes('library')) navOpen.value = [...navOpen.value, 'library']
  }, { immediate: true })
  watch(navOpen, (v, old) => {
    const added = v.filter(x => x.startsWith('project-') && !old?.includes(x)).at(-1)
    if (single.value && added && v.some(x => x.startsWith('project-') && x !== added)) {
      navOpen.value = v.filter(x => !x.startsWith('project-') || x === added)
      return
    }
    try {
      localStorage.setItem(OPEN_KEY, JSON.stringify(v))
    } catch { /* private mode */ }
  })
})
useLive(['repos', 'agents'], loadProjects) // added, renamed, removed: without leaving the page
// what changes elsewhere shows up here (ADR-072): opened once signed in —
// also right after the login page, where this layout stays mounted
onMounted(() => {
  watch(() => !bareLayout.value && !!user.value, (on) => { if (on) startLive() }, { immediate: true })
})

// pages below a project (/projects/:id/<child>/…) belong to one of its sections
const childSection: Record<string, string> = { agents: 'agents', automations: 'automations', workflows: 'workflows' }

// Vận hành: hidden with the ops feature off; Khách hàng only with the support pack on
function hasOps(id: string) {
  const p = projectList.value.find(x => x.id === id)
  return hasFeature(p, 'ops')
}
function hasSupport(id: string) {
  return hasFeature(projectList.value.find(x => x.id === id), 'support')
}

function projectSections(id: string): NavigationMenuItem[] {
  const to = (tab: string) => ({ path: `/projects/${id}`, query: { tab } })
  const child = route.path.match(new RegExp(`^/projects/${id}/([^/]+)/`))?.[1]
  const parent = child ? childSection[child] : undefined
  return withParent(parent, [
    { label: t('nav.chat'), icon: 'i-lucide-messages-square', to: to('chat'), exactQuery: 'partial' },
    // Việc has no tab (ADR-056): the team works through the chat; a task's page stays for its links
    ...(hasSupport(id) ? [{ label: t('nav.support'), icon: 'i-lucide-headset', to: to('support'), exactQuery: 'partial' as const }] : []),
    { label: t('nav.automations'), icon: 'i-lucide-alarm-clock', to: to('automations'), exactQuery: 'partial' },
    ...(hasOps(id) ? [{ label: t('nav.ops'), icon: 'i-lucide-activity', to: to('ops'), exactQuery: 'partial' as const }] : []),
    { label: t('project.sectionModel'), icon: 'i-lucide-users', to: to('agents'), exactQuery: 'partial' },
    { label: t('wf.section'), icon: 'i-lucide-workflow', to: to('workflows'), exactQuery: 'partial' },
    { label: t('project.sectionPerm'), icon: 'i-lucide-shield', to: to('perm'), exactQuery: 'partial' },
    ...(manages(id)
      ? [
          { label: t('files.section'), icon: 'i-lucide-folder-tree', to: to('files'), exactQuery: 'partial' as const },
          // skills and MCP install through the office's own settings (ADR-224)
          ...(isOfficeAdmin.value
            ? [
                { label: t('project.sectionSkill'), icon: 'i-lucide-sparkles', to: to('skill'), exactQuery: 'partial' as const },
                { label: t('project.sectionMcp'), icon: 'i-lucide-plug-zap', to: to('mcp'), exactQuery: 'partial' as const }
              ]
            : []),
          { label: t('project.sectionBurn'), icon: 'i-lucide-list-checks', to: to('burn'), exactQuery: 'partial' as const }, // the work board; Burn runs it (ADR-210)
          { label: t('nav.log'), icon: 'i-lucide-scroll-text', to: to('log'), exactQuery: 'partial' as const }
        ]
      : []),
    { label: t('project.sectionInfo'), icon: 'i-lucide-info', to: to('info'), exactQuery: 'partial' }
  ])
}

// marks the section a child page belongs to as the active one
function withParent(parent: string | undefined, items: NavigationMenuItem[]): NavigationMenuItem[] {
  if (!parent) return items
  return items.map(x => (x.to as { query?: { tab?: string } })?.query?.tab === parent ? { ...x, active: true } : x)
}

// what needs a person across the office: its count on "Sự cố" (pushed as it changes)
const { data: incData } = useLiveFetch<{ count: number }>('/api/incidents', { key: 'incidents', lazy: true })
const attention = computed(() => incData.value?.count ?? 0)
// connections failing (signed out, their CLI stuck): marked on "Kết nối AI", seen from any page
const { data: provData } = useLiveFetch<{ providers: Provider[] }>('/api/providers', { lazy: true })
const brokenProviders = computed(() => provData.value?.providers.filter(p => p.enabled && p.status === 'error').length ?? 0)

// the watch dashboards sit under "Theo dõi" (at most 5, ADR-159), + for a new one
const watchLayout = useWatchLayout()
const boardLabel = (b: { name: string }, i: number) => b.name || t('watchW.board.default', { n: i + 1 })
const watchBoards = computed<NavigationMenuItem[]>(() => [
  ...watchLayout.boards.value.map((b, i) => ({
    label: boardLabel(b, i),
    icon: 'i-lucide-layout-dashboard',
    active: route.path === '/watch' && b.id === watchLayout.board.value?.id,
    onSelect: () => { watchLayout.selectBoard(b.id); navigateTo('/watch') }
  })),
  ...(watchLayout.canAddBoard.value
    ? [{ label: t('watchW.board.add'), icon: 'i-lucide-plus', onSelect: () => { watchLayout.addBoard(); navigateTo('/watch') } }]
    : [])
])

const overviewTab = (tab: string) => ({ path: '/', query: { tab } })

const items = computed<NavigationMenuItem[][]>(() => {
  // what projects pick from: agents (ADR-153), skills, MCP servers, workflows;
  // one entry of the work group, its pages under it
  const library: NavigationMenuItem = {
    value: 'library',
    label: t('nav.library'),
    icon: 'i-lucide-library',
    children: [
      { label: t('nav.agents'), icon: 'i-lucide-bot', to: '/agents' },
      ...(isOfficeAdmin.value
        ? [
            { label: t('tools.skillsTab'), icon: 'i-lucide-sparkles', to: '/skills' },
            { label: t('tools.mcpTab'), icon: 'i-lucide-plug-zap', to: '/mcp' },
            { label: t('nav.automationTemplates'), icon: 'i-lucide-zap', to: '/automations/templates' },
            { label: t('packs.title'), icon: 'i-lucide-building-2', to: '/packs' },
            { label: t('wf.section'), icon: 'i-lucide-workflow', to: '/workflows' }
          ]
        : [])
    ]
  }
  // office-wide pages: the day's work, then how office is set up
  const office: NavigationMenuItem[] = [
    { label: t('nav.work'), type: 'label' },
    // everyone's: it reaches the projects of the person asking (ADR-240)
    { label: t('assistant.title'), icon: 'i-lucide-sparkles', to: '/assistant' },
    ...(showsGroup('library') ? [library] : []),
    // one place to follow the office: what needs a person is counted here; its tabs under it
    {
      value: 'overview', label: t('nav.overview'), icon: 'i-lucide-layout-dashboard', to: '/', badge: attention.value || undefined,
      children: [
        { label: t('home.tabWork'), icon: 'i-lucide-list-checks', to: overviewTab('work'), exactQuery: 'partial' as const },
        { label: t('home.tabStats'), icon: 'i-lucide-chart-column', to: overviewTab('stats'), exactQuery: 'partial' as const },
        ...(isOfficeAdmin.value
          ? [
              { label: t('home.tabMachine'), icon: 'i-lucide-cpu', to: overviewTab('machine'), exactQuery: 'partial' as const },
              { label: t('nav.data'), icon: 'i-lucide-database', to: overviewTab('data'), exactQuery: 'partial' as const }
            ]
          : [])
      ]
    },
    { value: 'watch', label: t('watch.title'), icon: 'i-lucide-layout-grid', to: '/watch', children: watchBoards.value },
    // the office's AI connections: an admin's
    ...(isOfficeAdmin.value
      ? [{
          label: t('nav.providers'), icon: 'i-lucide-plug', to: '/providers',
          badge: brokenProviders.value ? { label: String(brokenProviders.value), color: 'error' as const, variant: 'solid' as const } : undefined
        }]
      : [])
  ]
  const admin: NavigationMenuItem[] = isOfficeAdmin.value
    ? [
        { label: t('nav.admin'), type: 'label' },
        { label: t('nav.users'), icon: 'i-lucide-users', to: '/admin/users' },
        { label: t('nav.peers'), icon: 'i-lucide-building-2', to: '/admin/peers' },
        { label: t('nav.update'), icon: 'i-lucide-package', to: '/admin/update' }
      ]
    : canCreateProject.value // a manager: its members (ADR-239)
      ? [{ label: t('nav.admin'), type: 'label' }, { label: t('nav.users'), icon: 'i-lucide-users', to: '/admin/users' }]
      : []
  // projects: a title, the most used ones, then a link to all of them
  const projects: NavigationMenuItem[] = [
    { label: t('nav.projects'), type: 'label' },
    ...recentProjects.value.map(p => ({
      value: `project-${p.id}`, // open state follows the project, not its position
      label: p.name,
      icon: p.scope === 'machine' ? 'i-lucide-monitor' : 'i-lucide-folder',
      to: { path: `/projects/${p.id}`, query: { tab: 'chat' } },
      children: projectSections(p.id)
    })),
    projectList.value.length
      ? { label: t('nav.allProjects'), icon: 'i-lucide-list', to: '/projects', exact: true }
      : { label: t('nav.addProject'), icon: 'i-lucide-plus', to: '/projects', exact: true }
  ]
  // groups the account turned off in Settings stay reachable by their URL
  return [projects, showsGroup('work') ? office : [], showsGroup('admin') ? admin : []]
})

const settingsOpen = ref(false)
const userMenu = computed<DropdownMenuItem[][]>(() => [
  [{ label: user.value?.email ?? '', type: 'label' }],
  ...(viewedBy.value ? [[{ label: t('user.viewingAsEnd'), icon: 'i-lucide-undo-2', onSelect: () => endViewAs() }]] : []),
  [{ label: t('settings.title'), icon: 'i-lucide-settings', onSelect: () => { settingsOpen.value = true } }],
  [{ label: t('user.changePassword'), icon: 'i-lucide-key-round', to: '/account' }],
  // the office's records and backups: an admin's, from its own menu
  ...(isOfficeAdmin.value
    ? [[
        { label: t('nav.audit'), icon: 'i-lucide-scroll-text', to: '/admin/audit' },
        { label: t('nav.transfer'), icon: 'i-lucide-archive-restore', to: '/admin/transfer' }
      ]]
    : []),
  [{ label: t('user.logout'), icon: 'i-lucide-log-out', color: 'error', onSelect: () => logout() }]
])
</script>

<template>
  <div v-if="bareLayout" class="min-h-screen">
    <slot />
  </div>

  <UDashboardGroup v-else>
    <UDashboardSidebar collapsible resizable :default-size="18">
      <template #header="{ collapsed }">
        <div class="flex w-full min-w-0 items-center gap-2 px-1 font-semibold">
          <UIcon name="i-lucide-building-2" class="size-5 shrink-0 text-primary" />
          <span v-if="!collapsed" class="min-w-0 truncate">Agent Office</span>
          <PrefSwitcher v-if="!collapsed" class="ms-auto shrink-0" />
        </div>
      </template>

      <template #default="{ collapsed }">
        <UNavigationMenu v-model="navOpen" :collapsed="collapsed" :items="items" orientation="vertical" />
      </template>

      <template #footer="{ collapsed }">
        <div class="flex w-full min-w-0 flex-col gap-1">
          <div v-if="isOfficeAdmin && buildLabel && !collapsed" class="flex min-w-0 items-center gap-1 px-2.5 text-xs text-(--ui-text-dimmed)">
            <!-- the running build, and the update page: an admin's -->
            <NuxtLink to="/admin/update" class="flex min-w-0 items-center gap-1 hover:text-(--ui-text-muted)">
              <span v-if="hasUpdate" class="size-1.5 shrink-0 rounded-full bg-primary" :title="t('build.hasUpdate')" />
              <span class="truncate">{{ buildText }}</span>
            </NuxtLink>
            <UTooltip v-model:open="buildInfoOpen" :content="{ side: 'top' }">
              <button type="button" class="shrink-0" :aria-label="build?.subject || t('build.noSubject')" @click="buildInfoOpen = !buildInfoOpen">
                <UIcon name="i-lucide-info" class="size-3.5" />
              </button>
              <template #content>
                <div class="max-w-72 space-y-0.5 text-xs">
                  <p class="font-medium">{{ build?.subject || t('build.noSubject') }}</p>
                  <p v-if="buildTime" class="text-(--ui-text-muted)">{{ buildTime }}</p>
                </div>
              </template>
            </UTooltip>
          </div>
          <UDropdownMenu :items="userMenu" :content="{ align: 'start' }" class="w-full">
            <UButton
              color="neutral"
              variant="ghost"
              block
              :square="collapsed"
              class="justify-start"
              :avatar="{ alt: user?.name || user?.email }"
              :label="collapsed ? undefined : (user?.name || user?.email)"
              trailing-icon="i-lucide-chevrons-up-down"
              :ui="{ trailingIcon: collapsed ? 'hidden' : 'ms-auto' }"
            />
          </UDropdownMenu>
        </div>
      </template>
    </UDashboardSidebar>

    <div v-if="viewedBy" class="fixed bottom-3 left-1/2 z-50 flex max-w-[90vw] -translate-x-1/2 items-center gap-3 rounded-full bg-warning px-4 py-1.5 text-sm text-(--ui-bg) shadow-lg">
      <UIcon name="i-lucide-eye" class="size-4 shrink-0" />
      <span class="min-w-0 truncate">{{ t('user.viewingAs', { admin: viewedBy.email, email: user?.email ?? '' }) }}</span>
      <UButton size="xs" color="neutral" variant="solid" :label="t('user.viewingAsEnd')" @click="endViewAs" />
    </div>
    <slot />
    <FloatingChat />
    <SettingsModal v-model:open="settingsOpen" />
  </UDashboardGroup>
</template>
