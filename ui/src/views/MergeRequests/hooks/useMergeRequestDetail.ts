import { onScopeDispose, shallowRef } from 'vue'
import {
  getMergeRequestDetailSection,
  type MergeRequestBaseDetail,
  type MergeRequestDetailSection,
  type MergeRequestIssue,
  type MergeRequestRepository,
  type MergeRequestSignal,
  type MergeRequestSummary
} from '@/services/workbench-merge-requests'
import { losesAccess, transportIssue, type MergeRequestRow } from '../schemas/requests'

type SignalSection = Exclude<MergeRequestDetailSection, 'base'>

export type MergeRequestSectionRead =
  | { state: 'loading' }
  | { state: 'failed'; issue: MergeRequestIssue }
  | { state: 'ready'; signal: MergeRequestSignal; historicalCi?: MergeRequestSignal[] }

export interface MergeRequestDetailRead {
  repository: MergeRequestRepository
  number: number
  selected?: MergeRequestSummary
  result?: MergeRequestBaseDetail
  issue?: MergeRequestIssue
  signals: Partial<Record<SignalSection, MergeRequestSectionRead>>
}

interface DetailOptions {
  identityFor: (repositoryId: string) => string | undefined
  invalidateRepository: (repositoryId: string, issue: MergeRequestIssue, responseIdentity?: string) => void
}

const changedIdentity = { code: 'identityChanged', message: '仓库的连接身份已变化，请从列表重新打开。' }
const unknownIdentity = { code: 'unavailable', message: '无法核对仓库的连接身份，请刷新仓库后重试。' }
const changedVersion = { code: 'versionChanged', message: '平台上的请求已更新，请重新读取详情。' }

export function useMergeRequestDetail(options: DetailOptions) {
  const detail = shallowRef<MergeRequestDetailRead>()
  const requests = new Map<MergeRequestDetailSection, AbortController>()
  let identity: string | undefined
  let refreshedVersion = false
  let wantHistory = false

  function cancelReads() {
    for (const request of requests.values()) request.abort()
    requests.clear()
  }

  function clearDetail() {
    cancelReads()
    detail.value = undefined
    identity = undefined
  }

  function invalidateDetail(repositoryId: string, issue: MergeRequestIssue) {
    const target = detail.value
    if (target?.repository.id !== repositoryId) return
    cancelReads()
    identity = undefined
    detail.value = { repository: target.repository, number: target.number, issue, signals: {} }
  }

  function invalidateAccess(repositoryId: string, issue: MergeRequestIssue, responseIdentity?: string) {
    invalidateDetail(repositoryId, issue)
    options.invalidateRepository(repositoryId, issue, responseIdentity)
  }

  function patchSignal(section: SignalSection, read: MergeRequestSectionRead) {
    const target = detail.value
    if (target) detail.value = { ...target, signals: { ...target.signals, [section]: read } }
  }

  function applyIssue(section: MergeRequestDetailSection, issue: MergeRequestIssue) {
    const target = detail.value
    if (!target) return
    if (section === 'base') detail.value = { ...target, issue }
    else patchSignal(section, { state: 'failed', issue })
  }

  function refreshVersion() {
    const target = detail.value
    if (!target) return
    const repeat = refreshedVersion
    cancelReads()
    refreshedVersion = true
    detail.value = {
      repository: target.repository, number: target.number, selected: target.selected,
      signals: {}, issue: repeat ? changedVersion : undefined
    }
    if (!repeat) void readSection('base')
  }

  async function readSection(section: MergeRequestDetailSection) {
    const target = detail.value
    if (!target || !identity || requests.has(section)) return
    const { repository, number } = target
    const activeIdentity = options.identityFor(repository.id)
    if (!activeIdentity || activeIdentity !== identity) {
      invalidateAccess(repository.id, activeIdentity ? changedIdentity : unknownIdentity, activeIdentity)
      return
    }

    const expectedVersion = target.result?.version
    if (section !== 'base' && (!expectedVersion || (section === 'history' && !target.result?.historyAvailable))) return
    const controller = new AbortController()
    requests.set(section, controller)
    if (section === 'base') detail.value = { ...target, issue: undefined }
    else patchSignal(section, { state: 'loading' })

    const current = () => requests.get(section) === controller && !controller.signal.aborted
    function hasCurrentIdentity() {
      const latest = options.identityFor(repository.id)
      if (latest === activeIdentity) return true
      invalidateAccess(repository.id, latest ? changedIdentity : unknownIdentity, latest)
      return false
    }

    try {
      const result = await getMergeRequestDetailSection(repository.id, number, section, controller.signal)
      if (!current() || !hasCurrentIdentity()) return
      if (!result.identity || ('issue' in result && losesAccess(result.issue))) {
        invalidateAccess(repository.id, 'issue' in result ? result.issue : unknownIdentity, result.identity)
        return
      }
      if (result.identity !== activeIdentity) {
        invalidateAccess(repository.id, changedIdentity, result.identity)
        return
      }
      if ('issue' in result) {
        if (section !== 'base' && result.issue.code === 'versionChanged') refreshVersion()
        else applyIssue(section, result.issue)
        return
      }
      if (result.number !== number || !result.version) {
        applyIssue(section, { code: 'unavailable', message: '平台响应与所选请求不一致，可以重试。' })
        return
      }
      if (section === 'base' && 'descriptionAvailable' in result) {
        detail.value = { ...target, result, issue: undefined }
        void readSection('review')
        void readSection('ci')
        if (wantHistory && result.historyAvailable) void readSection('history')
      } else if (section !== 'base' && 'signal' in result) {
        if (result.version !== expectedVersion) refreshVersion()
        else patchSignal(section, { state: 'ready', signal: result.signal, historicalCi: result.historicalCi })
      } else {
        applyIssue(section, { code: 'unavailable', message: '平台响应缺少所请求的详情，可以重试。' })
      }
    } catch (cause) {
      if (!current() || !hasCurrentIdentity()) return
      const issue = transportIssue(cause)
      if (losesAccess(issue)) invalidateAccess(repository.id, issue)
      else applyIssue(section, issue)
    } finally {
      if (current()) requests.delete(section)
    }
  }

  function openDetail({ repository, request }: MergeRequestRow) {
    const currentIdentity = options.identityFor(repository.id)
    if (!currentIdentity) return
    clearDetail()
    identity = currentIdentity
    refreshedVersion = false
    wantHistory = false
    detail.value = { repository, number: request.number, selected: request, signals: {} }
    void readSection('base')
  }

  function retryDetailSection(section: MergeRequestDetailSection) {
    const target = detail.value
    if (!target) return
    if (section === 'base') {
      if (!target.issue || target.result) return
      refreshedVersion = false
    } else {
      const read = target.signals[section]
      if ((!read && section !== 'history') || read?.state === 'loading') return
      if (read?.state === 'ready' && read.signal.status !== 'readFailed') return
      if (section === 'history') wantHistory = true
    }
    void readSection(section)
  }

  onScopeDispose(clearDetail)

  return { detail, openDetail, clearDetail, invalidateDetail, retryDetailSection }
}
