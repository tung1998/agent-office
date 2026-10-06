<script setup lang="ts">
// Folder picker backed by the office server (GET /api/fs/dirs), because a
// browser cannot give a web page the absolute path of a local folder.
interface Entry {
  name: string
  path: string
  markers: string[]
  is_project: boolean
  has_children: boolean
  registered: boolean
}
interface Listing {
  path: string
  parent: string
  entries: Entry[]
  truncated: boolean
  shortcuts: { label: string, path: string }[]
}
interface Node extends Entry {
  depth: number
  open: boolean
  loading: boolean
  children: Node[] | null
}

const selected = defineModel<string>({ default: '' })
const { t } = useLang()

const root = ref<Listing | null>(null)
const nodes = ref<Node[]>([])
const loading = ref(false)
const error = ref('')
const showHidden = ref(false)

async function fetchDir(path: string) {
  return await $fetch<Listing>('/api/fs/dirs', { query: { path, hidden: showHidden.value ? '1' : undefined } })
}

function toNodes(entries: Entry[], depth: number): Node[] {
  return entries.map(e => ({ ...e, depth, open: false, loading: false, children: null }))
}

async function openRoot(path = '') {
  loading.value = true
  error.value = ''
  try {
    root.value = await fetchDir(path)
    nodes.value = toNodes(root.value.entries, 0)
  } catch (e) {
    error.value = apiError(e, t('folder.fetchError'))
  } finally {
    loading.value = false
  }
}

async function toggle(n: Node) {
  if (n.open) {
    n.open = false
    return
  }
  if (!n.children) {
    n.loading = true
    try {
      const l = await fetchDir(n.path)
      n.children = toNodes(l.entries, n.depth + 1)
    } catch (e) {
      error.value = apiError(e, t('folder.fetchError'))
      n.loading = false
      return
    }
    n.loading = false
  }
  n.open = true
}

// Flatten the open part of the tree for rendering.
const visible = computed(() => {
  const out: Node[] = []
  const walk = (list: Node[]) => {
    for (const n of list) {
      out.push(n)
      if (n.open && n.children) walk(n.children)
    }
  }
  walk(nodes.value)
  return out
})

const crumbs = computed(() => {
  const p = root.value?.path ?? ''
  const parts = p.split('/').filter(Boolean)
  return [{ label: '/', path: '/' }, ...parts.map((part, i) => ({ label: part, path: '/' + parts.slice(0, i + 1).join('/') }))]
})

watch(showHidden, () => openRoot(root.value?.path ?? ''))
onMounted(() => openRoot(selected.value || ''))
</script>

<template>
  <div class="overflow-hidden rounded-lg border border-(--ui-border)">
    <div class="flex flex-wrap items-center gap-1 border-b border-(--ui-border) bg-(--ui-bg-muted) px-2 py-1.5">
      <UButton
        v-for="s in root?.shortcuts ?? []" :key="s.path" :label="s.label" size="xs" color="neutral"
        :variant="root?.path === s.path ? 'soft' : 'ghost'" @click="openRoot(s.path)"
      />
      <USwitch v-model="showHidden" size="xs" :label="t('folder.hidden')" class="ms-auto" />
    </div>

    <div class="flex items-center gap-0.5 overflow-x-auto border-b border-(--ui-border) px-2 py-1 text-xs whitespace-nowrap">
      <UButton
        v-if="root?.parent" icon="i-lucide-arrow-up" size="xs" color="neutral" variant="ghost" :title="t('folder.upOneLevel')"
        @click="openRoot(root.parent)"
      />
      <template v-for="(c, i) in crumbs" :key="c.path">
        <span v-if="i > 1" class="text-(--ui-text-dimmed)">/</span>
        <button type="button" class="rounded px-1 hover:bg-(--ui-bg-accented)" @click="openRoot(c.path)">{{ c.label }}</button>
      </template>
    </div>

    <div class="h-72 overflow-y-auto py-1 text-sm">
      <div v-if="loading" class="p-4 text-center text-(--ui-text-muted)">{{ t('folder.loading') }}</div>
      <UAlert v-else-if="error" class="m-2" color="error" variant="subtle" :description="error" />
      <p v-else-if="!visible.length" class="p-4 text-center text-(--ui-text-muted)">{{ t('folder.empty') }}</p>
      <div
        v-for="n in visible" :key="n.path"
        class="group flex cursor-pointer items-center gap-1 py-1 pe-2"
        :class="selected === n.path ? 'bg-(--ui-primary)/10 text-(--ui-primary)' : 'hover:bg-(--ui-bg-muted)'"
        :style="{ paddingInlineStart: `${8 + n.depth * 18}px` }"
        @click="selected = n.path"
        @dblclick="n.has_children && toggle(n)"
      >
        <button
          type="button" class="flex size-5 shrink-0 items-center justify-center rounded hover:bg-(--ui-bg-accented)"
          :class="n.has_children ? '' : 'invisible'" @click.stop="toggle(n)"
        >
          <UIcon v-if="n.loading" name="i-lucide-loader-circle" class="size-3.5 animate-spin" />
          <UIcon v-else :name="n.open ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'" class="size-3.5" />
        </button>
        <UIcon :name="n.is_project ? 'i-lucide-folder-git-2' : (n.open ? 'i-lucide-folder-open' : 'i-lucide-folder')" class="size-4 shrink-0" :class="n.is_project ? 'text-primary' : 'text-(--ui-text-muted)'" />
        <span class="truncate">{{ n.name }}</span>
        <UBadge v-if="n.registered" :label="t('folder.registered')" size="sm" color="success" variant="subtle" class="ms-1" />
        <span v-if="n.is_project" class="ms-auto hidden truncate ps-2 text-xs text-(--ui-text-muted) group-hover:inline">{{ n.markers.join(' · ') }}</span>
      </div>
      <p v-if="root?.truncated" class="p-2 text-xs text-(--ui-text-muted)">{{ t('folder.truncated') }}</p>
    </div>

    <div class="flex items-center gap-2 border-t border-(--ui-border) bg-(--ui-bg-muted) px-2 py-1.5">
      <UIcon name="i-lucide-folder-check" class="size-4 shrink-0 text-(--ui-text-muted)" />
      <input
        v-model="selected" class="min-w-0 flex-1 bg-transparent font-mono text-xs outline-none"
        :placeholder="t('folder.placeholder')"
      >
      <UButton
        v-if="root" size="xs" color="neutral" variant="ghost" :label="t('folder.useOpen')"
        @click="selected = root.path"
      />
    </div>
  </div>
</template>
