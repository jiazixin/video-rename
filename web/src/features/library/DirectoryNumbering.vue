<script setup lang="ts">
import {computed,ref} from 'vue'
import {Hash,History,Undo2} from 'lucide-vue-next'
import {api,ApiError} from '../../app/api'
import DateTime from '../../components/DateTime.vue'
import Modal from '../../components/Modal.vue'
import './media-numbering.css'

type Batch={id:string;parent:number;path:string;status:string;created:string;undo_of:string;error:string;total:number;changed:number;items?:{entry:number;old_name:string;new_name:string}[]}
const props=defineProps<{directory:number;disabled:boolean}>()
const emit=defineEmits<{changed:[]}>()
const show=ref(false),busy=ref(false),history=ref(false),diagnostics=ref(''),copyStatus=ref(''),error=ref(''),result=ref(''),progress=ref(''),batches=ref<Batch[]>([]),records=ref<Batch[]>([]),page=ref(0)
const total=computed(()=>batches.value.reduce((sum,b)=>sum+b.total,0))
const changed=computed(()=>batches.value.reduce((sum,b)=>sum+b.changed,0))
const items=computed(()=>batches.value.flatMap(b=>(b.items||[]).map(item=>({...item,path:b.path}))))
const visible=computed(()=>items.value.slice(page.value*100,(page.value+1)*100))
const restoring=computed(()=>!!batches.value[0]?.undo_of)
const states:Record<string,string>={committed:'已完成',restored:'已还原',rolled_back:'已恢复原名',review:'待核对',running:'执行中',rollback:'正在恢复'}
let parent=0
async function preview(){
 history.value=false;error.value='';diagnostics.value='';copyStatus.value='';result.value='';batches.value=[];page.value=0;busy.value=true;progress.value='正在索引当前目录及所有子目录…'
 try{batches.value=await api<Batch[]>(`/directories/${parent}/numberings/tree-preview`,'POST',{})}
 catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}finally{busy.value=false;progress.value=''}
}
function open(){parent=props.directory;show.value=true;preview()}
async function loadHistory(){records.value=await api<Batch[]>(`/directories/${parent}/numberings/tree`)}
async function openHistory(){history.value=true;error.value='';diagnostics.value='';copyStatus.value='';busy.value=true;try{await loadHistory()}catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}finally{busy.value=false}}
async function restore(batch:Batch){
 busy.value=true;error.value='';diagnostics.value='';copyStatus.value='';result.value='';page.value=0
 try{const undo=await api<Batch>(`/numberings/${batch.id}/restore-preview`,'POST',{});batches.value=[{...undo,path:batch.path}];history.value=false}
 catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}finally{busy.value=false}
}
async function apply(){
 if(busy.value||!changed.value)return
 busy.value=true;error.value='';diagnostics.value='';copyStatus.value='';let done=0,files=0
 const pending=batches.value.filter(b=>b.changed),undo=restoring.value
 try{
  for(const batch of pending){
   progress.value=`正在${undo?'还原':'编号'} ${done+1} / ${pending.length} 个目录：${batch.path}`
   await api(`/numberings/${batch.id}/apply`,'POST',{});done++;files+=batch.changed
  }
  result.value=`已${undo?'还原':'编号'} ${files} 个文件，完成 ${done} 个目录。`
 }catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=`${(e as Error).message}。已完成 ${done} 个目录、${files} 个文件；剩余目录未执行，请重新预览。`}
 finally{
  batches.value=[];history.value=true;progress.value='';emit('changed')
  try{await loadHistory()}catch(e){error.value+=(error.value?'\n':'')+(e as Error).message}
  busy.value=false
 }
}
async function recover(batch:Batch){busy.value=true;error.value='';diagnostics.value='';copyStatus.value='';try{const updated=await api<Batch>(`/numberings/${batch.id}/recover`,'POST',{});if(updated.status==='review')error.value=updated.error;else result.value=updated.error;await loadHistory()}catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}finally{busy.value=false;emit('changed')}}
async function copyDiagnostics(){
 try{await navigator.clipboard.writeText(error.value+'\n'+diagnostics.value);copyStatus.value='已复制诊断'}
 catch{copyStatus.value='浏览器不允许复制，请手动选择下方文本'}
}
</script>
<template>
 <button class="library-action action-number" :disabled="disabled||busy" @click="open"><Hash :size="16"/>编号</button>
 <Modal v-if="show" :title="history?'目录编号记录':restoring?'还原目录编号':'目录编号预览'" wide @close="!busy&&(show=false)">
  <p class="help">当前目录及所有层级子目录的图片和音频，按文件夹独立编号，不受搜索、类型筛选或分页影响。使用已保存的编号规则，按名称自然升序排列；默认图片、音频分别编号，启用共用序号时先图片后音频。</p>
  <div class="numbering-tools"><button :disabled="busy" @click="preview">重新预览</button><button :disabled="busy" @click="openHistory"><History :size="15"/>编号记录</button></div>
  <p v-if="busy" role="status">{{progress||'正在处理，请稍候…'}}</p>
  <p v-if="result" class="notice success" role="status">{{result}}</p>
  <p v-if="error" class="notice error" role="alert">{{error}}</p>
  <details v-if="diagnostics" class="numbering-diagnostics"><summary>查看诊断详情</summary><button @click="copyDiagnostics">复制诊断</button><span role="status">{{copyStatus}}</span><pre>{{diagnostics}}</pre></details>
  <template v-if="!history&&batches.length">
   <p>共 {{batches.length}} 个目录、{{total}} 个文件，{{changed}} 个需要{{restoring?'还原':'修改'}}，{{total-changed}} 个保持不变。</p>
   <div class="table-scroll numbering-preview"><table><thead><tr><th>所在目录</th><th>原名</th><th>{{restoring?'还原为':'新名'}}</th></tr></thead><tbody><tr v-for="item in visible" :key="item.entry"><td>{{item.path}}</td><td>{{item.old_name}}</td><td>{{item.new_name}}<small v-if="item.old_name===item.new_name">（不变）</small></td></tr></tbody></table></div>
   <div class="pagination"><span>第 {{page+1}} / {{Math.max(1,Math.ceil(total/100))}} 页</span><button :disabled="!page||busy" @click="page--">上页</button><button :disabled="(page+1)*100>=total||busy" @click="page++">下页</button></div>
  </template>
  <p v-else-if="!history&&!busy&&!error" class="empty compact">当前目录及子目录中没有可编号的图片或音频</p>
  <template v-if="history">
   <p class="help">当前目录及子目录最近 100 次编号操作，按文件夹记录；支持刷新后还原。</p>
   <article v-for="batch in records" :key="batch.id" class="numbering-record"><div><strong>{{batch.path}} · {{batch.undo_of?'还原':'编号'}} · {{states[batch.status]||batch.status}}</strong><p class="numbering-record-meta"><DateTime :value="batch.created"/><span>{{batch.changed}} 个修改 / {{batch.total}} 个文件</span></p><p v-if="batch.error">{{batch.error}}</p></div><button v-if="batch.status==='committed'&&!batch.undo_of&&batch.changed" :disabled="busy" @click="restore(batch)"><Undo2 :size="15"/>还原本次编号</button><button v-if="['review','running','rollback'].includes(batch.status)" :disabled="busy" @click="recover(batch)">重试恢复原名</button></article>
   <p v-if="!records.length&&!busy" class="empty compact">暂无编号记录</p>
  </template>
  <div class="modal-actions"><button :disabled="busy" @click="show=false">关闭</button><button v-if="!history&&batches.length" class="primary" :disabled="busy||!changed" @click="apply">{{restoring?'确认整批还原':'确认编号'}}</button></div>
 </Modal>
</template>
<style scoped>
.numbering-tools{display:flex;gap:8px;margin-bottom:16px}.numbering-preview th:first-child{width:30%}
</style>
