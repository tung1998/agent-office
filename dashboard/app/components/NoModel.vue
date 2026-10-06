<script setup lang="ts">
const { t } = useLang()
defineProps<{ projectId: string, admin: boolean }>()
defineEmits<{ choose: [] }>()
</script>

<template>
  <div class="rounded-lg border border-dashed border-(--ui-border) p-10 text-center">
    <UIcon name="i-lucide-network" class="mx-auto size-8 text-(--ui-text-dimmed)" />
    <p class="mt-2 font-medium">{{ t('noModel.title') }}</p>
    <p class="text-sm text-(--ui-text-muted)">{{ t('noModel.desc') }}</p>
    <div v-if="admin" class="mt-4 flex justify-center gap-2">
      <UButton :to="`/projects/${projectId}/setup`" icon="i-lucide-sparkles" :label="t('noModel.setupAi')" />
      <UButton icon="i-lucide-network" :label="t('noModel.choose')" color="neutral" variant="outline" @click="$emit('choose')" />
    </div>
  </div>
</template>
