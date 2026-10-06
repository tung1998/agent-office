<script setup lang="ts">
// Picks a chat's tags: the project's tags to choose again quickly, or a new
// one typed in. Each change is emitted at once (the caller saves it).
const props = withDefaults(defineProps<{ suggestions: string[], autofocus?: boolean }>(), { autofocus: true })
const model = defineModel<string[]>({ default: () => [] })
const { t } = useLang()
// what is on the chat and what the project has, one list, no repeats
const items = computed(() => {
  const out = [...model.value]
  for (const s of props.suggestions) if (!out.some(x => sameTag(x, s))) out.push(s)
  return out
})
// what is typed: emptied once a tag is added (Enter) or picked
const search = ref('')
function onCreate(name: string) {
  const tag = name.trim().replace(/\s+/g, ' ')
  if (tag && !model.value.some(x => sameTag(x, tag))) model.value = [...model.value, tag]
  search.value = ''
}
watch(model, () => { search.value = '' })
</script>

<template>
  <UInputMenu
    v-model="model" v-model:search-term="search" :items="items" multiple create-item
    :placeholder="t('chatTag.placeholder')" icon="i-lucide-tag" class="w-full" :autofocus="props.autofocus"
    @create="onCreate"
  />
</template>
