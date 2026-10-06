// saveError tells why a save failed; someone else's change since the page
// was opened (409, ADR-072) says so and offers to reload.
export function useSaveError() {
  const toast = useToast()
  const { t } = useLang()
  return (e: unknown, reload?: () => unknown) => {
    const d = (e as { data?: { code?: string } })?.data
    if (d?.code === 'conflict') {
      toast.add({ title: t('draft.conflict'), description: apiError(e), color: 'warning',
        actions: reload ? [{ label: t('draft.reload'), onClick: () => { void reload() } }] : undefined })
      return
    }
    toast.add({ title: apiError(e), color: 'error' })
  }
}
