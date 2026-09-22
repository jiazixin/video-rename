<script setup lang="ts">
import { onMounted,onUnmounted,ref,watch } from 'vue'
import { api,bootstrap,session } from './api'
import Login from '../features/auth/Login.vue'
import Workspace from '../layouts/Workspace.vue'
import Library from '../features/library/Library.vue'
import History from '../features/history/History.vue'
import Runs from '../features/runs/Runs.vue'
import Notifications from '../features/notifications/Notifications.vue'
import Models from '../features/models/Models.vue'
const page=ref('library'),loading=ref(true),system=ref({model:'',ready:false,message:''})
let libraryHash=location.hash.startsWith('#library/')?location.hash.slice(1):'library'
function navigate(v:string){location.hash=v==='library'?libraryHash:v;page.value=v}
function hash(){if(location.hash.startsWith('#library'))libraryHash=location.hash.slice(1);const name=location.hash.slice(1).split('/')[0];page.value=['library','history','runs','models','notifications'].includes(name)?name:'library'}
async function loadSystem(){if(session.value){try{system.value=await api('/system')}catch{}}}
onMounted(async()=>{hash();window.addEventListener('hashchange',hash);await bootstrap();await loadSystem();loading.value=false})
watch(session,async value=>{if(value){try{await api('/notifications/timezone','PUT',{timezone:Intl.DateTimeFormat().resolvedOptions().timeZone})}catch{}}})
onUnmounted(()=>window.removeEventListener('hashchange',hash));watch(session,loadSystem)
</script>
<template><div v-if="loading" class="boot">正在打开工作台…</div><Login v-else-if="!session"/><Workspace v-else :page="page" :model="system.model" :ready="system.ready" @navigate="navigate"><div v-if="system.message" class="notice error">{{system.message}}。文件浏览和手动改名仍可使用。</div><Library v-if="page==='library'" :ready="system.ready" @run="navigate('runs')"/><History v-else-if="page==='history'"/><Models v-else-if="page==='models'" @changed="loadSystem"/><Notifications v-else-if="page==='notifications'"/><Runs v-else :ready="system.ready"/></Workspace></template>
