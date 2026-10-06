<script setup lang="ts">
// An agent's avatar: its image, or its icon on its color.
const props = withDefaults(defineProps<{ agent?: { id?: string, name?: string, avatar?: AvatarSpec }, size?: '2xs' | 'xs' | 'sm' | 'md' | 'lg' | 'xl' }>(), { size: 'sm' })
const av = computed(() => avatarOf(props.agent))
const box = computed(() => ({ '2xs': 'size-4', xs: 'size-6', sm: 'size-7', md: 'size-9', lg: 'size-12', xl: 'size-20' })[props.size])
const glyph = computed(() => ({ '2xs': 'size-2.5', xs: 'size-3.5', sm: 'size-4', md: 'size-5', lg: 'size-6', xl: 'size-10' })[props.size])
</script>

<template>
  <img v-if="av.image" :src="av.image" :alt="agent?.name" class="shrink-0 rounded-full object-cover" :class="box">
  <span v-else class="grid shrink-0 place-items-center rounded-full text-white" :class="[box, avatarColors[av.color]]" :title="agent?.name">
    <UIcon :name="av.icon" :class="glyph" />
  </span>
</template>
