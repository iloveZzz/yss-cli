let mermaidModule: Promise<typeof import('mermaid').default> | undefined
let renderQueue: Promise<unknown> = Promise.resolve()

// Images can load while Mermaid measures the diagram, before SVG cleanup.
const embeddedResource = /<\s*(?:img|image)\b|!\[[^\]\n]*\]\s*[([]|(?:^|[{,]\s*)['"]?img['"]?\s*:|\b(?:url|image-set)\s*\(|@import\b/im
const maxDiagramLength = 50_000

function loadMermaid() {
  mermaidModule ??= import('mermaid').then(({ default: mermaid }) => {
    mermaid.initialize({
      startOnLoad: false, securityLevel: 'strict', suppressErrorRendering: true, maxTextSize: maxDiagramLength,
    })
    return mermaid
  })
  return mermaidModule
}

export function renderMermaid(
  code: string,
  signal: AbortSignal,
  { allowEmbeddedResources = true } = {}
): Promise<string> {
  const result = renderQueue.then(async () => {
    signal.throwIfAborted()
    // Mermaid returns a replacement error diagram for oversized input instead of rejecting.
    if (code.length > maxDiagramLength) throw new Error('Mermaid diagram exceeds the text limit')
    if (!allowEmbeddedResources && embeddedResource.test(code)) {
      throw new Error('Mermaid embedded resources are disabled')
    }
    const mermaid = await loadMermaid()
    signal.throwIfAborted()
    const { svg } = await mermaid.render(`yss-mermaid-${crypto.randomUUID()}`, code)
    signal.throwIfAborted()

    // Strict mode sanitizes SVG. Clean up navigation and images before mounting it.
    const parsed = document.createElement('template')
    parsed.innerHTML = svg
    const diagram = parsed.content.querySelector('svg')
    if (!diagram) throw new Error('Missing Mermaid SVG')
    diagram.querySelectorAll('a').forEach(link => {
      link.removeAttribute('href')
      link.removeAttribute('xlink:href')
    })
    diagram.querySelectorAll('image, img').forEach(image => image.remove())
    if (!allowEmbeddedResources) {
      diagram.querySelectorAll('[href], [xlink\\:href]').forEach(element => {
        for (const name of ['href', 'xlink:href']) {
          const value = element.getAttribute(name)
          if (value && !value.startsWith('#')) element.removeAttribute(name)
        }
      })
    }
    return diagram.outerHTML
  })
  // Mermaid serializes rendering. Check cancellation before enqueueing new work in it.
  renderQueue = result.catch(() => undefined)
  return result
}
