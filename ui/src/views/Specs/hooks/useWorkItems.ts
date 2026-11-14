import { shallowRef, watch, onBeforeUnmount, type Ref } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { queryWorkItems, errorMessage, type WorkQuery, type QueryResult } from '@/services/workbench'
export function useWorkItems(query:Readonly<Ref<WorkQuery>>) {
 const {snapshot,refresh:refreshSnapshot,expired}=useWorkbench()
 const result=shallowRef<QueryResult>();const loading=shallowRef(false);const error=shallowRef('')
 let controller:AbortController|undefined;let generation=0
 async function refresh(){
  controller?.abort();const own=new AbortController();controller=own;const gen=++generation
  if(!snapshot.value||expired.value){loading.value=false;return}
  loading.value=true
  try{const value=await queryWorkItems(query.value,own.signal);if(gen!==generation||own.signal.aborted)return
   if(value.revision!==snapshot.value?.revision){await refreshSnapshot();if(gen===generation && !own.signal.aborted && snapshot.value?.revision!==value.revision)error.value='查询版本已变化，请刷新重试。';return}
   result.value=value;error.value=''
  }catch(e){if(gen===generation&&!own.signal.aborted)error.value=errorMessage(e)}
  finally{if(gen===generation)loading.value=false}
 }
 watch([()=>JSON.stringify(query.value),snapshot],()=>void refresh(),{immediate:true})
 onBeforeUnmount(()=>{generation++;controller?.abort()})
 return {result,loading,error,refresh}
}
