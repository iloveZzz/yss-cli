/** Combine a caller cancellation and deadline on older system WebViews too. */
export function withTimeout(signal: AbortSignal, milliseconds: number): {
  signal: AbortSignal
  dispose: () => void
} {
  if (typeof AbortSignal.any === 'function' && typeof AbortSignal.timeout === 'function') {
    return { signal: AbortSignal.any([signal, AbortSignal.timeout(milliseconds)]), dispose: () => {} }
  }

  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const cleanup = () => {
    if (timer !== undefined) clearTimeout(timer)
    signal.removeEventListener('abort', abortFromCaller)
    controller.signal.removeEventListener('abort', cleanup)
  }
  const abortFromCaller = () => controller.abort(signal.reason)
  if (signal.aborted) abortFromCaller()
  else {
    signal.addEventListener('abort', abortFromCaller, { once: true })
    timer = setTimeout(() => controller.abort(new DOMException('请求超时', 'TimeoutError')), milliseconds)
    controller.signal.addEventListener('abort', cleanup, { once: true })
  }
  return { signal: controller.signal, dispose: cleanup }
}
