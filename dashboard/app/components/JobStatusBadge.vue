<script setup lang="ts">
const props = defineProps<{ status: JobStatus }>()
const { t } = useLang()
const meta = computed(() => jobStatusMeta[props.status] ?? jobStatusMeta.pending)
</script>

<template>
  <UBadge :color="meta.color" variant="subtle" size="sm" :icon="meta.icon" :label="t(`job.status.${status}`)" :ui="{ leadingIcon: status === 'running' ? 'animate-spin' : '' }" />
</template>
