import {
  CreateSurfaceMessageSchema,
  UpdateComponentsMessageSchema,
  UpdateDataModelMessageSchema,
} from '@a2ui/web_core/v1_0'

const catalogId = 'https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json'
const maxFenceBytes = 32 * 1024
const maxMessages = 16
const maxComponents = 64
const maxComponentDepth = 8
const maxJsonDepth = 16
const maxJsonEntries = 256
const maxText = 4_000
const maxLabel = 160
const identifier = /^[_a-zA-Z][_a-zA-Z0-9]{0,63}$/
const reservedKeys = new Set(['__proto__', 'prototype', 'constructor'])

type RecordValue = Record<string, unknown>
type DynamicText = string | string[]
type TextVariant = 'body' | 'caption'
type ButtonVariant = 'default' | 'primary' | 'borderless'
type Action =
  | { kind: 'select'; label: DynamicText; text: DynamicText }
  | { kind: 'details'; text: DynamicText }

type Definition =
  | { kind: 'Card' | 'Column'; children: string[] }
  | { kind: 'Text'; text: DynamicText; variant: TextVariant }
  | { kind: 'Button'; child: string; action: Action; variant: ButtonVariant }

export type AssistantA2uiSelection = {
  id: string
  kind: 'select'
  buttonLabel: string
  optionLabel: string
  text: string
  variant: ButtonVariant
}

export type AssistantA2uiNode =
  | { id: string; kind: 'Card' | 'Column'; children: AssistantA2uiNode[] }
  | { id: string; kind: 'Text'; text: string; caption: boolean }
  | { id: string; kind: 'details'; buttonLabel: string; text: string }
  | AssistantA2uiSelection

type AssistantA2uiSurface = {
  id: string
  root: AssistantA2uiNode
  options: ReadonlyMap<string, AssistantA2uiSelection>
}

type SurfaceData = {
  id: string
  model: RecordValue
  components: Map<string, Definition>
}

type ParseResult = { surface: AssistantA2uiSurface | null; invalidUpdate: boolean }

class IncompleteSurface extends Error {}

