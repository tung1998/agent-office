<script setup lang="ts">
// The shape of a list while its data comes (a phone over a VPN): rows of an
// icon and two lines, so the page does not jump when they arrive.
withDefaults(defineProps<{ n?: number, icon?: boolean }>(), { n: 3, icon: true })
</script>

<template>
  <div class="divide-y divide-(--ui-border)" aria-busy="true">
    <div v-for="i in n" :key="i" class="flex items-center gap-3 px-4 py-2.5">
      <USkeleton v-if="icon" class="size-8 shrink-0 rounded-lg" />
      <div class="min-w-0 flex-1 space-y-1.5">
        <USkeleton class="h-3.5" :style="{ width: `${70 - (i % 3) * 15}%` }" />
        <USkeleton class="h-3 w-1/3" />
      </div>
    </div>
  </div>
</template>
