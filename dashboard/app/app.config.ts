export default defineAppConfig({
  ui: {
    colors: {
      primary: 'indigo',
      neutral: 'zinc'
    },
    dashboardPanel: { slots: { root: 'office-panel' } },
    dashboardNavbar: { slots: { root: 'office-header' } },
    card: { slots: { root: 'office-card' } },
    input: { slots: { base: 'office-input' } },
    textarea: { slots: { base: 'office-input' } },
    popover: { slots: { content: 'office-popup' } },
    dropdownMenu: { slots: { content: 'office-popup' } },
    contextMenu: { slots: { content: 'office-popup' } },
    // a long title wraps before the close button instead of running under it
    modal: { slots: { content: 'office-popup', title: 'pe-8', description: 'pe-8' } },
    slideover: { slots: { content: 'office-popup', title: 'pe-8', description: 'pe-8' } },
    // dropdown content width defaults to the trigger button width (w-(--reka-*-trigger-width)),
    // which shrinks to the icon on mobile when the trigger label is hidden — min-w-fit wins over width
    select: { slots: { base: 'office-input', content: 'office-popup min-w-fit max-w-[calc(100vw-1rem)]' } },
    selectMenu: { slots: { base: 'office-input', content: 'office-popup min-w-fit max-w-[calc(100vw-1rem)]' } },
    inputMenu: { slots: { base: 'office-input', content: 'office-popup min-w-fit max-w-[calc(100vw-1rem)]' } },
    // ⓘ explanations are sentences: wrap them instead of cutting to one line
    tooltip: { slots: { content: 'h-auto max-w-sm', text: 'whitespace-normal! overflow-visible' } }
  }
})
