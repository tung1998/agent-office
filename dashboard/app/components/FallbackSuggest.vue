<script setup lang="ts">
// The fallback order office suggests for an agent (ADR-267): another vendor
// first, then another account of the same vendor; none broken or out of
// quota. Applying it only fills the form: the person saves the card.
const props = defineProps<{ agentId: string, providers: { id: string, name: string }[], current: string[] }>()
const emit = defineEmits<{ apply: [order: string[]] }>()
const { t } = useLang()
const { data, error } = useLiveFetch<{ order: string[], reasons: string[] }>(() => `/api/agents/${props.agentId}/fallback-suggest`, { lazy: true })
const name = (id: string) => props.providers.find(p => p.id === id)?.name ?? id
const ids = (list: string[]) => list.map(e => e.split('|')[0])
const same = computed(() => !!data.value && JSON.stringify(ids(props.current)) === JSON.stringify(data.value.order))
</script>

<template>
  <div v-if="!error" class="rounded-md border border-dashed border-(--ui-border) p-2.5 text-xs">
    <p class="flex items-center gap-1 font-medium">
      <UIcon name="i-lucide-sparkles" class="size-3.5 text-(--ui-primary)" />
      <InfoLabel :label="t('scoring.fallback')" :info="t('scoring.fallbackHint')" />
    </p>
    <USkeleton v-if="!data" class="mt-1.5 h-4 w-48" />
    <p v-else-if="!data.order.length" class="mt-1 text-(--ui-text-muted)">{{ t('scoring.fallbackNone') }}</p>
    <template v-else>
      <ol class="mt-1.5 flex flex-wrap items-center gap-1.5">
        <li v-for="(id, i) in data.order" :key="id" class="flex items-center gap-1">
          <span class="tabular-nums text-(--ui-text-muted)">{{ i + 1 }}.</span>
          <span>{{ name(id) }}</span>
          <UBadge :color="data.reasons[i] === 'other_vendor' ? 'success' : 'neutral'" variant="subtle" size="sm">
            {{ data.reasons[i] === 'other_vendor' ? t('scoring.fallbackOther') : t('scoring.fallbackSame') }}
          </UBadge>
        </li>
      </ol>
      <p v-if="same" class="mt-1 text-(--ui-text-muted)">{{ t('scoring.fallbackSame0') }}</p>
      <UButton v-else size="xs" variant="soft" class="mt-1.5" :label="t('scoring.fallbackApply')" @click="emit('apply', data.order)" />
    </template>
  </div>
</template>
