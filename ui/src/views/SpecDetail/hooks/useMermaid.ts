import { watch, type Ref } from 'vue'
import { renderMermaid } from '@/lib/renderMermaid'

export function useMermaid(article: Readonly<Ref<HTMLElement | null>>, html: Readonly<Ref<string>>) {
  watch([article, html], async ([root], _previous, cleanup) => {
    const blocks = Array.from(root?.querySelectorAll<HTMLElement>('pre > code.language-mermaid') ?? [])
    if (!blocks.length) return
    const controller = new AbortController()
    cleanup(() => controller.abort())
    for (const code of blocks) {
      if (controller.signal.aborted) return
      const pre = code.parentElement!
      try {
        const svg = await renderMermaid(code.textContent ?? '', controller.signal)
        if (controller.signal.aborted || !root?.contains(code)) return
        const diagram = document.createElement('figure')
        diagram.className = 'mermaid-diagram'
        diagram.tabIndex = 0
        diagram.setAttribute('aria-label', 'Mermaid 图表，可横向滚动；源码见 Markdown 原文')
        diagram.innerHTML = svg
        pre.replaceWith(diagram)
      } catch {
        if (controller.signal.aborted || !root?.contains(code)) return
        const notice = document.createElement('p')
        notice.className = 'notice warning'
        notice.setAttribute('role', 'alert')
        notice.textContent = 'Mermaid 图表渲染失败，请检查语法，或切换原文后重新预览。下方保留源码。'
        pre.before(notice)
      }
    }
  }, { flush: 'post' })
}
