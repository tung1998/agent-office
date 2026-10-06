// useDraft keeps a form's draft apart from the server's data (ADR-072): it is
// filled once, and again from newer data only while the person has not
// touched it. Newer data while they edit makes it stale: the page says so
// ("changed elsewhere · Reload") and never overwrites what they typed.
// After a save, reset() takes the server's data again.
export function useDraft<T>(source: Ref<T | null | undefined> | (() => T | null | undefined), form: object | Ref<unknown>, fill: (d: T) => void) {
  const snap = () => JSON.stringify(isRef(form) ? form.value : toRaw(form))
  const filled = ref<string | null>(null) // the form as last filled
  let base = '' // the server's data it was filled from
  const latest = shallowRef<T | null>(null)
  const stale = ref(false)
  const tick = ref(0) // the form's edits (deep), for dirty
  watch(() => (isRef(form) ? form.value : form), () => { tick.value++ }, { deep: true })
  const dirty = computed(() => { void tick.value; return filled.value !== null && snap() !== filled.value })
  // edits undone by hand: nothing to lose, take the newest
  watch(dirty, (d) => { if (!d && stale.value && latest.value != null) apply(latest.value) })
  function apply(d: T) {
    fill(d)
    filled.value = snap()
    base = JSON.stringify(d)
    stale.value = false
  }
  watch(source, (d) => {
    if (d == null) return
    const first = latest.value === null
    latest.value = d
    if (first || !dirty.value) apply(d)
    else if (JSON.stringify(d) !== base) stale.value = true // changed on the server while edited here
  }, { immediate: true })
  // reset: the server's latest, what was typed dropped (Reload, or after Save)
  function reset() {
    const d = typeof source === 'function' ? source() : source.value
    if (d != null) latest.value = d
    if (latest.value != null) apply(latest.value)
  }
  return { dirty, stale, reset }
}
