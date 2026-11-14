import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import { defaultWorkQuery, stages, statuses, type WorkQuery, type Layout, type GroupBy, type Stage, type Status } from '../../../services/workbench-model'
export function encodeWorkQuery(query: WorkQuery, layout: Layout, groupBy: GroupBy, viewId?: string): LocationQueryRaw {
 return {entity:query.entity,q:query.q||undefined,project:query.projectIds.length?query.projectIds:undefined,spec:query.specIds.length?query.specIds:undefined,stage:query.stages.length?query.stages:undefined,status:query.statuses.length?query.statuses:undefined,activity:query.activity,showClosed:String(query.includeClosed),sourceProblems:String(query.sourceProblemsOnly),sort:query.sort,layout,groupBy,viewId}
}
export function decodeWorkQuery(raw: LocationQuery): {query:WorkQuery;layout:Layout;groupBy:GroupBy;viewId?:string} {
 const values=(key:string):string[]=>{const value=raw[key];return (Array.isArray(value)?value:[value]).filter((v):v is string=>typeof v==='string'&&!!v)}
 const text=(key:string)=>values(key)[0]??''
 const query:WorkQuery={...defaultWorkQuery(),entity:text('entity')==='task'?'task':'spec',q:text('q'),projectIds:values('project'),specIds:values('spec'),stages:values('stage').filter(s=>Object.hasOwn(stages,s)) as Stage[],statuses:values('status').filter(s=>Object.hasOwn(statuses,s)) as Status[],activity:text('activity')==='all'?'all':text('activity')==='inactive'?'inactive':'active',includeClosed:text('showClosed')==='true',sourceProblemsOnly:text('sourceProblems')==='true',sort:text('sort')==='titleAsc'?'titleAsc':'updatedDesc'}
 if(query.statuses.some(s=>s==='done'||s==='canceled'))query.includeClosed=true
 return {query,layout:text('layout')==='board'?'board':'list',groupBy:text('groupBy')==='stage'?'stage':'status',viewId:text('viewId')||undefined}
}
