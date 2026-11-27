import { FitAddon } from '@xterm/addon-fit'
import { Terminal, type ITheme } from '@xterm/xterm'
import { errorMessage, invoke, type TerminalOutput, type TerminalSession, type TerminalWindowsPty } from './native'

const INPUT_CHUNK_BYTES = 8 * 1024
const MAX_QUEUED_INPUT_BYTES = 1024 * 1024
const MAX_TERMINAL_COLS = 500
const MAX_TERMINAL_ROWS = 300
const REPLY_TIMEOUT_MS = 600
const PASTE_START = '\x1b[200~'
const PASTE_END = '\x1b[201~'
const INTERRUPT = '\x03'
const encoder = new TextEncoder()
const interruptBytes = encoder.encode(INTERRUPT)
const finishPasteAndInterrupt = encoder.encode(PASTE_END + INTERRUPT)

interface TerminalSize {
  cols: number
  rows: number
}

interface InputEntry {
  bytes: Uint8Array
  cursor: number
  bracketed: boolean
  interrupted: boolean
}

interface InputFrame {
  bytes: Uint8Array
  cursor: number
  interrupt: boolean
}

function inputFrame(input: InputEntry): InputFrame {
  if (input.interrupted) {
    const needsPasteEnd = input.cursor > 0 && input.cursor < input.bytes.length
    return {
      bytes: needsPasteEnd ? finishPasteAndInterrupt : interruptBytes,
      cursor: input.bytes.length,
      interrupt: true,
    }
  }

  let cursor = Math.min(input.cursor + INPUT_CHUNK_BYTES, input.bytes.length)
  if (input.bracketed && cursor < input.bytes.length) {
    // Keep the closer and UTF-8 code points intact if this is the last accepted frame.
    cursor = Math.min(cursor, input.bytes.length - PASTE_END.length)
    while ((input.bytes[cursor]! & 0xc0) === 0x80) cursor--
  }
  return { bytes: input.bytes.subarray(input.cursor, cursor), cursor, interrupt: false }
}

async function transportReply<T>(request: Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error('终端请求响应超时')), REPLY_TIMEOUT_MS)
  })
  try {
    return await Promise.race([request, timeout])
  } finally {
    clearTimeout(timer)
  }
}

function retryDelay(attempt: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, Math.min(40 * 2 ** attempt, 500)))
}

function terminalTheme(host: HTMLElement): ITheme {
  const styles = getComputedStyle(host)
  const color = (token: string, fallback: string) => styles.getPropertyValue(token).trim() || fallback
  const background = color('--terminal-background', '#ffffff')
  const foreground = color('--brand-color-text', '#25272b')
  const accent = color('--yss-color-primary-control-hover', '#345cc9')
  const muted = color('--brand-color-text-secondary', '#62666c')

  return {
    background,
    foreground,
    cursor: accent,
    cursorAccent: background,
    selectionBackground: color('--yss-colors-primary-bg', '#edf2ff'),
    selectionInactiveBackground: color('--brand-color-fill-secondary', '#f0f2f5'),
    selectionForeground: foreground,
    black: foreground,
    red: color('--yss-colors-error-text', '#bd3037'),
    green: '#176447',
    yellow: '#82520f',
    blue: '#294b96',
    magenta: '#713f92',
    cyan: '#14616e',
    white: '#e6eaf2',
    brightBlack: muted,
    brightRed: '#b63141',
    brightGreen: '#237756',
    brightYellow: '#8d580e',
    brightBlue: accent,
    brightMagenta: '#8944a6',
    brightCyan: '#0e7482',
    brightWhite: '#f8fafc',
  }
}

export class TerminalRenderer {
  private readonly terminal: Terminal
  private readonly fitAddon = new FitAddon()
  private sessionId: string | null = null
  private running = false
  private closePending = false
  private disposed = false
  private readonly initialOutput: Uint8Array[] = []
  private outputConsumed = 0
  private outputAcknowledged = 0
  private outputUnavailable = false
  private acknowledging = false
  private readonly inputQueue: InputEntry[] = []
  private queuedBytes = 0
  private inputEnqueued = 0
  private inputUnavailable = false
  private writing = false
  private resumeInput: (() => void) | undefined
  private requestedSize: TerminalSize | null = null
  private sentSize: TerminalSize | null = null
  private resizing = false

