export interface TerminalProject {
  id: string
  name: string
  rootPath: string
}

export interface TerminalSession {
  id: string
  projectId: string
  rootPath: string
  state: 'running' | 'closing' | 'cleanupPending' | 'exited' | 'failed'
  exitCode?: number | null
  error?: string | null
}

export interface TerminalWindowsPty {
  backend: 'conpty'
  buildNumber?: number
}

export interface TerminalContext {
  connectionId: string
  selectedProjectId?: string | null
  projects: TerminalProject[]
  sessions: TerminalSession[]
  windowsPty?: TerminalWindowsPty | null
}

export type TerminalOutput = ArrayBuffer | Uint8Array

export type TerminalPanelAction = 'toggle-maximize' | 'hide' | 'read' | 'resize'
export type TerminalResizeIntent =
  | { kind: 'width' | 'step'; pixels: number }
  | { kind: 'minimum' | 'maximum' }

export interface TerminalPanelLayout {
  revision: number
  splitEpoch: number
  mode: 'split' | 'maximized' | 'hidden'
  temporary: boolean
  ratio: number
  terminalWidth: number
  minRatio: number | null
  maxRatio: number | null
}

type TerminalAckResult = 'acknowledged' | 'unavailable' | 'invalidCursor'
type TerminalWriteResult =
  | { status: 'accepted'; nextOffset: number }
  | { status: 'queueFull' | 'closed' | 'invalidOffset' | 'invalidInput' }

interface NativeChannel<T> {
  onmessage: (data: T) => void
}

interface TerminalCommands {
  terminal_ready: { args: undefined; result: void }
  terminal_context: { args: undefined; result: TerminalContext }
  terminal_start: {
    args: {
      connectionId: string
      projectId: string
      cols: number
      rows: number
      output: NativeChannel<TerminalOutput>
    }
    result: TerminalSession
  }
  terminal_write: {
    args: { sessionId: string; offset: number; bytes: number[] }
    result: TerminalWriteResult
  }
  terminal_resize: { args: { sessionId: string; cols: number; rows: number }; result: void }
  terminal_ack: { args: { sessionId: string; cursor: number }; result: TerminalAckResult }
  terminal_close: { args: { connectionId: string; sessionId: string }; result: void }
  terminal_panel_layout: {
    args: { action: TerminalPanelAction; resize?: TerminalResizeIntent; splitEpoch?: number; commit?: boolean }
    result: TerminalPanelLayout
  }
}

interface TerminalEvents {
  'terminal:context-changed': null
  'terminal:session-changed': null
  'terminal:panel-layout': TerminalPanelLayout
}

interface TerminalEvent<T> {
  payload: T
}

interface NativeTauri {
  core: {
    invoke<T>(command: string, args?: object): Promise<T>
    Channel: new <T>(onmessage: (data: T) => void) => NativeChannel<T>
  }
  event: {
    listen<T>(event: keyof TerminalEvents, listener: (event: TerminalEvent<T>) => void): Promise<() => void>
  }
}

function tauri(): NativeTauri {
  const api = (window as Window & { __TAURI__?: NativeTauri }).__TAURI__
  if (!api?.core || !api.event) throw new Error('请从 YSS 工作台打开终端')
  return api
}

export function invoke<K extends keyof TerminalCommands>(
  command: K,
  args: TerminalCommands[K]['args'],
): Promise<TerminalCommands[K]['result']> {
  return tauri().core.invoke(command, args)
}

export function outputChannel(onOutput: (data: TerminalOutput) => void) {
  return new (tauri().core.Channel)(onOutput)
}

export function listen<K extends keyof TerminalEvents>(
  event: K,
  listener: (event: TerminalEvent<TerminalEvents[K]>) => void,
) {
  return tauri().event.listen(event, listener)
}

export function errorMessage(error: unknown, fallback: string) {
  if (error instanceof Error && error.message) return error.message
  if (typeof error === 'string' && error) return error
  return fallback
}
