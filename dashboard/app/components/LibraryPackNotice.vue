<script setup lang="ts">
defineProps<{ pack: string }>()
const { t } = useLang()
</script>

<template>
  <UAlert
    color="warning" variant="subtle" icon="i-lucide-package"
    :title="t('team.lib.requiresPack', { pack })" :description="t('team.lib.packHelp')"
  />
</template>
