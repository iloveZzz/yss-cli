<script setup lang="ts">
import { useRoute } from 'vue-router'
import StatusBadge from './StatusBadge.vue'
import { dateLabel,stages,type WorkItem } from '@/services/workbench'
defineProps<{items:WorkItem[]}>();const route=useRoute()
</script>
<template><div class="table-scroll"><table class="spec-table"><caption class="sr-only">Spec 查询结果</caption><thead><tr><th scope="col">Spec</th><th scope="col">阶段</th><th scope="col">状态</th><th scope="col">下一步／阻塞</th><th scope="col">更新</th></tr></thead><tbody><tr v-for="item in items" :key="item.id"><td><RouterLink :to="{path:'/specs/'+item.specId,query:{return:route.fullPath}}">{{item.title}}</RouterLink><span class="row-sub">{{item.projectName}}{{!item.active?' · 已停用':''}}</span><span v-if="item.hasSourceProblem" class="warning-text">来源异常</span></td><td>{{stages[item.stage]}}</td><td><StatusBadge :status="item.status" /></td><td class="next-cell">{{item.status==='blocked'?item.blocker:item.nextAction||'—'}}</td><td class="muted nowrap">{{dateLabel(item.updatedAt)}}</td></tr></tbody></table><p v-if="!items.length" class="empty">当前筛选没有匹配记录；可清除条件或查看全部记录。</p></div></template>
