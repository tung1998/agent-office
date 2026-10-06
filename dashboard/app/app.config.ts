export default defineAppConfig({
  ui: {
    colors: {
      primary: 'indigo',
      neutral: 'zinc'
    },
    // a long title wraps before the close button instead of running under it
    modal: { slots: { title: 'pe-8', description: 'pe-8' } },
    slideover: { slots: { title: 'pe-8', description: 'pe-8' } }
  }
})
