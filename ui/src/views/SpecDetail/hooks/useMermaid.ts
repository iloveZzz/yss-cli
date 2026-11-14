import { watch, type Ref } from 'vue'

export function useMermaid(article: Readonly<Ref<HTMLElement | null>>, html: Readonly<Ref<string>>) {
  watch([article, html], async ([root], _previous, cleanup) => {
    const blocks = Array.from(root?.querySelectorAll<HTMLElement>('pre > code.language-mermaid') ?? [])
    if (!blocks.length) return
    let canceled = false
    cleanup(() => { canceled = true })
    const ready = import('mermaid').then(({ default: mermaid }) => {
      mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', suppressErrorRendering: true })
      return mermaid
    })
    for (const code of blocks) {
      if (canceled) return
      const pre = code.parentElement!
      try {
        const mermaid = await ready
        if (canceled) return
        const { svg } = await mermaid.render(`yss-mermaid-${crypto.randomUUID()}`, code.textContent ?? '')
        if (canceled || !root?.contains(code)) return
        const diagram = document.createElement('figure')
        diagram.className = 'mermaid-diagram'
        diagram.tabIndex = 0
        diagram.setAttribute('aria-label', 'Mermaid 图表，可横向滚动；源码见 Markdown 原文')
        // Strict mode sanitizes the generated SVG with Mermaid's bundled DOMPurify.
        diagram.innerHTML = svg
        // Keep diagrams read-only and preserve the preview's image/navigation restrictions.
        diagram.querySelectorAll('a').forEach(link => {
          link.removeAttribute('href')
          link.removeAttribute('xlink:href')
        })
        diagram.querySelectorAll('image, img').forEach(image => image.remove())
        pre.replaceWith(diagram)
      } catch {
        if (canceled || !root?.contains(code)) return
        const notice = document.createElement('p')
        notice.className = 'notice warning'
        notice.setAttribute('role', 'alert')
        notice.textContent = 'Mermaid 图表渲染失败，请检查语法，或切换原文后重新预览。下方保留源码。'
        pre.before(notice)
      }
    }
  }, { flush: 'post' })
}
