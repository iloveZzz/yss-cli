import { computed, onMounted, onScopeDispose, shallowRef } from 'vue'
import { errorMessage, invoke, listen, type TerminalPanelAction, type TerminalPanelLayout, type TerminalResizeIntent } from './native'

interface ResizeRequest {
  resize: TerminalResizeIntent
  splitEpoch: number
  commit: boolean
}

interface Drag {
  id: number
  origin: number
  initialWidth: number
  width: number
}

export function useTerminalPanel() {
  const layout = shallowRef<TerminalPanelLayout>()
  const actionPending = shallowRef(false)
  const error = shallowRef('')
  const dragging = shallowRef(false)
  const pendingResizes: ResizeRequest[] = []
  let resizeInFlight: Promise<void> | undefined
  let drag: Drag | undefined
  let frame = 0
  let disposed = false
  let unlisten: (() => void) | undefined

  const visible = computed(() => layout.value !== undefined && layout.value.mode !== 'hidden')
  const canResize = computed(() => layout.value?.mode === 'split' && !actionPending.value)
  const maximizeLabel = computed(() => {
    if (layout.value?.mode !== 'maximized') return '最大化终端面板'
    return layout.value.temporary ? '当前窗口较窄，暂时无法恢复并排' : '恢复并排'
  })

  function acceptLayout(value: TerminalPanelLayout) {
    if (disposed || (layout.value && value.revision <= layout.value.revision)) return
    if (value.splitEpoch !== layout.value?.splitEpoch) {
      drag = undefined
      dragging.value = false
      pendingResizes.length = 0
    }
    layout.value = value
  }

  async function changeLayout(action: Exclude<TerminalPanelAction, 'resize'>) {
    if (disposed || actionPending.value || (action === 'toggle-maximize' && (!layout.value || layout.value.temporary))) return
    actionPending.value = true
    error.value = ''
    try {
      await resizeInFlight
      while (pendingResizes.length && !disposed) await flushResize()
      if (disposed) return
      if (!unlisten) {
        const stop = await listen('terminal:panel-layout', ({ payload }) => acceptLayout(payload))
        if (disposed) {
          stop()
          return
        }
        unlisten = stop
      }
      acceptLayout(await invoke('terminal_panel_layout', { action }))
    } catch (cause) {
      if (!disposed) error.value = errorMessage(cause, '无法更新终端面板')
    } finally {
      actionPending.value = false
      scheduleResize()
    }
  }

  function scheduleResize() {
    if (disposed || frame || resizeInFlight || !canResize.value || !pendingResizes.length) return
    frame = window.requestAnimationFrame(() => {
      frame = 0
      if (canResize.value) void flushResize()
    })
  }

  function flushResize() {
    const next = pendingResizes.shift()
    if (!next || disposed || layout.value?.mode !== 'split' || layout.value.splitEpoch !== next.splitEpoch) return
    resizeInFlight = applyResize(next).finally(() => {
      resizeInFlight = undefined
      scheduleResize()
    })
    return resizeInFlight
  }

  async function applyResize(next: ResizeRequest) {
    try {
      acceptLayout(await invoke('terminal_panel_layout', { action: 'resize', ...next }))
      if (!disposed) error.value = ''
    } catch (cause) {
      if (!disposed) error.value = errorMessage(cause, '无法调整终端面板宽度')
    }
  }

  function resize(resize: TerminalResizeIntent, commit: boolean) {
    const current = layout.value
    if (!current || !canResize.value) return
    const previous = pendingResizes.at(-1)
    if (previous && resize.kind === 'width' && previous.resize.kind === 'width' && !previous.commit) {
      pendingResizes.pop()
    } else if (previous && resize.kind === 'step' && previous.resize.kind === 'step'
      && Math.sign(resize.pixels) === Math.sign(previous.resize.pixels)) {
      pendingResizes.pop()
      resize = { kind: 'step', pixels: resize.pixels + previous.resize.pixels }
    }
    pendingResizes.push({ resize, commit, splitEpoch: current.splitEpoch })
    scheduleResize()
  }

  function startDrag(event: PointerEvent) {
    const current = layout.value
    if (!canResize.value || !current || !event.isPrimary || event.button !== 0 || drag) return
    if (!(event.currentTarget instanceof HTMLElement)) return
    event.preventDefault()
    event.currentTarget.focus({ preventScroll: true })
    event.currentTarget.setPointerCapture(event.pointerId)
    drag = {
      id: event.pointerId,
      origin: event.screenX,
      initialWidth: current.terminalWidth,
      width: current.terminalWidth,
    }
    dragging.value = true
  }

  function moveDrag(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.id) return
    // The native WebView moves with the separator, so its clientX is not stable.
    drag.width = drag.initialWidth + drag.origin - event.screenX
    resize({ kind: 'width', pixels: drag.width }, false)
  }

  function finishDrag(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.id) return
    if (event.type === 'pointerup') {
      drag.width = drag.initialWidth + drag.origin - event.screenX
    }
    resize({ kind: 'width', pixels: drag.width }, true)
    drag = undefined
    dragging.value = false
  }

  function resizeWithKey(event: KeyboardEvent) {
    if (!canResize.value || drag || event.isComposing || event.altKey || event.ctrlKey || event.metaKey) return
    const step = event.shiftKey ? 80 : 24
    const keys: Partial<Record<string, TerminalResizeIntent>> = {
      ArrowLeft: { kind: 'step', pixels: step },
      ArrowRight: { kind: 'step', pixels: -step },
      Home: { kind: 'minimum' },
      End: { kind: 'maximum' },
    }
    const next = Object.hasOwn(keys, event.key) ? keys[event.key] : undefined
    if (!next) return
    event.preventDefault()
    resize(next, true)
  }

  onMounted(() => void changeLayout('read'))
  onScopeDispose(() => {
    disposed = true
    unlisten?.()
    if (frame) window.cancelAnimationFrame(frame)
    pendingResizes.length = 0
  })

  return { layout, visible, canResize, actionPending, error, dragging, maximizeLabel, changeLayout,
    startDrag, moveDrag, finishDrag, resizeWithKey }
}
