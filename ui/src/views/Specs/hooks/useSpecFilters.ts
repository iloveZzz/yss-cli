import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { defaultWorkQuery, type WorkQuery, type Layout, type GroupBy, type SavedView } from '@/services/workbench'
import { decodeWorkQuery, encodeWorkQuery } from '../schemas/filter'
export function useSpecFilters() {
 const route=useRoute();const router=useRouter();const decoded=computed(()=>decodeWorkQuery(route.query))
 const filters=computed(()=>decoded.value.query);const layout=computed(()=>decoded.value.layout);const groupBy=computed(()=>decoded.value.groupBy);const viewId=computed(()=>decoded.value.viewId)
 function update(patch:Partial<WorkQuery>, display?:{layout?:Layout;groupBy?:GroupBy}) {
  const next={...filters.value,...patch}
  if(patch.includeClosed===false)next.statuses=next.statuses.filter(s=>s!=='done'&&s!=='canceled')
  if(next.statuses.some(s=>s==='done'||s==='canceled'))next.includeClosed=true
  void router.replace({query:encodeWorkQuery(next,display?.layout??layout.value,display?.groupBy??groupBy.value,viewId.value)})
 }
 function apply(view:SavedView){void router.replace({query:encodeWorkQuery(view.query,view.layout,view.groupBy,view.id)})}
 function clear(){void router.replace({query:encodeWorkQuery(defaultWorkQuery(),layout.value,groupBy.value)})}
 return {filters,layout,groupBy,viewId,update,apply,clear}
}
