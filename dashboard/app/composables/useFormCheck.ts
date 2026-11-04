// useFormCheck keeps a form's submit button pressable: pressing it with a
// field wrong or missing marks every such field (UFormField :error) and
// focuses the first one. rules() maps a field key to its message, or a falsy
// value when the field is fine; keys run in the form's order. Each field's
// UFormField carries data-field="<key>" so the focus can find it.
export function useFormCheck(rules: () => Record<string, string | false | null | undefined>) {
  const tried = ref(false)
  // once tried, a mark goes away as soon as its field is put right
  const errors = computed<Record<string, string>>(() => {
    if (!tried.value) return {}
    return Object.fromEntries(Object.entries(rules()).filter((e): e is [string, string] => !!e[1]))
  })
  function check(): boolean {
    tried.value = true
    const first = Object.keys(errors.value)[0]
    if (first) focusField(first)
    return !first
  }
  function reset() { tried.value = false }
  return { errors, check, reset }
}

export function focusField(key: string) {
  nextTick(() => {
    // a modal sits at the end of the page: the last shown match is the open form's
    const box = [...document.querySelectorAll<HTMLElement>(`[data-field="${key}"]`)].reverse().find(el => el.offsetParent !== null)
    if (!box) return
    box.scrollIntoView({ block: 'center', behavior: 'smooth' })
    box.querySelector<HTMLElement>('input:not([type=hidden]):not([disabled]), textarea:not([disabled]), select, button:not([disabled]), [tabindex]:not([tabindex="-1"])')
      ?.focus({ preventScroll: true })
  })
}
