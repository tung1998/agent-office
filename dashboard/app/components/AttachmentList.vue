<script setup lang="ts">
// Files attached to a sent message or a task: image thumbnails, other files as chips.
import type { Attachment } from './PromptInput.vue'

defineProps<{ items: Attachment[], align?: 'start' | 'end' }>()
const icon = { image: 'i-lucide-image', pdf: 'i-lucide-file-text', text: 'i-lucide-file-code' }
</script>

<template>
  <div v-if="items.length" class="flex flex-wrap gap-2" :class="align === 'end' ? 'justify-end' : ''">
    <a
      v-for="a in items" :key="a.id" :href="`/api/attachments/${a.id}`" target="_blank" rel="noopener"
      class="flex items-center gap-2 rounded-md border border-(--ui-border) bg-(--ui-bg-elevated) text-xs transition hover:border-(--ui-primary)"
      :class="a.kind === 'image' ? 'p-0.5' : 'py-1.5 ps-2 pe-2.5'"
    >
      <img v-if="a.kind === 'image'" :src="`/api/attachments/${a.id}`" :alt="a.name" class="max-h-40 max-w-60 rounded object-contain">
      <template v-else>
        <UIcon :name="icon[a.kind]" class="size-4 text-(--ui-text-muted)" />
        <span class="max-w-48 truncate">{{ a.name }}</span>
      </template>
    </a>
  </div>
</template>
