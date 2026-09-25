<script setup lang="ts">
import { Settings2,FolderOpen,ScrollText,Clock3,LogOut,PanelLeftClose,BarChart3,Images,ListVideo,Coins } from 'lucide-vue-next'
import { api,session } from '../app/api'
import { computed,ref } from 'vue'
const props=defineProps<{page:string;model:string;ready:boolean}>()
const emit=defineEmits<{navigate:[page:string]}>();const collapsed=ref(false),error=ref('')
const groups=[
 {label:'',items:[{id:'library',name:'文件目录',icon:FolderOpen}]},
 {label:'任务记录',items:[{id:'extractions',name:'抽帧记录',icon:Images},{id:'history',name:'运行记录',icon:ScrollText}]},
 {label:'运行管理',items:[{id:'plans',name:'计划管理',icon:Clock3},{id:'runs',name:'批次记录',icon:ListVideo}]},
 {label:'统计分析',items:[{id:'usage/tokens',name:'模型用量',icon:Coins},{id:'usage/analysis',name:'运行分析',icon:BarChart3}]},
 {label:'',items:[{id:'settings',name:'系统设置',icon:Settings2}]},
]
const title=computed(()=>groups.flatMap(group=>group.items).find(item=>item.id===props.page)?.name||'文件目录')
async function logout(){try{await api('/auth/logout','POST',{});session.value=null}catch(e){error.value=(e as Error).message}}
</script>
<template>
<div class="workspace" :class="{collapsed}">
 <aside class="sidebar">
  <a href="#library" class="brand"><span class="brand-mark"><img src="/logo.png" alt="" width="32" height="32"/></span><span>video-rename</span></a>
  <nav aria-label="主导航">
   <div v-for="(group,index) in groups" :key="index" class="nav-group">
    <div v-if="group.label" class="sidebar-caption">{{group.label}}</div>
    <button v-for="item in group.items" :key="item.id" :class="{active:page===item.id}" :aria-label="item.name" :aria-current="page===item.id?'page':undefined" :title="item.name" @click="emit('navigate',item.id)"><component :is="item.icon" :size="18"/><span>{{item.name}}</span><span v-if="page===item.id" class="nav-dot"/></button>
   </div>
  </nav>
  <div class="sidebar-footer"><div class="model-box"><span class="eyebrow">视觉模型</span><strong :title="model">{{model||'未连接'}}</strong><span class="model-state"><i class="signal" :class="{off:!ready}"/>{{ready?'自动命名已就绪':'自动命名暂不可用'}}</span></div><div class="account"><div class="avatar">{{session?.username.slice(0,1).toUpperCase()}}</div><div><strong>{{session?.username}}</strong><small>管理员</small></div><button class="icon-btn" aria-label="退出登录" @click="logout"><LogOut :size="17"/></button></div><p v-if="error" class="error">{{error}}</p></div>
 </aside>
 <div class="workspace-main"><header class="topbar"><div><button class="icon-btn sidebar-toggle" aria-label="收起或展开导航" :aria-expanded="!collapsed" @click="collapsed=!collapsed"><PanelLeftClose :size="18"/></button><strong>{{title}}</strong></div></header><main class="page-main"><slot/></main></div>
</div>
</template>
<style scoped>
.sidebar>.brand{margin-bottom:26px;flex-shrink:0}
.sidebar nav{overflow-y:auto;min-height:0;gap:20px;padding-bottom:16px}
.nav-group{display:flex;flex-direction:column;gap:4px}
.sidebar-caption{margin:0 0 4px;font-size:11px;letter-spacing:0;color:var(--steel)}
.sidebar nav button{padding:10px 12px;flex-shrink:0}
.sidebar-footer{flex-shrink:0}
.sidebar-toggle{display:flex}
.collapsed .sidebar{display:none}.collapsed .workspace-main{margin-left:0}
@media(max-width:800px){.sidebar nav{margin-top:0;gap:12px}.nav-group+.nav-group{border-top:1px solid var(--line);padding-top:12px}.sidebar nav button{justify-content:center;padding:10px}.sidebar-footer .account{padding:12px 0}}
</style>
