interface AssistantChartPoint {
  label: string
  value: number
}

export interface AssistantChartData {
  type: 'bar' | 'line'
  title: string
  unit?: string
  data: AssistantChartPoint[]
}

const maxChartBytes = 16 * 1024
const maxChartPoints = 60
const encoder = new TextEncoder()

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function plainText(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const text = value.trim()
  return text && !/[\p{Cc}\u2028\u2029]/u.test(text) ? text : undefined
}

export function parseAssistantChart(code: string): AssistantChartData | undefined {
  if (code.length > maxChartBytes || encoder.encode(code).length > maxChartBytes) return undefined

  let input: unknown
  try {
    input = JSON.parse(code)
  } catch {
    return undefined
  }
  if (!isRecord(input) || (input.type !== 'bar' && input.type !== 'line')) return undefined

  const title = plainText(input.title)
  const unit = plainText(input.unit)
  const minimumPoints = input.type === 'line' ? 2 : 1
  if (!title || (Object.hasOwn(input, 'unit') && unit === undefined)) return undefined
  if (!Array.isArray(input.data) || input.data.length < minimumPoints || input.data.length > maxChartPoints) {
    return undefined
  }

  const data: AssistantChartPoint[] = []
  for (const point of input.data) {
    if (!isRecord(point)) return undefined
    const label = plainText(point.label)
    const value = point.value
    if (!label || typeof value !== 'number' || !Number.isFinite(value)) return undefined
    data.push({ label, value })
  }
  return { type: input.type, title, unit, data }
}
