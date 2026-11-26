import { inject, onScopeDispose, provide, shallowReadonly, shallowRef, type InjectionKey, type Ref, type ShallowRef } from 'vue'
import { errorMessage, expired } from '@/services/workbench'
import { loadWorkbenchCatalog, type WorkbenchCatalog } from '@/services/workbench-read'

const catalog = shallowRef<WorkbenchCatalog>()
const catalogLoading = shallowRef(false)
const catalogError = shallowRef('')
let catalogRequest: Promise<boolean> | undefined

function refreshCatalog(minimumRevision?: number): Promise<boolean> {
  if (expired.value) return Promise.resolve(false)
  if (catalogRequest) {
    return minimumRevision === undefined ? catalogRequest : catalogRequest.then((ok) =>
      ok && (catalog.value?.revision ?? -1) < minimumRevision ? refreshCatalog() : ok
    )
  }
  catalogLoading.value = true
  catalogRequest = loadWorkbenchCatalog()
    .then((next) => {
      catalog.value = next
      catalogError.value = ''
      return true
    })
    .catch((cause) => {
      catalogError.value = errorMessage(cause)
      return false
    })
    .finally(() => {
      catalogRequest = undefined
      catalogLoading.value = false
    })
  return catalogRequest
}

export function useWorkbenchCatalog() {
  return {
    catalog: shallowReadonly(catalog),
    catalogLoading: shallowReadonly(catalogLoading),
    catalogError: shallowReadonly(catalogError),
    refreshCatalog
  }
}

interface RouteReads {
  busy: Readonly<Ref<boolean>>
  readAt: Readonly<Ref<string | undefined>>
  refresh: () => Promise<unknown>
}

const routeReadsKey: InjectionKey<ShallowRef<RouteReads | undefined>> = Symbol('workbench route reads')

export function provideWorkbenchRouteReads() {
  const routeReads = shallowRef<RouteReads>()
  provide(routeReadsKey, routeReads)
  return routeReads
}

export function registerWorkbenchRouteReads(reads: RouteReads) {
  const routeReads = inject(routeReadsKey)
  if (!routeReads) return
  routeReads.value = reads
  onScopeDispose(() => {
    if (routeReads.value === reads) routeReads.value = undefined
  })
}