  constructor(
    private readonly host: HTMLElement,
    private readonly reportError: (message: string) => void,
    windowsPty?: TerminalWindowsPty | null,
  ) {
    this.terminal = new Terminal({
      cursorBlink: false,
      disableStdin: true,
      fontFamily: 'ui-monospace, Menlo, Consolas, monospace',
      fontSize: 13,
      linkHandler: { activate() {} },
      minimumContrastRatio: 4.5,
      scrollback: 2000,
      theme: terminalTheme(host),
      ...(windowsPty ? { windowsPty } : {}),
    })
    this.terminal.loadAddon(this.fitAddon)
    this.terminal.open(this.host)
    this.terminal.onData((data) => {
      if (!this.canQueueInput) return
      const current = this.inputQueue[0]
      if (data === INTERRUPT && current?.bracketed && !current.interrupted) {
        current.interrupted = true
        return
      }
      this.queueInput(encoder.encode(data), data.startsWith(PASTE_START) && data.endsWith(PASTE_END))
    })
    this.terminal.onBinary((data) => {
      this.queueInput(Uint8Array.from(Array.from(data, (char) => char.charCodeAt(0) & 0xff)))
    })
    if (!/Mac/.test(navigator.platform)) {
      this.terminal.attachCustomKeyEventHandler((event) => {
        const copy = event.type === 'keydown' && event.ctrlKey && !event.altKey
          && !event.metaKey && !event.shiftKey && event.key.toLowerCase() === 'c'
        return !copy || !this.terminal.hasSelection()
      })
    }
  }

  get id() {
    return this.sessionId
  }

  dimensions(): TerminalSize {
    if (this.host.clientWidth && this.host.clientHeight) {
      const size = this.fitAddon.proposeDimensions()
      if (size && Number.isFinite(size.cols) && Number.isFinite(size.rows)) {
        this.terminal.resize(
          Math.min(size.cols, MAX_TERMINAL_COLS),
          Math.min(size.rows, MAX_TERMINAL_ROWS),
        )
      }
    }
    return { cols: this.terminal.cols, rows: this.terminal.rows }
  }

  bind(session: TerminalSession, launchSize: TerminalSize) {
    this.sessionId = session.id
    this.sentSize = launchSize
    this.updateState(session.state)
    for (const bytes of this.initialOutput.splice(0)) this.render(bytes)
  }

  updateState(state: TerminalSession['state']) {
    this.running = state === 'running'
    this.terminal.options.disableStdin = !this.canQueueInput
    if (this.running) {
      void this.resize()
    } else {
      this.clearInput()
      this.releaseInputWait()
      this.requestedSize = null
    }
  }

  setClosePending(pending: boolean) {
    if (this.disposed) return
    this.closePending = pending
    this.terminal.options.disableStdin = !this.canQueueInput
    if (!pending) {
      this.releaseInputWait()
      void this.resize()
    }
  }

  writeOutput(data: TerminalOutput) {
    if (this.disposed) return
    const bytes = data instanceof Uint8Array ? data : new Uint8Array(data)
    if (!bytes.byteLength) return
    if (this.sessionId) this.render(bytes)
    else this.initialOutput.push(bytes)
  }

  private render(bytes: Uint8Array) {
    this.terminal.write(bytes, () => {
      if (this.disposed) return
      this.outputConsumed += bytes.byteLength
      void this.acknowledge()
    })
  }

  private async acknowledge() {
    const sessionId = this.sessionId
    if (!sessionId || this.acknowledging || this.outputUnavailable) return
    this.acknowledging = true
    let failures = 0
    try {
      while (!this.disposed && this.outputAcknowledged < this.outputConsumed) {
        const cursor = this.outputConsumed
        try {
          const reply = await transportReply(invoke('terminal_ack', { sessionId, cursor }))
          if (reply !== 'acknowledged') {
            this.outputUnavailable = true
            if (reply === 'invalidCursor' && !this.disposed) this.reportError('终端输出确认位置无效')
            return
          }
          this.outputAcknowledged = cursor
          failures = 0
        } catch {
          failures++
          if (failures === 3 && !this.disposed) this.reportError('终端输出确认中断，正在重试')
          await retryDelay(failures)
        }
      }
    } finally {
      this.acknowledging = false
    }
  }

