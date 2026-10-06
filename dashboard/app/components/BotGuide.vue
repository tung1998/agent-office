<script setup lang="ts">
// The numbered steps to make a Telegram / Discord bot and find the ids.
const props = defineProps<{ kind: 'telegram' | 'discord', open?: boolean, plain?: boolean }>()
const { t } = useLang()
const steps = computed<{ text: string, link?: { label: string, url: string } }[]>(() => props.kind === 'discord'
  ? [
      { text: t('channels.gd1'), link: { label: 'Discord Developer Portal', url: 'https://discord.com/developers/applications' } },
      { text: t('channels.gd2') },
      { text: t('channels.gd3') },
      { text: t('channels.gd4') }
    ]
  : [
      { text: t('channels.gt1'), link: { label: '@BotFather', url: 'https://t.me/BotFather' } },
      { text: t('channels.gt2') },
      { text: t('channels.gt3'), link: { label: '@userinfobot', url: 'https://t.me/userinfobot' } },
      { text: t('channels.gt4') }
    ])
</script>

<template>
  <ol v-if="plain" class="space-y-3">
    <li v-for="(s, i) in steps" :key="i" class="flex gap-2.5 text-sm">
      <span class="flex size-5 shrink-0 items-center justify-center rounded-full bg-primary/15 text-xs font-medium text-primary">{{ i + 1 }}</span>
      <span class="min-w-0">
        {{ s.text }}
        <a v-if="s.link" :href="s.link.url" target="_blank" rel="noopener" class="ms-1 inline-flex items-center gap-0.5 text-primary hover:underline">
          {{ s.link.label }}<UIcon name="i-lucide-external-link" class="size-3" />
        </a>
      </span>
    </li>
  </ol>
  <details v-else class="group rounded-lg border border-(--ui-border) bg-(--ui-bg-elevated)/40 p-3" :open="open">
    <summary class="flex cursor-pointer list-none items-center gap-2 text-sm font-medium">
      <UIcon name="i-lucide-chevron-right" class="size-4 transition group-open:rotate-90" />
      {{ kind === 'discord' ? t('channels.guideDiscord') : t('channels.guideTelegram') }}
    </summary>
    <ol class="mt-3 space-y-2.5">
      <li v-for="(s, i) in steps" :key="i" class="flex gap-2.5 text-sm">
        <span class="flex size-5 shrink-0 items-center justify-center rounded-full bg-primary/15 text-xs font-medium text-primary">{{ i + 1 }}</span>
        <span class="min-w-0">
          {{ s.text }}
          <a v-if="s.link" :href="s.link.url" target="_blank" rel="noopener" class="ms-1 inline-flex items-center gap-0.5 text-primary hover:underline">
            {{ s.link.label }}<UIcon name="i-lucide-external-link" class="size-3" />
          </a>
        </span>
      </li>
    </ol>
  </details>
</template>
