<script setup lang="ts">
const colorMode = useColorMode()
const { lang, setLang, t } = useLang()

const isDark = computed(() => colorMode.value === 'dark')
// a theme decides light or dark by its own colors: the switch is the default look's
const { theme } = useTheme()
</script>

<template>
  <div class="flex items-center gap-1">
    <UButton
      v-if="!theme"
      :icon="isDark ? 'i-lucide-sun' : 'i-lucide-moon'"
      :aria-label="t(isDark ? 'pref.light' : 'pref.dark')"
      color="neutral"
      variant="ghost"
      size="sm"
      @click="colorMode.preference = isDark ? 'light' : 'dark'"
    />
    <UButton
      :label="lang === 'vi' ? 'VI' : 'EN'"
      :aria-label="t('pref.lang')"
      color="neutral"
      variant="ghost"
      size="sm"
      @click="setLang(lang === 'vi' ? 'en' : 'vi')"
    />
  </div>
</template>
