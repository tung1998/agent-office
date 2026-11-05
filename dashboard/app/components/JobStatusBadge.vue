<script setup lang="ts">
const props = defineProps<{ status: JobStatus, iconOnlyOnMobile?: boolean }>()
const { t } = useLang()
const meta = computed(() => jobStatusMeta[props.status] ?? jobStatusMeta.pending)
</script>

<template>
  <UBadge :color="meta.color" variant="subtle" size="sm" :icon="meta.icon" :label="t(`job.status.${status}`)" :title="t(`job.status.${status}`)" class="whitespace-nowrap" :ui="{ leadingIcon: status === 'running' ? 'animate-spin' : '', label: iconOnlyOnMobile ? 'max-sm:sr-only' : '' }" />
</template>
