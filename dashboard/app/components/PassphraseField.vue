<script setup lang="ts">
// The file password of an encrypted export (ADR-274/276). Making a file asks
// for it twice and says at once when the two differ; opening one asks once.
const props = defineProps<{ confirm?: boolean }>()
const value = defineModel<string>({ default: '' })
const valid = defineModel<boolean>('valid', { default: false })
const { t } = useLang()
const MIN = 12

const again = ref('')
const short = computed(() => value.value.length > 0 && [...value.value].length < MIN)
const differs = computed(() => props.confirm && again.value.length > 0 && again.value !== value.value)
watchEffect(() => {
  valid.value = [...value.value].length >= MIN && (!props.confirm || again.value === value.value)
})
</script>

<template>
  <div class="grid gap-3 sm:grid-cols-2">
    <UFormField :label="t('transfer.password')" :hint="t('transfer.passwordMin', { n: MIN })" :error="short ? t('transfer.passwordShort', { n: MIN }) : undefined">
      <UInput v-model="value" type="password" :autocomplete="confirm ? 'new-password' : 'current-password'" class="w-full" />
    </UFormField>
    <UFormField v-if="confirm" :label="t('transfer.passwordAgain')" :error="differs ? t('transfer.passwordDiffers') : undefined">
      <UInput v-model="again" type="password" autocomplete="new-password" class="w-full" />
    </UFormField>
    <p v-else class="self-end pb-2 text-xs text-(--ui-text-muted)">{{ t('transfer.passwordOpenHint') }}</p>
  </div>
</template>
