// The theme kept in this browser goes on before the first paint (useTheme
// then checks the account's copy once the person is known).
export default defineNuxtPlugin(() => {
  try {
    paintTheme(readSavedTheme())
  } catch { /* the default look */ }
})
