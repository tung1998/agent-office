<script setup lang="ts">
import type { DropdownMenuItem, NavigationMenuItem } from '@nuxt/ui'

const route = useRoute()
const { user, build, isAdmin, logout } = useAuth()
const { t, dateLocale } = useLang()

// which office is running, above the user (from /api/auth/me)
const buildLabel = computed(() => {
  const b = build.value
  if (!b) return ''
  return b.revision ? `${b.version} · ${b.revision.slice(0, 7)}` : b.version
})
const buildText = computed(() => build.value?.dirty ? `${buildLabel.value} · ${t('build.dirty')}` : buildLabel.value)
const buildTime = computed(() => {
  const at = build.value?.time
  return at ? t('build.time', { time: new Date(at).toLocaleString(dateLocale.value) }) : ''
})
const buildInfoOpen = ref(false)
// a dot when the source moved past the running build (admins, once per load)
const hasUpdate = ref(false)
watch(isAdmin, async (admin) => {
  if (!admin || !import.meta.client) return
  try {
    hasUpdate.value = (await $fetch<{ changed: boolean }>('/api/system/update/changes')).changed
  } catch { /* no source to compare with */ }
}, { immediate: true })

const bareLayout = computed(() => route.path === '/login' || route.path === '/setup-account')

// sidebar: the 5 projects this viewer opens most, each with its sections
// (refetched on navigation and on a change, so added/renamed projects show up)
const projectList = ref<Project[]>([])
const { top } = useProjectUsage()
const recentProjects = computed(() => top(projectList.value, 5, route.params.id as string | undefined))
async function loadProjects() {
  if (bareLayout.value) return
  try {
    projectList.value = (await $fetch<{ projects: Project[] }>('/api/projects')).projects
  } catch { /* signed out: the auth middleware redirects */ }
}
watch(() => route.path, loadProjects, { immediate: true })
useLive(['repos', 'org_models'], loadProjects) // added, renamed, removed: without leaving the page
// what changes elsewhere shows up here (ADR-072): opened once signed in —
// also right after the login page, where this layout stays mounted
onMounted(() => {
  watch(() => !bareLayout.value && !!user.value, (on) => { if (on) startLive() }, { immediate: true })
})

// pages below a project (/projects/:id/<child>/…) belong to one of its sections
const childSection: Record<string, string> = { agents: 'model', automations: 'automations' }

