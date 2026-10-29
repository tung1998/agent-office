<script setup lang="ts">
import type { WatchItem } from '~/composables/useWatchLayout'
import type { WidgetKind } from '~/utils/watchWidgets'

// One box of the watch board: a widget (a chat, a process, a monitor…) under
// a header (grip, widget, project, ⚙, menu). The widget lays out by the box's
// own size. A box one cell high or wide has no header (like a phone's
// widget): its buttons float over it on hover or touch.
const props = defineProps<{ item: WatchItem, projects: Project[], movable?: boolean }>()
const emit = defineEmits<{ grip: [e: PointerEvent] }>()
const { t } = useLang()
const { isOfficeAdmin, manages } = useAuth()
const { setPane, setConfig } = useWatchLayout()

const kind = computed(() => props.item.widget)
const def = computed(() => widgetDef(kind.value))
const project = computed(() => props.projects.find(p => p.id === props.item.projectId))
// what it needs before it can show anything
const needsProject = computed(() => def.value.project === 'required' && !project.value)
// a project's widget: its admin or manager (ADR-224); one of the machine: the office admin
const forbidden = computed(() => !!def.value.admin && (def.value.project === 'required' ? !!project.value && !manages(project.value.id) : !isOfficeAdmin.value))
// the project turned off the feature the widget shows (feature packs)
const featureOff = computed(() => !!def.value.feature && !!project.value && !hasFeature(project.value, def.value.feature))

const el = ref<HTMLElement | null>(null)
const size = ref({ w: 0, h: 0 })
let ro: ResizeObserver | undefined
watch(el, (node, old) => {
  if (!import.meta.client) return
  ro ??= new ResizeObserver(([e]) => {
    if (e) size.value = { w: Math.round(e.contentRect.width), h: Math.round(e.contentRect.height) }
  })
  if (old) ro.unobserve(old)
  if (node) ro.observe(node)
}, { flush: 'post' })
onBeforeUnmount(() => ro?.disconnect())
const tight = computed(() => size.value.w > 0 && size.value.w < 260)
const bare = computed(() => (props.item.w === 1 || props.item.h === 1) && kind.value !== 'chat' && !needsProject.value)

provide(watchBoxKey, {
  paneId: props.item.id,
  widget: kind,
  projectId: computed(() => props.item.projectId),
  size,
  cells: computed(() => ({ w: props.item.w, h: props.item.h })),
  config: computed(() => widgetConfig(kind.value, props.item.config)),
  set: (key, value) => setConfig(props.item.id, key, value)
})

const widgets: Record<Exclude<WidgetKind, 'chat'>, ReturnType<typeof resolveComponent>> = {
  process: resolveComponent('WatchWidgetProcess'),
  container: resolveComponent('WatchWidgetContainer'),
  monitor: resolveComponent('WatchWidgetMonitor'),
  stat: resolveComponent('WatchWidgetStat'),
  jobs: resolveComponent('WatchWidgetJobs'),
  chart: resolveComponent('WatchWidgetChart'),
  incidents: resolveComponent('WatchWidgetIncidents'),
  approvals: resolveComponent('WatchWidgetApprovals'),
  system: resolveComponent('WatchWidgetSystem'),
  budget: resolveComponent('WatchWidgetBudget'),
  lights: resolveComponent('WatchWidgetLights'),
  note: resolveComponent('WatchWidgetNote'),
  git: resolveComponent('WatchWidgetGit'),
  controls: resolveComponent('WatchWidgetControls'),
  automations: resolveComponent('WatchWidgetAutomations'),
  breakdown: resolveComponent('WatchWidgetBreakdown'),
  limits: resolveComponent('WatchWidgetLimits'),
  preview: resolveComponent('WatchWidgetPreview'),
  consoles: resolveComponent('WatchWidgetConsoles'),
  provider: resolveComponent('WatchWidgetProvider'),
  agent: resolveComponent('WatchWidgetAgent'),
  burn: resolveComponent('WatchWidgetBurn'),
  burnwork: resolveComponent('WatchWidgetBurnWork'),
  tasks: resolveComponent('WatchWidgetTasks'),
  changes: resolveComponent('WatchWidgetChanges'),
  team: resolveComponent('WatchWidgetTeam'),
  officemap: resolveComponent('WatchWidgetOfficeMap')
}
const title = computed(() => props.item.title || t(`watchW.kind.${kind.value}` as 'watchW.kind.chat'))
</script>

<template>
  <div class="group/hdr relative flex flex-col overflow-hidden rounded-lg border border-(--ui-border) bg-(--ui-bg)" tabindex="-1">
    <!-- the chat keeps its own header: the box's buttons go in it -->
    <div v-if="!bare && (kind !== 'chat' || needsProject)" class="flex shrink-0 items-center border-b border-(--ui-border)" :class="tight ? 'gap-0.5 p-1' : 'gap-1 p-1.5'">
      <WatchBoxControls :item="item" :projects="projects" :tight="tight" :movable="movable" part="lead" @grip="(e) => emit('grip', e)" />
      <p class="min-w-0 flex-1 truncate px-1 font-medium" :class="tight ? 'text-xs' : 'text-sm'" :title="title">{{ title }}</p>
      <WatchBoxControls :item="item" :projects="projects" :tight="tight" part="actions" />
    </div>
    <!-- no header: the buttons over the widget, on hover or touch -->
    <div
      v-if="bare"
      class="absolute end-1 top-1 z-10 flex items-center rounded-md border border-(--ui-border) bg-(--ui-bg)/95 opacity-0 shadow-sm transition group-hover/hdr:opacity-100 group-focus-within/hdr:opacity-100"
    >
      <WatchBoxControls :item="item" :projects="projects" tight :movable="movable" part="lead" @grip="(e) => emit('grip', e)" />
      <WatchBoxControls :item="item" :projects="projects" tight part="actions" />
    </div>
    <div ref="el" class="min-h-0 flex-1">
      <WatchEmpty v-if="forbidden" icon="i-lucide-lock" :text="t('watchW.adminOnly')" />
      <WatchEmpty v-else-if="featureOff" icon="i-lucide-eye-off" :text="t('watchW.featureOff')" />
      <WatchEmpty v-else-if="needsProject" :icon="def.icon" :text="t('watchW.needProject')">
        <USelect
          :items="projects.map(p => ({ label: p.name, value: p.id }))" :placeholder="t('watch.project')" class="w-full"
          @update:model-value="(v) => setPane(item.id, { projectId: String(v) })"
        />
      </WatchEmpty>
      <WatchWidgetChat v-else-if="kind === 'chat'" :key="item.projectId" :conversation-id="item.conversationId">
        <template #lead><WatchBoxControls :item="item" :projects="projects" :tight="tight" :movable="movable" part="lead" @grip="(e) => emit('grip', e)" /></template>
        <template #actions><WatchBoxControls :item="item" :projects="projects" :tight="tight" part="actions" /></template>
      </WatchWidgetChat>
      <component :is="widgets[kind as Exclude<WidgetKind, 'chat'>]" v-else :key="`${kind}:${item.projectId}`" />
    </div>
  </div>
</template>
