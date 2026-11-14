<script setup lang="ts">
import { useRoute } from 'vue-router'
import StatusBadge from './StatusBadge.vue'
import { dateLabel,stages,type WorkItem } from '@/services/workbench'
defineProps<{items:WorkItem[]}>();const route=useRoute()
</script>
<template><div class="table-scroll"><table class="spec-table"><caption class="sr-only">Spec 与任务查询结果</caption><thead><tr><th scope="col">工作项</th><th scope="col">阶段</th><th scope="col">状态</th><th scope="col">下一步／阻塞</th><th scope="col">更新</th></tr></thead><tbody><tr v-for="item in items" :key="item.entity+item.id"><td><RouterLink :to="{path:'/specs/'+item.specId,query:{taskId:item.entity==='task'?item.id:undefined,return:route.fullPath}}">{{item.title}}</RouterLink><span class="row-sub">{{item.projectName}} · {{item.entity==='task'?'任务 / '+item.specTitle:'Spec'}}{{!item.active?' · 已停用':''}}</span><span v-if="item.hasSourceProblem" class="warning-text">来源异常</span></td><td>{{stages[item.stage]}}</td><td><StatusBadge :status="item.status" /></td><td class="next-cell">{{item.status==='blocked'?item.blocker:item.nextAction||'—'}}</td><td class="muted nowrap">{{dateLabel(item.updatedAt)}}</td></tr></tbody></table><p v-if="!items.length" class="empty">当前筛选没有匹配记录；可清除条件或查看全部记录。</p></div></template>
