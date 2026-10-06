<script setup lang="ts">
import type { WatchNode } from '~/composables/useWatchLayout'

// One node of the watch screen: a box, or a split of boxes with drag handles between them.
const props = defineProps<{ node: WatchNode, projects: Project[] }>()
const { resize } = useWatchLayout()

const el = ref<HTMLElement | null>(null)
const dragging = ref(-1)
function startDrag(i: number, e: PointerEvent) {
  if (props.node.kind !== 'split' || !el.value) return
  const split = props.node
  const row = split.dir === 'row'
  const rect = el.value.getBoundingClientRect()
  const total = row ? rect.width : rect.height
  const start = row ? e.clientX : e.clientY
  const base = [...split.sizes]
  dragging.value = i
  const move = (ev: PointerEvent) => {
    const delta = ((row ? ev.clientX : ev.clientY) - start) / total * 100
    const pair = base[i]! + base[i + 1]!
    const a = Math.min(pair - 10, Math.max(10, base[i]! + delta)) // no box under 10%
    const next = [...base]
    next[i] = a
    next[i + 1] = pair - a
    resize(split.id, next)
  }
  const up = () => {
    dragging.value = -1
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', up)
  }
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', up)
}
</script>

<template>
  <WatchBox v-if="node.kind === 'pane'" :pane="node" :projects="projects" class="h-full min-h-0 min-w-0" />
  <div v-else ref="el" class="flex h-full min-h-0 min-w-0" :class="[node.dir === 'row' ? 'flex-row' : 'flex-col', dragging >= 0 && 'select-none']">
    <template v-for="(c, i) in node.children" :key="c.id">
      <div class="min-h-0 min-w-0" :style="{ flex: `${node.sizes[i] ?? 1} 1 0` }">
        <WatchTree :node="c" :projects="projects" />
      </div>
      <div
        v-if="i < node.children.length - 1"
        class="shrink-0 rounded-full transition-colors hover:bg-(--ui-primary)/40"
        :class="[node.dir === 'row' ? 'mx-0.5 w-1 cursor-col-resize' : 'my-0.5 h-1 cursor-row-resize', dragging === i && 'bg-(--ui-primary)/60']"
        role="separator" :aria-orientation="node.dir === 'row' ? 'vertical' : 'horizontal'"
        @pointerdown.prevent="startDrag(i, $event)"
      />
    </template>
  </div>
</template>
