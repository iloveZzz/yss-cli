import { watch, type Ref } from 'vue'

// A dialog with several entry buttons remembers the one that actually opened it.
export function useDialogFocus(open: Ref<boolean>) {
  let previous: HTMLElement | null = null
  watch(open, (visible) => {
    if (visible && document.activeElement instanceof HTMLElement) previous = document.activeElement
  }, { flush: 'sync' })
  return (event: Event) => {
    if (!previous?.isConnected) return
    const target = previous
    previous = null
    event.preventDefault()
    target.focus({ preventScroll: true })
  }
}
