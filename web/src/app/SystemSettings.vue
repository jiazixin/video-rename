<script setup lang="ts">
import {Bell,Film,Hash,MessageSquareText,Settings2} from 'lucide-vue-next'
import {NumberingSettings,ProcessingSettings} from '../features/settings'
import Models from '../features/models/Models.vue'
import Sampling from '../features/sampling/Sampling.vue'
import Notifications from '../features/notifications/Notifications.vue'
import './system-settings.css'
defineProps<{tab:string}>()
defineEmits<{changed:[]}>()
const tabs=[{id:'processing',label:'处理与缓存',icon:Settings2},{id:'numbering',label:'编号规则',icon:Hash},{id:'models',label:'模型配置',icon:MessageSquareText},{id:'sampling',label:'抽帧设置',icon:Film},{id:'notifications',label:'通知设置',icon:Bell}]
function select(id:string){location.hash=`settings/${id}`}
</script>
<template><section class="system-settings"><div class="page-heading"><div><h1>系统设置<span class="title-dot">.</span></h1><p>管理处理资源、编号规则、模型、抽帧规则与通知。</p></div></div><div class="log-tabs settings-tabs" aria-label="系统设置分类"><button v-for="item in tabs" :key="item.id" :class="{active:tab===item.id}" :aria-pressed="tab===item.id" @click="select(item.id)"><component :is="item.icon" :size="18"/>{{item.label}}</button></div><ProcessingSettings v-if="tab==='processing'"/><NumberingSettings v-else-if="tab==='numbering'"/><Models v-else-if="tab==='models'" @changed="$emit('changed')"/><Sampling v-else-if="tab==='sampling'"/><Notifications v-else-if="tab==='notifications'"/></section></template>