function projectSections(id: string): NavigationMenuItem[] {
  const to = (tab: string) => ({ path: `/projects/${id}`, query: { tab } })
  const child = route.path.match(new RegExp(`^/projects/${id}/([^/]+)/`))?.[1]
  const parent = child ? childSection[child] : undefined
  return withParent(parent, [
    { label: t('nav.chat'), icon: 'i-lucide-messages-square', to: to('chat'), exactQuery: 'partial' },
    // Việc has no tab (ADR-056): the team works through the chat; a task's page stays for its links
    { label: t('nav.automations'), icon: 'i-lucide-alarm-clock', to: to('automations'), exactQuery: 'partial' },
    { label: t('nav.ops'), icon: 'i-lucide-activity', to: to('ops'), exactQuery: 'partial' },
    { label: t('project.sectionModel'), icon: 'i-lucide-users', to: to('model'), exactQuery: 'partial' },
    { label: t('project.sectionPerm'), icon: 'i-lucide-shield', to: to('perm'), exactQuery: 'partial' },
    ...(isAdmin.value
      ? [
          { label: t('files.section'), icon: 'i-lucide-folder-tree', to: to('files'), exactQuery: 'partial' as const },
          { label: t('project.sectionSkill'), icon: 'i-lucide-sparkles', to: to('skill'), exactQuery: 'partial' as const },
          { label: t('project.sectionMcp'), icon: 'i-lucide-plug-zap', to: to('mcp'), exactQuery: 'partial' as const },
          { label: t('project.sectionBurn'), icon: 'i-lucide-flame', to: to('burn'), exactQuery: 'partial' as const },
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

// what needs a person across the office: its count on "Sự cố"
const { data: incData, refresh: refreshInc } = useLiveFetch<{ count: number }>('/api/incidents', { lazy: true })
const attention = computed(() => incData.value?.count ?? 0)
let incTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => { incTimer = setInterval(() => refreshInc(), 60000) })
onBeforeUnmount(() => clearInterval(incTimer))

const items = computed<NavigationMenuItem[][]>(() => {
  // office-wide pages: the day's work, then how office is set up
  const office: NavigationMenuItem[] = [
    { label: t('nav.work'), type: 'label' },
    // one place to follow the office: what needs a person is counted here
    { label: t('assistant.title'), icon: 'i-lucide-sparkles', to: '/assistant' },
    { label: t('watch.title'), icon: 'i-lucide-layout-grid', to: '/watch' },
    { label: t('nav.overview'), icon: 'i-lucide-layout-dashboard', to: '/', badge: attention.value || undefined },
    { label: t('nav.jobs'), icon: 'i-lucide-list-checks', to: '/jobs' }
  ]
  const settings: NavigationMenuItem[] = [
    { label: t('nav.settings'), type: 'label' },
    { label: t('nav.providers'), icon: 'i-lucide-plug', to: '/providers' },
    { label: t('nav.templates'), icon: 'i-lucide-network', to: '/templates' },
    ...(isAdmin.value ? [{ label: t('nav.library'), icon: 'i-lucide-library', to: '/library' }] : [])
  ]
  const admin: NavigationMenuItem[] = isAdmin.value
    ? [
        { label: t('nav.admin'), type: 'label' },
        { label: t('nav.users'), icon: 'i-lucide-users', to: '/admin/users' },
        { label: t('nav.audit'), icon: 'i-lucide-scroll-text', to: '/admin/audit' },
        { label: t('nav.transfer'), icon: 'i-lucide-archive-restore', to: '/admin/transfer' },
        { label: t('nav.data'), icon: 'i-lucide-database', to: '/admin/data' },
        { label: t('nav.update'), icon: 'i-lucide-package', to: '/admin/update' }
      ]
    : []
  // projects: a title, the most used ones, then a link to all of them
  const projects: NavigationMenuItem[] = [
    { label: t('nav.projects'), type: 'label' },
    ...recentProjects.value.map(p => ({
      value: `project-${p.id}`, // open state follows the project, not its position
      label: p.name,
      icon: p.scope === 'machine' ? 'i-lucide-monitor' : 'i-lucide-folder',
      to: { path: `/projects/${p.id}`, query: { tab: 'chat' } },
      defaultOpen: route.params.id === p.id,
      children: projectSections(p.id)
    })),
    projectList.value.length
      ? { label: t('nav.allProjects'), icon: 'i-lucide-list', to: '/projects', exact: true }
      : { label: t('nav.addProject'), icon: 'i-lucide-plus', to: '/projects', exact: true }
  ]
  return [projects, office, settings, admin]
})

const userMenu = computed<DropdownMenuItem[][]>(() => [
  [{ label: user.value?.email ?? '', type: 'label' }],
  [{ label: t('user.changePassword'), icon: 'i-lucide-key-round', to: '/account' }, { label: t('cli.title'), icon: 'i-lucide-terminal', to: '/account' }],
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
        <!-- remount when the open project or the list changes so its sections expand -->
        <UNavigationMenu :key="`${route.params.id ?? ''}:${projectList.length}`" :collapsed="collapsed" :items="items" orientation="vertical" />
      </template>

      <template #footer="{ collapsed }">
        <div class="flex w-full min-w-0 flex-col gap-1">
          <div v-if="buildLabel && !collapsed" class="flex min-w-0 items-center gap-1 px-2.5 text-xs text-(--ui-text-dimmed)">
            <!-- the update page is admin-only: members just read the line -->
            <NuxtLink v-if="isAdmin" to="/admin/update" class="flex min-w-0 items-center gap-1 hover:text-(--ui-text-muted)">
              <span v-if="hasUpdate" class="size-1.5 shrink-0 rounded-full bg-primary" :title="t('build.hasUpdate')" />
              <span class="truncate">{{ buildText }}</span>
            </NuxtLink>
            <span v-else class="min-w-0 truncate">{{ buildText }}</span>
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

    <slot />
    <FloatingChat />
  </UDashboardGroup>
</template>
