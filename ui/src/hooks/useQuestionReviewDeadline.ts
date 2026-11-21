import { watch } from 'vue'
import { nextReviewDeadline } from '@/services/workbench-content'
import type { QuestionView } from '@/services/workbench'

export function useQuestionReviewDeadline(
  source: () => { readAt: string; questions: QuestionView[] } | undefined,
  refresh: () => Promise<unknown>
) {
  watch(source, (value, _, onCleanup) => {
    const deadline = value && nextReviewDeadline(value.questions, value.readAt)
    if (deadline === undefined) return
    let active = true
    let timer: ReturnType<typeof setTimeout>
    const run = async () => {
      const remaining = deadline - Date.now()
      if (remaining > 0) {
        timer = setTimeout(run, Math.min(remaining, 2_147_483_647))
        return
      }
      await refresh()
      // A successful read replaces the source and schedules its next deadline.
      if (active) timer = setTimeout(run, 5_000)
    }
    onCleanup(() => {
      active = false
      clearTimeout(timer)
    })
    void run()
  }, { immediate: true })
}
