<script setup lang="ts">
import {BarChart3,Coins} from 'lucide-vue-next'
import UsageTokens from './UsageTokens.vue'
import UsageAnalysis from './UsageAnalysis.vue'
defineProps<{tab:string}>()
const tabs=[{id:'tokens',label:'模型用量',icon:Coins},{id:'analysis',label:'数据分析',icon:BarChart3}]
function select(id:string){location.hash=`usage/${id}`}
</script>
<template>
<section>
 <div class="page-heading">
  <div>
   <span class="eyebrow">模型请求</span>
   <h1>用量分析<span class="title-dot">.</span></h1>
   <p>按时间、模型和批次统计 token 消耗，或查看批次处理性能。</p>
  </div>
 </div>
 <div class="log-tabs usage-tabs" aria-label="用量分析分类">
  <button v-for="item in tabs" :key="item.id" :class="{active:tab===item.id}" :aria-pressed="tab===item.id" @click="select(item.id)"><component :is="item.icon" :size="18"/>{{item.label}}</button>
 </div>
 <UsageTokens v-if="tab!=='analysis'"/>
 <UsageAnalysis v-else/>
</section>
</template>
