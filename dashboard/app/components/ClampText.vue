<script setup lang="ts">
// long text clamped to a few lines; a toggle to read the rest shows up only
// when it was actually cut (measured, not guessed from length)
const props = withDefaults(defineProps<{ text: string, lines?: number }>(), { lines: 4 })
const { t } = useLang()
const el = ref<HTMLElement | null>(null)
const expanded = ref(false)
const clamped = ref(false)
function check() {
  if (!el.value || expanded.value) return
  clamped.value = el.value.scrollHeight > el.value.clientHeight + 1
}
let ro: ResizeObserver | undefined
onMounted(() => {
  check()
  ro = new ResizeObserver(() => check())
  if (el.value) ro.observe(el.value)
})
onBeforeUnmount(() => ro?.disconnect())
watch(() => props.text, () => nextTick(check))
watch(expanded, () => nextTick(check))
</script>

<template>
  <div>
    <p
      ref="el" class="whitespace-pre-line"
      :style="expanded ? undefined : { display: '-webkit-box', WebkitLineClamp: lines, WebkitBoxOrient: 'vertical', overflow: 'hidden' }"
    >{{ text }}</p>
    <button
      v-if="clamped || expanded" type="button" class="text-xs font-medium text-(--ui-primary) hover:underline"
      :aria-expanded="expanded" @click="expanded = !expanded"
    >
      {{ expanded ? t('common.less') : t('common.more') }}
    </button>
  </div>
</template>
