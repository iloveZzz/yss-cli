import { shallowRef } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { errorMessage, type WorkItem, type Stage, type Status } from '@/services/workbench'
export function useWorkActions() {
 const {submit,saving}=useWorkbench();const open=shallowRef(false);const blocker=shallowRef('');const error=shallowRef('');const notice=shallowRef('')
 let pending:{item:WorkItem;revision:number}|undefined
 async function move(item:WorkItem,dimension:'status'|'stage',value:Status|Stage,expectedRevision:number){
  error.value='';notice.value='';if(item[dimension]===value||saving.value)return
  if(dimension==='status' && value==='blocked'){pending={item,revision:expectedRevision};blocker.value=item.blocker;open.value=true;return}
  try{await submit(dimension==='status'?{kind:'setWorkStatus',entity:item.entity,id:item.id,status:value as Status,blocker:item.blocker}:{kind:'setWorkStage',entity:item.entity,id:item.id,stage:value as Stage},expectedRevision);notice.value='已移动；若卡片不再显示，表示它已不符合当前筛选。'}catch(e){error.value=errorMessage(e)}
 }
 async function confirm(){if(!pending||saving.value)return;if(!blocker.value.trim()){error.value='请填写阻塞说明';return}try{await submit({kind:'setWorkStatus',entity:pending.item.entity,id:pending.item.id,status:'blocked',blocker:blocker.value},pending.revision);open.value=false;pending=undefined;notice.value='已记录阻塞；结果已刷新。'}catch(e){error.value=errorMessage(e)}}
 function cancel(){open.value=false;pending=undefined;error.value=''}
 return {move,confirm,cancel,open,blocker,error,notice,saving}
}
