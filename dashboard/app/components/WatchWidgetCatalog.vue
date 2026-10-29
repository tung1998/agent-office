<script setup lang="ts">
import type { WidgetKind } from '~/utils/watchWidgets'

// current: the box's widget, when it is changed (not a box added)
const props = defineProps<{ projects: Project[], current?: WidgetKind }>()
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ select: [kind: WidgetKind] }>()
const { t } = useLang()
const { isOfficeAdmin, canCreateProject } = useAuth()
const query = ref('')

const someHas = (feature: FeatureId) => props.projects.some(p => hasFeature(p, feature))
const gallery = computed(() => widgetGroups.map(group => {
  const kinds: WidgetKind[] = [
    ...(group === 'agents' ? ['chat' as const] : []),
    ...groupWidgets(group, isOfficeAdmin.value, someHas, canCreateProject.value).map(w => w.kind)
  ]
  const widgets = kinds.map(kind => widgetDef(kind)).filter(w => {
    const term = query.value.trim().toLocaleLowerCase()
    return !term || `${t(`watchW.kind.${w.kind}` as 'watchW.kind.chat')} ${t(`watchW.desc.${w.kind}` as 'watchW.desc.chat')}`.toLocaleLowerCase().includes(term)
  })
  return { group, widgets }
}).filter(g => g.widgets.length))

function choose(kind: WidgetKind) {
  emit('select', kind)
  open.value = false
  query.value = ''
}
</script>

<template>
  <UModal v-model:open="open" :title="current ? t('watchW.catalog.changeTitle') : t('watchW.catalog.title')" :description="current ? t('watchW.catalog.changeHint') : t('watchW.catalog.hint')" :ui="{ content: 'sm:max-w-6xl', body: 'max-h-[min(78dvh,54rem)] overflow-y-auto p-3 sm:p-5' }">
    <template #body>
      <div class="sticky -top-3 z-10 -mx-3 -mt-3 border-b border-(--ui-border) bg-(--ui-bg)/95 p-3 backdrop-blur sm:-top-5 sm:-mx-5 sm:-mt-5 sm:p-5">
        <UInput v-model="query" icon="i-lucide-search" :placeholder="t('watchW.catalog.search')" class="w-full sm:max-w-sm" />
      </div>
      <div v-if="gallery.length" class="space-y-7 pt-5">
        <section v-for="group in gallery" :key="group.group">
          <h3 class="mb-3 flex items-center gap-2 text-sm font-semibold text-(--ui-text-highlighted)">
            <UIcon :name="groupIcon[group.group]" class="size-4 text-(--ui-primary)" />
            {{ t(`watchW.group.${group.group}`) }}
            <span class="text-xs font-normal text-(--ui-text-dimmed)">{{ group.widgets.length }}</span>
          </h3>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
            <button
              v-for="widget in group.widgets" :key="widget.kind" type="button"
              class="group flex min-w-0 flex-col overflow-hidden rounded-xl border bg-(--ui-bg) text-start transition hover:border-(--ui-primary)/70 hover:shadow-md focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-(--ui-primary)"
              :class="widget.kind === current ? 'border-(--ui-primary) ring-1 ring-(--ui-primary)' : 'border-(--ui-border)'"
              :aria-label="`${current ? t('watchW.catalog.use') : t('watchW.catalog.add')} ${t(`watchW.kind.${widget.kind}` as 'watchW.kind.chat')}`"
              @click="choose(widget.kind)"
            >
              <WatchWidgetSample :kind="widget.kind" />
              <span class="flex min-w-0 flex-1 flex-col p-3">
                <span class="flex items-center gap-2">
                  <UIcon :name="widget.icon" class="size-4 shrink-0 text-(--ui-primary)" />
                  <span class="min-w-0 flex-1 truncate text-sm font-semibold">{{ t(`watchW.kind.${widget.kind}` as 'watchW.kind.chat') }}</span>
                  <span class="shrink-0 rounded-md bg-(--ui-bg-elevated) px-1.5 py-0.5 text-[10px] tabular-nums text-(--ui-text-muted)">{{ widget.grid.w }}×{{ widget.grid.h }}</span>
                </span>
                <span class="mt-1.5 line-clamp-2 min-h-9 text-xs leading-4 text-(--ui-text-muted)">{{ t(`watchW.desc.${widget.kind}` as 'watchW.desc.chat') }}</span>
                <span v-if="widget.kind === current" class="mt-3 flex items-center gap-1 text-xs font-medium text-(--ui-text-muted)">
                  <UIcon name="i-lucide-check" class="size-3.5" />{{ t('watchW.catalog.inUse') }}
                </span>
                <span v-else class="mt-3 flex items-center gap-1 text-xs font-medium text-(--ui-primary) opacity-80 transition group-hover:opacity-100">
                  <UIcon :name="current ? 'i-lucide-replace' : 'i-lucide-plus'" class="size-3.5" />{{ current ? t('watchW.catalog.use') : t('watchW.catalog.add') }}
                </span>
              </span>
            </button>
          </div>
        </section>
      </div>
      <div v-else class="grid min-h-40 place-items-center py-8 text-sm text-(--ui-text-muted)">{{ t('watchW.catalog.noResults') }}</div>
    </template>
  </UModal>
</template>
