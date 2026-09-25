<script setup lang="ts">
import { Settings2,FolderOpen,ScrollText,Clock3,LogOut,PanelLeftClose,BarChart3,Images,ListVideo,Coins } from 'lucide-vue-next'
import { api,session } from '../app/api'
import { computed,ref } from 'vue'
const props=defineProps<{page:string;model:string;ready:boolean}>()
const emit=defineEmits<{navigate:[page:string]}>();const collapsed=ref(false),error=ref('')
const groups=[
 {label:'工作台',items:[{id:'library',name:'文件目录',icon:FolderOpen}]},
 {label:'任务',items:[{id:'extractions',name:'抽帧记录',icon:Images},{id:'history',name:'运行记录',icon:ScrollText},{id:'plans',name:'计划管理',icon:Clock3},{id:'runs',name:'批次记录',icon:ListVideo}]},
 {label:'分析',items:[{id:'usage/tokens',name:'模型用量',icon:Coins},{id:'usage/analysis',name:'运行分析',icon:BarChart3}]},
 {label:'系统',items:[{id:'settings',name:'系统设置',icon:Settings2}]},
]
const title=computed(()=>groups.flatMap(group=>group.items).find(item=>item.id===props.page)?.name||'文件目录')
const modelLabel=computed(()=>props.model.split(/[\\/]/).filter(Boolean).at(-1)||'未连接')
async function logout(){try{await api('/auth/logout','POST',{});session.value=null}catch(e){error.value=(e as Error).message}}
</script>
<template>
<div class="workspace" :class="{collapsed}">
 <aside class="sidebar">
   <a href="#library" class="brand"><span class="brand-mark"><img src="/logo.png" alt="" width="40" height="40"/></span><span>video-rename</span></a>
  <nav aria-label="主导航">
   <div v-for="(group,index) in groups" :key="index" class="nav-group">
    <div v-if="group.label" class="sidebar-caption">{{group.label}}</div>
    <button v-for="item in group.items" :key="item.id" :class="{active:page===item.id}" :aria-label="item.name" :aria-current="page===item.id?'page':undefined" :title="item.name" @click="emit('navigate',item.id)"><component :is="item.icon" :size="20" :stroke-width="1.8"/><span>{{item.name}}</span></button>
   </div>
  </nav>
   <div class="sidebar-footer">
    <div class="model-box"><span class="eyebrow">当前模型</span><div class="model-name"><i class="signal" :class="{off:!ready}"/><strong :title="model">{{modelLabel}}</strong></div><span class="model-state">{{ready?'自动命名 · 已就绪':'自动命名 · 暂不可用'}}</span></div>
    <div class="account"><div class="avatar">{{session?.username.slice(0,1).toUpperCase()}}</div><div class="account-info"><strong :title="session?.username">{{session?.username}}</strong><small>管理员</small></div><button class="icon-btn" aria-label="退出登录" title="退出登录" @click="logout"><LogOut :size="20" :stroke-width="1.8"/></button></div><p v-if="error" class="error">{{error}}</p>
   </div>
 </aside>
 <div class="workspace-main"><header class="topbar"><div><button class="icon-btn sidebar-toggle" aria-label="收起或展开导航" :aria-expanded="!collapsed" @click="collapsed=!collapsed"><PanelLeftClose :size="18"/></button><strong>{{title}}</strong></div></header><main class="page-main"><slot/></main></div>