function isRecord(value: unknown): value is RecordValue {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function requireValid(condition: unknown): asserts condition {
  if (!condition) throw new Error('Invalid A2UI message')
}

function hasOnlyKeys(value: RecordValue, keys: readonly string[]) {
  return Object.keys(value).every(key => keys.includes(key))
}

function validateJson(value: unknown, depth = 0) {
  requireValid(depth <= maxJsonDepth)
  if (Array.isArray(value)) {
    requireValid(value.length <= maxJsonEntries)
    for (const child of value) validateJson(child, depth + 1)
  } else if (isRecord(value)) {
    const entries = Object.entries(value)
    requireValid(entries.length <= maxJsonEntries)
    for (const [key, child] of entries) {
      requireValid(!reservedKeys.has(key))
      validateJson(child, depth + 1)
    }
  } else if (typeof value === 'number') {
    requireValid(Number.isFinite(value))
  }
}

function readId(value: unknown): string {
  requireValid(typeof value === 'string' && identifier.test(value))
  return value
}

function readPointer(path: unknown): string[] {
  requireValid(typeof path === 'string' && path.startsWith('/') && path.length <= 256)
  if (path === '/') return []
  const tokens = path.slice(1).split('/')
  requireValid(tokens.length <= maxComponentDepth)
  return tokens.map(token => {
    requireValid(!/~(?![01])/.test(token))
    const key = token.replaceAll('~1', '/').replaceAll('~0', '~')
    requireValid(!reservedKeys.has(key))
    return key
  })
}

function readText(value: unknown): DynamicText {
  if (typeof value === 'string') {
    requireValid(value.length <= maxText)
    return value
  }
  requireValid(isRecord(value) && hasOnlyKeys(value, ['@path']))
  return readPointer(value['@path'])
}

function readAction(action: unknown): Action {
  requireValid(isRecord(action) && hasOnlyKeys(action, ['event']))
  const event = action.event
  requireValid(isRecord(event) && hasOnlyKeys(event, ['name', 'context']) && isRecord(event.context))
  const { name, context } = event
  if (name === 'select') {
    requireValid(hasOnlyKeys(context, ['label', 'text']))
    return { kind: name, label: readText(context.label), text: readText(context.text) }
  }
  requireValid(name === 'details' && hasOnlyKeys(context, ['text']))
  return { kind: name, text: readText(context.text) }
}

function readDefinition(node: RecordValue): Definition {
  const fields = ['id', 'component']
  switch (node.component) {
    case 'Card':
      requireValid(hasOnlyKeys(node, [...fields, 'child']))
      return { kind: node.component, children: [readId(node.child)] }
    case 'Column':
      requireValid(hasOnlyKeys(node, [...fields, 'children']) && Array.isArray(node.children) &&
        node.children.length > 0 && node.children.length <= maxComponents)
      return { kind: node.component, children: node.children.map(readId) }
    case 'Text': {
      const variant = node.variant === undefined ? 'body' : node.variant
      requireValid(hasOnlyKeys(node, [...fields, 'text', 'variant']) &&
        (variant === 'body' || variant === 'caption'))
      return { kind: node.component, text: readText(node.text), variant }
    }
    case 'Button': {
      const variant = node.variant === undefined ? 'default' : node.variant
      requireValid(hasOnlyKeys(node, [...fields, 'child', 'action', 'variant']) &&
        (variant === 'default' || variant === 'primary' || variant === 'borderless'))
      return { kind: node.component, child: readId(node.child), action: readAction(node.action), variant }
    }
    default: throw new Error('Unsupported A2UI component')
  }
}

function updateComponents(old: Map<string, Definition>, components: readonly RecordValue[]) {
  const next = new Map(old)
  const updated = new Set<string>()
  requireValid(components.length <= maxComponents)
  for (const node of components) {
    const id = readId(node.id)
    requireValid(!updated.has(id))
    updated.add(id)
    next.set(id, readDefinition(node))
  }
  requireValid(next.size <= maxComponents)
  return next
}

function getOwnValue(value: unknown, key: string): unknown {
  if (Array.isArray(value)) {
    if (/^(0|[1-9][0-9]{0,2})$/.test(key)) return value[Number(key)]
    return undefined
  }
  if (isRecord(value) && Object.hasOwn(value, key)) return value[key]
  return undefined
}

function updateValue(old: unknown, tokens: string[], value: unknown): unknown {
  const [key, ...remaining] = tokens
  if (key === undefined) return value
  const next = updateValue(getOwnValue(old, key), remaining, value)
  if (Array.isArray(old)) {
    requireValid(/^(0|[1-9][0-9]{0,2})$/.test(key))
    const index = Number(key)
    requireValid(index <= old.length && index < maxJsonEntries)
    const updated = [...old]
    if (next === null) updated.splice(index, 1)
    else updated[index] = next
    return updated
  }
  requireValid(old === undefined || isRecord(old))
  const updated = { ...old }
  if (next === null) delete updated[key]
  else updated[key] = next
  requireValid(Object.keys(updated).length <= maxJsonEntries)
  return updated
}

function applyMessage(previous: SurfaceData | null, message: unknown): SurfaceData {
  validateJson(message)
  requireValid(isRecord(message))
  if (Object.hasOwn(message, 'createSurface')) {
    requireValid(!previous)
    const { createSurface: packet } = CreateSurfaceMessageSchema.parse(message)
    requireValid(packet.catalogId === catalogId && packet.sendDataModel !== true && packet.metadata === undefined)
    return {
      id: readId(packet.surfaceId),
      model: packet.dataModel ?? {},
      components: updateComponents(new Map(), packet.components ?? []),
    }
  }
  requireValid(previous)
  if (Object.hasOwn(message, 'updateComponents')) {
    const { updateComponents: packet } = UpdateComponentsMessageSchema.parse(message)
    requireValid(packet.surfaceId === previous.id)
    return { ...previous, components: updateComponents(previous.components, packet.components) }
  }
  const { updateDataModel: packet } = UpdateDataModelMessageSchema.parse(message)
  requireValid(packet.surfaceId === previous.id && Object.hasOwn(packet, 'value'))
  const tokens = readPointer(packet.path ?? '/')
  const model = updateValue(previous.model, tokens, packet.value)
  requireValid(isRecord(model) || (!tokens.length && model === null))
  validateJson(model)
  return { ...previous, model: model ?? {} }
}

function validateComponentGraph(components: Map<string, Definition>) {
  const heights = new Map<string, number>()
  const visiting = new Set<string>()
  function height(id: string): number {
    const previousHeight = heights.get(id)
    if (previousHeight !== undefined) return previousHeight
    const definition = components.get(id)
    if (!definition) return 0
    requireValid(!visiting.has(id) && visiting.size < maxComponentDepth)
    visiting.add(id)
    let result = 1
    if (definition.kind === 'Card' || definition.kind === 'Column') {
      for (const child of definition.children) result = Math.max(result, height(child) + 1)
    } else if (definition.kind === 'Button') {
      const child = components.get(definition.child)
      requireValid(!child || child.kind === 'Text')
      result = height(definition.child) + 1
    }
    visiting.delete(id)
    requireValid(result <= maxComponentDepth)
    heights.set(id, result)
    return result
  }
  for (const id of components.keys()) height(id)
}

function projectSurface(data: SurfaceData): AssistantA2uiSurface {
  const options = new Map<string, AssistantA2uiSelection>()
  const rendered = new Set<string>()
  let renderedText = 0
  let readable = false
  function resolve(value: DynamicText, limit: number, allowBlank = false): string {
    let resolved: unknown = value
    if (typeof value !== 'string') {
      resolved = value.reduce<unknown>(getOwnValue, data.model)
      if (resolved === undefined) throw new IncompleteSurface()
    }
    requireValid(typeof resolved === 'string' && resolved.length <= limit)
    const nonblank = Boolean(resolved.trim())
    requireValid(allowBlank || nonblank)
    readable ||= nonblank
    renderedText += resolved.length
    requireValid(renderedText <= maxFenceBytes)
    return resolved
  }

  function project(id: string, textLimit = maxText): AssistantA2uiNode {
    const definition = data.components.get(id)
    if (!definition) throw new IncompleteSurface()
    requireValid(!rendered.has(id))
    rendered.add(id)
    switch (definition.kind) {
      case 'Card':
      case 'Column':
        return { id, kind: definition.kind, children: definition.children.map(child => project(child)) }
      case 'Text':
        return {
          id, kind: definition.kind, text: resolve(definition.text, textLimit, textLimit === maxText),
          caption: definition.variant === 'caption',
        }
      case 'Button': {
        const child = project(definition.child, maxLabel)
        requireValid(child.kind === 'Text')
        const { action, variant } = definition
        const text = resolve(action.text, maxText)
        if (action.kind === 'details') return { id, kind: action.kind, buttonLabel: child.text, text }
        const selection = {
          id, kind: action.kind, buttonLabel: child.text, optionLabel: resolve(action.label, maxLabel), text, variant,
        }
        options.set(id, selection)
        return selection
      }
    }
  }

  const root = project('root')
  requireValid(root.kind === 'Card' || root.kind === 'Column')
  if (!readable) throw new IncompleteSurface()
  return { id: data.id, root, options }
}

export function parseAssistantA2ui(content: string): ParseResult {
  if (content.length > maxFenceBytes || new TextEncoder().encode(content).length > maxFenceBytes) {
    return { surface: null, invalidUpdate: true }
  }
  let raw: unknown
  try {
    raw = JSON.parse(content)
  } catch {
    return { surface: null, invalidUpdate: true }
  }
  if (!Array.isArray(raw) || !raw.length || raw.length > maxMessages || !isRecord(raw[0]) ||
    !Object.hasOwn(raw[0], 'createSurface')) return { surface: null, invalidUpdate: true }

  let data: SurfaceData | null = null
  let surface: AssistantA2uiSurface | null = null
  let invalidUpdate = true
  for (const message of raw) {
    try {
      const candidate = applyMessage(data, message)
      validateComponentGraph(candidate.components)
      try {
        surface = projectSurface(candidate)
        invalidUpdate = false
      } catch (error) {
        if (!(error instanceof IncompleteSurface)) throw error
        invalidUpdate = true
      }
      // A later message can supply missing children or bindings. An invalid packet leaves the preceding data intact.
      data = candidate
    } catch {
      invalidUpdate = true
    }
  }
  return { surface, invalidUpdate }
}

export function assistantA2uiText(content: string): string {
  const { surface } = parseAssistantA2ui(content)
  if (!surface) return ''
  const lines = new Set<string>()
  function collect(node: AssistantA2uiNode) {
    switch (node.kind) {
      case 'Card':
      case 'Column':
        node.children.forEach(collect)
        break
      case 'Text':
      case 'details':
        if (node.text.trim()) lines.add(node.text)
        break
      case 'select':
        lines.add(node.optionLabel)
        break
    }
  }
  collect(surface.root)
  return [...lines].join('\n')
}
