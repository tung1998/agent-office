<script setup lang="ts">
// The account's theme (from the user menu), a panel of its own on the right:
// colors, wallpapers, its own backgrounds and how see-through the panes are.
const open = defineModel<boolean>('open', { default: false })
const { t } = useLang()
</script>

<template>
  <USlideover v-model:open="open" side="right" :title="t('theme.title')">
    <template #body>
      <ThemePicker />
    </template>
  </USlideover>
</template>
