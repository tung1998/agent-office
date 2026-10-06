<script setup lang="ts">
// Pick an agent's avatar: a color and an icon, or upload a picture.
const model = defineModel<AvatarSpec>({ default: () => ({}) })
const props = defineProps<{ agent: { id?: string, name?: string } }>()
const { t } = useLang()
const toast = useToast()
const input = ref<HTMLInputElement | null>(null)
const current = computed(() => avatarOf({ ...props.agent, avatar: model.value }))

function set(p: AvatarSpec) {
  model.value = { color: current.value.color, icon: current.value.icon, ...p, image: p.image }
}
async function upload(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (!f) return
  try {
    model.value = { image: await resizeImage(f) }
  } catch {
    toast.add({ title: t('avatar.badImage'), color: 'error' })
  }
  if (input.value) input.value.value = ''
}
function shuffle() {
  const colors = Object.keys(avatarColors)
  model.value = { color: colors[Math.floor(Math.random() * colors.length)], icon: avatarIcons[Math.floor(Math.random() * avatarIcons.length)] }
}
</script>

<template>
  <div class="flex flex-wrap items-start gap-4">
    <AgentAvatar :agent="{ ...agent, avatar: model }" size="xl" />
    <div class="min-w-0 flex-1 space-y-3">
      <div class="flex flex-wrap gap-2">
        <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-upload" :label="t('avatar.upload')" @click="input?.click()" />
        <UButton size="xs" color="neutral" variant="outline" icon="i-lucide-shuffle" :label="t('avatar.shuffle')" @click="shuffle" />
        <UButton v-if="model.color || model.icon || model.image" size="xs" color="neutral" variant="ghost" icon="i-lucide-rotate-ccw" :label="t('avatar.reset')" @click="model = {}" />
      </div>
      <div v-if="!model.image" class="space-y-2">
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="(cls, c) in avatarColors" :key="c" type="button" class="size-6 rounded-full ring-offset-2 ring-offset-(--ui-bg)" :class="[cls, current.color === c && 'ring-2 ring-(--ui-text)']"
            :aria-label="c" @click="set({ color: c })"
          />
        </div>
        <div class="flex flex-wrap gap-1">
          <button
            v-for="i in avatarIcons" :key="i" type="button" class="grid size-8 place-items-center rounded-md hover:bg-(--ui-bg-elevated)"
            :class="current.icon === i && 'bg-(--ui-bg-elevated) ring-1 ring-primary'" :aria-label="i" @click="set({ icon: i })"
          >
            <UIcon :name="i" class="size-4" />
          </button>
        </div>
      </div>
      <p v-else class="text-xs text-(--ui-text-muted)">{{ t('avatar.imageHint') }}</p>
    </div>
    <input ref="input" type="file" accept="image/png,image/jpeg,image/webp,image/gif" class="hidden" @change="upload">
  </div>
</template>
