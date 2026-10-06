<script setup lang="ts">
// a unified diff, coloured line by line (patch cards, the file editor). A big
// one shows its first lines, more on asking: every line is an element.
const props = defineProps<{ diff: string }>()
const { t } = useLang()

const STEP = 2000
const limit = ref(STEP)
watch(() => props.diff, () => { limit.value = STEP })
const all = computed(() => props.diff.split('\n'))
const lines = computed(() => all.value.slice(0, limit.value).map((l) => {
  const kind = l.startsWith('+++') || l.startsWith('---') ? 'file'
    : l.startsWith('@@') ? 'hunk'
      : l.startsWith('+') ? 'add'
        : l.startsWith('-') ? 'del' : 'ctx'
  return { text: l, kind }
}))
const rest = computed(() => Math.max(0, all.value.length - limit.value))
</script>

<template>
  <div class="overflow-auto">
    <pre class="py-1 text-xs leading-5"><code><span
      v-for="(l, i) in lines" :key="i" class="block px-3"
      :class="{
        'bg-(--ui-success)/10 text-(--ui-success)': l.kind === 'add',
        'bg-(--ui-error)/10 text-(--ui-error)': l.kind === 'del',
        'text-(--ui-text-muted)': l.kind === 'hunk' || l.kind === 'file'
      }"
    >{{ l.text || ' ' }}</span></code></pre>
    <div v-if="rest" class="flex items-center gap-2 border-t border-(--ui-border) px-3 py-1.5 text-xs text-(--ui-text-muted)">
      <span class="flex-1">{{ t('diff.more', { n: rest }) }}</span>
      <UButton size="xs" color="neutral" variant="outline" :label="t('diff.showMore', { n: Math.min(rest, STEP) })" @click="limit += STEP" />
    </div>
  </div>
</template>
