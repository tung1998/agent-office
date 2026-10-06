<script setup lang="ts">
defineProps<{ templates: OrgModel[], allowAi?: boolean }>()
const selected = defineModel<string>({ default: '' })
const { t: translate } = useLang()
</script>

<template>
  <div class="grid gap-2">
    <button
      v-if="allowAi" type="button"
      class="flex items-start gap-3 rounded-lg border p-3 text-left transition"
      :class="selected === '__ai' ? 'border-(--ui-primary) bg-(--ui-primary)/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
      @click="selected = '__ai'"
    >
      <UIcon name="i-lucide-sparkles" class="mt-0.5 size-5 shrink-0 text-primary" />
      <div class="min-w-0">
        <p class="font-medium">{{ translate('tpl.aiSuggest') }} <span class="text-xs font-normal text-(--ui-text-muted)">{{ translate('tpl.aiRecommended') }}</span></p>
        <p class="text-sm text-(--ui-text-muted)">{{ translate('tpl.aiDesc') }}</p>
      </div>
    </button>
    <button
      v-for="tpl in templates" :key="tpl.id" type="button"
      class="flex items-start gap-3 rounded-lg border p-3 text-left transition"
      :class="selected === tpl.id ? 'border-(--ui-primary) bg-(--ui-primary)/5' : 'border-(--ui-border) hover:border-(--ui-border-accented)'"
      @click="selected = tpl.id"
    >
      <UIcon :name="kindIcon[tpl.kind]" class="mt-0.5 size-5 shrink-0 text-primary" />
      <div class="min-w-0">
        <p class="font-medium">
          {{ tpl.name }} <span class="text-xs font-normal text-(--ui-text-muted)">{{ translate('tpl.agentCount', { n: tpl.agent_count }) }}</span>
        </p>
        <p class="line-clamp-2 text-sm text-(--ui-text-muted)">{{ tpl.description }}</p>
      </div>
    </button>
  </div>
</template>