</div>
</template>
<style scoped>
.workspace{--sidebar-width:256px;--sidebar-bg:#fafbfc;--sidebar-border:#e7e9ec;--sidebar-muted:#98a0aa;--sidebar-hover:#f2f4f5;--sidebar-active:#edf6f2;--sidebar-accent:#208a70}
.sidebar{width:var(--sidebar-width);height:100vh;height:100dvh;padding:0 12px;background:var(--sidebar-bg);border-right:1px solid var(--sidebar-border);overflow:hidden}
.workspace-main{margin-left:var(--sidebar-width)}
.sidebar>.brand{display:flex;gap:12px;min-height:72px;padding:0 8px;margin:0 0 16px;flex-shrink:0;font-size:20px;font-weight:700;letter-spacing:-.4px;color:#17191c}
.sidebar .brand-mark{width:40px;height:40px;min-width:40px;padding:0;background:transparent;border:0;box-shadow:none;border-radius:0}
.sidebar .brand-mark img{display:block;width:40px;height:40px;object-fit:contain}
.sidebar nav{overflow-y:auto;overflow-x:hidden;min-height:0;gap:24px;padding:0 0 16px;flex:0 1 auto;scrollbar-width:thin}
.nav-group{display:flex;flex-direction:column;gap:4px;flex-shrink:0}
.sidebar-caption{padding:0 12px 0 44px;margin:0 0 4px;font-size:12px;line-height:16px;font-weight:500;letter-spacing:.02em;color:var(--sidebar-muted)}
.sidebar nav button{height:44px;padding:0 12px;gap:12px;border:0;border-radius:8px;flex-shrink:0;background:transparent;color:#59616c;font-size:15px;font-weight:500;box-shadow:none;transition:background-color 120ms ease,color 120ms ease}
.sidebar nav button svg{color:#707781;stroke-width:1.8}
.sidebar nav button:hover{background:var(--sidebar-hover);color:#282d32}
.sidebar nav button.active,.sidebar nav button.active:hover{background:var(--sidebar-active);color:#185c4a;font-weight:600;border:0;box-shadow:none}
.sidebar nav button.active svg{color:var(--sidebar-accent)}
.sidebar nav button:focus-visible{outline:2px solid var(--sidebar-accent);outline-offset:-2px}
.sidebar-footer{margin-top:auto;flex-shrink:0;min-width:0;padding-top:16px}
.sidebar .model-box{border-top:1px solid var(--sidebar-border);padding:16px 12px;gap:8px;min-width:0}
.sidebar .model-box .eyebrow{font-size:12px;font-weight:500;letter-spacing:0;color:var(--sidebar-muted)}
.model-name{display:flex;align-items:center;gap:8px;min-width:0}
.sidebar .model-box strong{min-width:0;font-size:14px;font-weight:600;color:#2b3035;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sidebar .signal{width:8px;height:8px;background:var(--sidebar-accent)}
.sidebar .signal.off{background:var(--sidebar-muted)}
.sidebar .model-state{padding-left:16px;font-size:13px;color:#737a83}
.sidebar .account{padding:16px 12px;gap:12px;border-top:1px solid var(--sidebar-border);transition:background-color 120ms ease}
.sidebar .account:hover{background:#f4f5f6}
.sidebar .avatar{width:36px;height:36px;flex-shrink:0;font-size:14px;background:#e9edf0;color:#59616c}
.account-info{min-width:0;flex:1}
.sidebar .account strong{font-size:14px;font-weight:600;max-width:none;color:#2b3035;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sidebar .account small{font-size:12px;color:#8b929a}
.sidebar .account .icon-btn{width:32px;height:32px;flex-shrink:0;padding:4px;margin-left:auto;color:#707781;border:0;background:transparent}
.sidebar .account .icon-btn:hover{color:#282d32;background:#e9edf0}
.sidebar .account svg{stroke-width:1.8}
.sidebar-footer>.error{padding:8px 12px;overflow-wrap:anywhere}
.sidebar-toggle{display:flex}
.collapsed .sidebar{display:none}.collapsed .workspace-main{margin-left:0}
@media(max-width:800px){
 .workspace{--sidebar-width:64px}
 .sidebar{padding:0 8px}
 .sidebar>.brand{display:flex;min-height:72px;padding:0;justify-content:center;margin-bottom:8px}
 .sidebar nav{margin-top:0;gap:16px;padding-bottom:8px}
 .sidebar-caption{display:none}
 .sidebar nav button{justify-content:center;padding:0;height:44px}
 .sidebar-footer{padding-top:8px}
 .sidebar .model-box{display:none}
 .sidebar .account{flex-direction:column;padding:12px 0;gap:8px}
 .sidebar .account .icon-btn{margin:0}
}
</style>
