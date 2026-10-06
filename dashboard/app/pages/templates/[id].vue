<script setup lang="ts">
const route = useRoute()
const id = computed(() => route.params.id as string)
const { t } = useLang()
</script>

<template>
  <PageShell :title="t('tpl.pageTitle')">
    <template #actions>
      <UButton to="/templates" icon="i-lucide-arrow-left" :label="t('tpl.allModels')" color="neutral" variant="ghost" />
    </template>
    <OrgModelEditor :key="id" :model-id="id" />
  </PageShell>
</template>
