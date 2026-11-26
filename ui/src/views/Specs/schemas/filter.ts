import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import {
  defaultWorkQuery,
  stages,
  statuses,
  contentStatuses,
  type WorkQuery,
  type Layout,
  type GroupBy,
  type Stage,
  type Status,
  type ContentStatus
} from '@/services/workbench-model'
export function encodeWorkQuery(
  query: WorkQuery,
  layout: Layout,
  groupBy: GroupBy,
  viewId?: string
): LocationQueryRaw {
  return {
    entity: 'spec',
    progressBasis: query.progressBasis,
    attention: query.attention.length ? query.attention : undefined,
    q: query.q || undefined,
    project: query.projectIds.length ? query.projectIds : undefined,
    spec: query.specIds.length ? query.specIds : undefined,
    stage: query.stages.length ? query.stages : undefined,
    status: query.statuses.length ? query.statuses : undefined,
    contentStatus: query.contentStatuses?.length ? query.contentStatuses : undefined,
    reminder: query.reminderKinds?.length ? query.reminderKinds : undefined,
    reconfirm: query.needsReconfirmation ? 'true' : undefined,
    activity: query.activity,
    showClosed: String(query.includeClosed),
    sourceProblems: String(query.sourceProblemsOnly),
    sort: query.sort,
    layout,
    groupBy,
    viewId
  }
}
export function decodeWorkQuery(raw: LocationQuery): {
  query: WorkQuery
  layout: Layout
  groupBy: GroupBy
  viewId?: string
} {
  const values = (key: string): string[] => {
    const value = raw[key]
    return (Array.isArray(value) ? value : [value]).filter(
      (entry): entry is string => typeof entry === 'string' && !!entry
    )
  }
  const text = (key: string) => values(key)[0] ?? ''
  const query: WorkQuery = {
    ...defaultWorkQuery(),
    progressBasis:
      text('progressBasis') === 'effective' || !Object.keys(raw).length
        ? 'effective'
        : 'personal',
    attention: values('attention').filter((value) =>
      ['any', 'awaitingConfirmation', 'unverified', 'needsSync', 'needsReview', 'conflict'].includes(value)
    ),
    q: text('q'),
    projectIds: values('project'),
    specIds: values('spec'),
    stages: values('stage').filter((stage) => Object.hasOwn(stages, stage)) as Stage[],
    statuses: values('status').filter((status) => Object.hasOwn(statuses, status)) as Status[],
    contentStatuses: values('contentStatus').filter((status) =>
      Object.hasOwn(contentStatuses, status)
    ) as ContentStatus[],
    reminderKinds: values('reminder'),
    needsReconfirmation: text('reconfirm') === 'true',
    activity:
      text('activity') === 'all' ? 'all' : text('activity') === 'inactive' ? 'inactive' : 'active',
    includeClosed: text('showClosed') === 'true',
    sourceProblemsOnly: text('sourceProblems') === 'true',
    sort: text('sort') === 'titleAsc' ? 'titleAsc' : 'updatedDesc'
  }
  if (query.statuses.some((status) => status === 'done' || status === 'canceled'))
    query.includeClosed = true
  return {
    query,
    layout: text('layout') === 'board' ? 'board' : 'list',
    groupBy: text('groupBy') === 'stage' ? 'stage' : 'status',
    viewId: text('viewId') || undefined
  }
}
