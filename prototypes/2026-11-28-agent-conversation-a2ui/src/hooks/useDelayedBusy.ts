import { shallowRef, watch, type WatchSource } from 'vue'

export function useDelayedBusy(active: WatchSource<boolean>, delay: WatchSource<number>) {
  const visible = shallowRef(false)
  watch([active, delay], ([pending, wait], _previous, onCleanup) => {
    visible.value = pending && wait <= 0
    if (!pending || wait <= 0) return
    const timer = setTimeout(() => { visible.value = true }, wait)
    onCleanup(() => clearTimeout(timer))
  }, { immediate: true, flush: 'sync' })
  return visible
}