  private get canQueueInput() {
    return this.running && !this.closePending && !this.inputUnavailable && !this.disposed
  }

  private queueInput(bytes: Uint8Array, bracketed = false) {
    if (!this.canQueueInput || !bytes.byteLength) return
    if (this.queuedBytes + bytes.byteLength > MAX_QUEUED_INPUT_BYTES) {
      this.reportError('终端输入过长，请分段粘贴')
      return
    }
    this.inputQueue.push({ bytes, cursor: 0, bracketed, interrupted: false })
    this.queuedBytes += bytes.byteLength
    void this.writeInput()
  }

  private clearInput() {
    this.inputQueue.length = 0
    this.queuedBytes = 0
  }

  private releaseInputWait() {
    const resume = this.resumeInput
    this.resumeInput = undefined
    resume?.()
  }

  private async writeInput() {
    const sessionId = this.sessionId
    if (!sessionId || this.writing) return
    this.writing = true
    try {
      while (this.running && !this.inputUnavailable && !this.disposed) {
        const input = this.inputQueue[0]
        if (!input) break
        const frame = inputFrame(input)
        const outcome = await this.sendInputFrame(sessionId, input, frame)
        if (outcome === 'closed') break
        if (outcome === 'interrupted' || this.inputQueue[0] !== input) continue
        input.cursor = frame.cursor
        if (input.cursor === input.bytes.length && (!input.interrupted || frame.interrupt)) {
          this.inputQueue.shift()
          this.queuedBytes -= input.bytes.byteLength
        }
      }
    } finally {
      this.writing = false
    }
  }

  private async sendInputFrame(sessionId: string, input: InputEntry, frame: InputFrame) {
    const offset = this.inputEnqueued
    const bytes = Array.from(frame.bytes)
    let failures = 0
    let rejectedByQueue = false
    let unknownDelivery = false
    while (this.running && !this.disposed) {
      if (this.closePending) {
        // Keep the pending frame and its delivery history across a failed close request.
        await new Promise<void>((resolve) => { this.resumeInput = resolve })
        continue
      }
      // Only replace a declined paste frame if no earlier attempt could still arrive.
      if (input.interrupted && !frame.interrupt && rejectedByQueue && !unknownDelivery) {
        return 'interrupted'
      }
      try {
        const reply = await transportReply(invoke('terminal_write', { sessionId, offset, bytes }))
        if (reply.status === 'accepted' && reply.nextOffset === offset + bytes.length) {
          this.inputEnqueued = reply.nextOffset
          return 'accepted'
        }
        if (reply.status !== 'queueFull') {
          if (!this.disposed && this.running) {
            if (reply.status === 'invalidInput') this.reportError('终端输入超出允许范围')
            else if (reply.status !== 'closed') this.reportError('终端输入顺序无效，请重新打开会话')
            this.inputUnavailable = true
            this.terminal.options.disableStdin = true
          }
          return 'closed'
        }
        rejectedByQueue = true
      } catch {
        unknownDelivery = true
        failures++
        if (failures === 3 && this.running && !this.disposed) this.reportError('终端输入连接中断，正在重试')
      }
      await retryDelay(failures)
    }
    return 'closed'
  }

  fit() {
    if (this.disposed) return
    const size = this.dimensions()
    this.requestedSize = size
    void this.resize()
  }

  private async resize() {
    const sessionId = this.sessionId
    if (!sessionId || this.resizing) return
    this.resizing = true
    let failures = 0
    try {
      while (this.running && !this.closePending && !this.disposed && this.requestedSize) {
        const size = this.requestedSize
        if (this.sentSize?.cols === size.cols && this.sentSize.rows === size.rows) break
        try {
          await invoke('terminal_resize', { sessionId, ...size })
          this.sentSize = size
          failures = 0
        } catch (error) {
          // A lost reply can leave the native PTY at the failed request's size.
          this.sentSize = null
          if (!this.running || this.disposed) break
          failures++
          if (failures === 3) this.reportError(errorMessage(error, '终端尺寸调整失败'))
          await retryDelay(failures)
        }
      }
    } finally {
      this.resizing = false
    }
  }

  focus() {
    this.terminal.focus()
  }

  dispose() {
    this.disposed = true
    this.clearInput()
    this.releaseInputWait()
    this.requestedSize = null
    this.initialOutput.length = 0
    this.terminal.dispose()
  }
}
