<script setup lang="ts">
const { lang, uiLocale } = useLang()

useHead({
  htmlAttrs: {
    lang
  }
})
</script>

<template>
  <UApp :locale="uiLocale" :toaster="{ position: 'top-right' }">
    <!-- a bar at the top the moment a page starts loading: a click always shows it was heard -->
    <NuxtLoadingIndicator color="var(--ui-primary)" :height="3" :throttle="0" />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
  </UApp>
</template>
