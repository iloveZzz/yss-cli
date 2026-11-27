interface WorkbenchTerminalLayout {
  revision?: number
  mode: 'hidden' | 'split' | 'maximized'
  railViewport?: { canExpand: boolean; defaultCollapsed: boolean }
}

type DesktopWindow = Window & { __YSS_DESKTOP_TERMINAL_URL__?: string; __YSS_TERMINAL_LAYOUT__?: unknown }

function desktopTerminalControl() {
  const desktop = window as DesktopWindow
  const address = desktop.__YSS_DESKTOP_TERMINAL_URL__
  if (desktop !== desktop.top || !address) return
  try {
    const url = new URL(address)
    if (url.origin === location.origin && !url.search && !url.hash) return url
  } catch {
    // The shell bridge is unavailable when the injected address is malformed.
  }
}

export function terminalPanelOpenUrl() {
  const control = desktopTerminalControl()
  control?.searchParams.set('action', 'open')
  return control?.href
}

export function reportTerminalRailWidth(cssPixels: number) {
  if (!Number.isInteger(cssPixels) || cssPixels < 0 || cssPixels > 512) return false
  const control = desktopTerminalControl()
  if (!control) return false
  control.searchParams.set('railWidth', cssPixels.toString())
  window.location.assign(control.href)
  return true
}

export function workbenchTerminalLayout(value: unknown): WorkbenchTerminalLayout | undefined {
  if (!value || typeof value !== 'object') return
  const { revision, mode, railViewport, visible, windowWidth } = value as Partial<WorkbenchTerminalLayout> & {
    visible?: unknown
    windowWidth?: unknown
  }
  if (mode !== 'hidden' && mode !== 'split' && mode !== 'maximized') return
  if (revision !== undefined) {
    if (!Number.isSafeInteger(revision) || revision < 1) return
    if (typeof railViewport?.canExpand !== 'boolean' || typeof railViewport.defaultCollapsed !== 'boolean') return
    return { revision, mode, railViewport }
  }
  // A workbench served by a different CLI version can be connected to an older desktop shell.
  if (visible !== (mode !== 'hidden')) return
  if (typeof windowWidth !== 'number' || !Number.isFinite(windowWidth) || windowWidth <= 0) return { mode }
  return { mode, railViewport: { canExpand: windowWidth >= 720, defaultCollapsed: windowWidth < 880 } }
}

export function initialTerminalLayout() {
  return workbenchTerminalLayout((window as DesktopWindow).__YSS_TERMINAL_LAYOUT__)
}
