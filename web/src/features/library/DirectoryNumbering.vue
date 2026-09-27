<script setup lang="ts">
import {computed,ref,onDeactivated,watch,nextTick} from 'vue'
import {useNumberingTask} from './numbering-task'
import {Hash,History,Undo2} from 'lucide-vue-next'
import {api,ApiError} from '../../app/api'
import DateTime from '../../components/DateTime.vue'
import Modal from '../../components/Modal.vue'
import NumberingDiagnostics from './NumberingDiagnostics.vue'
import './media-numbering.css'

type Batch={id:string;parent:number;path:string;status:string;created:string;undo_of:string;error:string;total:number;changed:number;items?:{entry:number;old_name:string;new_name:string}[]}
const confirmingClose=ref(false),continueButton=ref<HTMLButtonElement>()
const task=useNumberingTask()
const progress=task.progress
const previewKey=ref(''),remoteItems=ref<{entry:number;old_name:string;new_name:string;path:string}[]>([])
const props=defineProps<{directory:number;disabled:boolean}>()
const emit=defineEmits<{changed:[]}>()
const show=ref(false),busy=ref(false),history=ref(false),diagnostics=ref(''),error=ref(''),result=ref(''),batches=ref<Batch[]>([]),records=ref<Batch[]>([]),page=ref(0)
const total=computed(()=>batches.value.reduce((sum,b)=>sum+b.total,0))
const changed=computed(()=>batches.value.reduce((sum,b)=>sum+b.changed,0))
const items=computed(()=>batches.value.flatMap(b=>(b.items||[]).map(item=>({...item,path:b.path}))))
const visible=computed(()=>previewKey.value?remoteItems.value:items.value.slice(page.value*100,(page.value+1)*100))
const restoring=computed(()=>!!batches.value[0]?.undo_of)
const states:Record<string,string>={committed:'已完成',restored:'已还原',rolled_back:'已恢复原名',review:'待核对',running:'执行中',rollback:'正在恢复'}
let parent=0
function close(){show.value=false;confirmingClose.value=false;task.cancel()}
async function requestClose(){
 if(confirmingClose.value){confirmingClose.value=false;return}
 if(!busy.value){close();return}
 confirmingClose.value=true
 await nextTick();continueButton.value?.focus()
}
onDeactivated(close)
watch(()=>props.directory,()=>{if(show.value)close()})
function report(e:unknown){if(!show.value)return;diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}
async function loadPage(next:number){
 busy.value=true
 try{
  if(previewKey.value){
   const data=await api<{batches:Batch[];items:typeof remoteItems.value}>(`/numbering/jobs/${previewKey.value}/result?offset=${next*100}`)
   if(!show.value)return
   batches.value=data.batches;remoteItems.value=data.items
  }
  page.value=next
 }catch(e){report(e)}finally{busy.value=false}
}
async function preview(){
 history.value=false;error.value='';diagnostics.value='';result.value='';batches.value=[];previewKey.value='';page.value=0;busy.value=true
 try{const state=await task.run(parent,'preview');if(!show.value)return;previewKey.value=state.key;await loadPage(0)}
 catch(e){report(e)}finally{busy.value=false}
}
function open(){parent=props.directory;show.value=true;preview()}
async function loadHistory(){records.value=await api<Batch[]>(`/directories/${parent}/numberings/tree`)}
async function openHistory(){history.value=true;error.value='';diagnostics.value='';busy.value=true;try{await loadHistory()}catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}finally{busy.value=false}}
async function restore(batch:Batch){
 busy.value=true;error.value='';diagnostics.value='';result.value='';page.value=0
 previewKey.value=''
 try{const undo=await api<Batch>(`/numberings/${batch.id}/restore-preview`,'POST',{});batches.value=[{...undo,path:batch.path}];history.value=false}
 catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}finally{busy.value=false}
}
async function apply(){
 if(busy.value||!changed.value)return
 busy.value=true;error.value='';diagnostics.value=''
 const pending=batches.value.filter(b=>b.changed),undo=restoring.value
 try{
  const state=await task.run(parent,'apply',previewKey.value?undefined:pending.map(b=>b.id),previewKey.value||undefined)
  if(show.value)result.value=`已${undo?'还原':'编号'} ${state.files} 个文件，完成 ${state.done} 个目录。`
 }catch(e){report(e)}
 finally{
  batches.value=[];previewKey.value='';history.value=true;emit('changed')
  if(show.value){try{await loadHistory()}catch(e){report(e)}}
  busy.value=false
 }
}
async function recover(batch:Batch){busy.value=true;error.value='';diagnostics.value='';try{const updated=await api<Batch>(`/numberings/${batch.id}/recover`,'POST',{});if(updated.status==='review')error.value=updated.error;else result.value=updated.error;await loadHistory()}catch(e){diagnostics.value=e instanceof ApiError?e.processingLog:'';error.value=(e as Error).message}finally{busy.value=false;emit('changed')}}
</script>
<template>
 <button class="library-action action-number" :disabled="disabled||busy" @click="open"><Hash :size="16"/>编号</button>
 <Modal v-if="show" :title="confirmingClose?'停止当前处理？':history?'目录编号记录':restoring?'还原目录编号':'目录编号预览'" wide contained @close="requestClose">
  <div v-if="confirmingClose" class="cancel-confirmation">
   <p>确定停止并关闭编号窗口吗？</p>
   <p class="help">索引或预览将停止，重新打开需要重新预览。正在编号时，当前未完成目录会恢复原名，已完成目录保留。</p>
  </div>
  <div v-else class="numbering-body">
  <p class="help">当前目录及所有层级子目录的图片和音频，按文件夹独立编号，不受搜索、类型筛选或分页影响。使用已保存的编号规则，按名称自然升序排列；默认图片、音频分别编号，启用共用序号时先图片后音频。</p>
  <div class="numbering-tools"><button :disabled="busy" @click="preview">重新预览</button><button :disabled="busy" @click="openHistory"><History :size="15"/>编号记录</button></div>
  <p v-if="busy" class="help">关闭前会再次确认；正在改名的目录会安全恢复，已完成的目录保留。</p>
  <p v-if="busy" role="status">{{progress||'正在处理，请稍候…'}}</p>
  <p v-if="result" class="notice success" role="status">{{result}}</p>
  <p v-if="error" class="notice error" role="alert">{{error}}</p>
  <NumberingDiagnostics v-if="diagnostics" :message="error" :diagnostics="diagnostics"/>
  <template v-if="!history&&batches.length">
   <p>共 {{batches.length}} 个目录、{{total}} 个文件，{{changed}} 个需要{{restoring?'还原':'修改'}}，{{total-changed}} 个保持不变。</p>
   <div class="table-scroll numbering-preview"><table><thead><tr><th>所在目录</th><th>原名</th><th>{{restoring?'还原为':'新名'}}</th></tr></thead><tbody><tr v-for="item in visible" :key="item.entry"><td>{{item.path}}</td><td>{{item.old_name}}</td><td>{{item.new_name}}<small v-if="item.old_name===item.new_name">（不变）</small></td></tr></tbody></table></div>
   <div class="pagination"><span>第 {{page+1}} / {{Math.max(1,Math.ceil(total/100))}} 页</span><button :disabled="!page||busy" @click="loadPage(page-1)">上页</button><button :disabled="(page+1)*100>=total||busy" @click="loadPage(page+1)">下页</button></div>
  </template>
  <p v-else-if="!history&&!busy&&!error" class="empty compact">当前目录及子目录中没有可编号的图片或音频</p>
  <div v-if="history" class="numbering-history">
   <p class="help">当前目录及子目录最近 100 次编号操作，按文件夹记录；支持刷新后还原。</p>
   <article v-for="batch in records" :key="batch.id" class="numbering-record"><div><strong>{{batch.path}} · {{batch.undo_of?'还原':'编号'}} · {{states[batch.status]||batch.status}}</strong><p class="numbering-record-meta"><DateTime :value="batch.created"/><span>{{batch.changed}} 个修改 / {{batch.total}} 个文件</span></p><p v-if="batch.error">{{batch.error}}</p></div><button v-if="batch.status==='committed'&&!batch.undo_of&&batch.changed" :disabled="busy" @click="restore(batch)"><Undo2 :size="15"/>还原本次编号</button><button v-if="['review','running','rollback'].includes(batch.status)" :disabled="busy" @click="recover(batch)">重试恢复原名</button></article>
   <p v-if="!records.length&&!busy" class="empty compact">暂无编号记录</p>
  </div>
  </div>
  <template #footer>
  <div v-if="confirmingClose" class="modal-actions"><button ref="continueButton" class="primary" @click="confirmingClose=false">继续处理</button><button @click="close">确定停止并关闭</button></div>
  <div v-else class="modal-actions"><button @click="requestClose">{{busy?'停止并关闭':'关闭'}}</button><button v-if="!history&&batches.length" class="primary" :disabled="busy||!changed" @click="apply">{{restoring?'确认整批还原':'确认编号'}}</button></div>
  </template>
 </Modal>
</template>
<style scoped>
.numbering-body{display:flex;flex-direction:column;min-height:0;overflow:hidden}.numbering-body>p,.numbering-tools,.numbering-body>.pagination{flex-shrink:0}.numbering-body>.help:first-child{margin-top:0}.numbering-body .numbering-preview{min-height:0;flex:1 1 auto;max-height:45vh;overflow:auto}.numbering-history{min-height:0;overflow:auto}.numbering-body>.pagination{padding:10px 0}.cancel-confirmation{padding:8px 0}.numbering-tools{display:flex;gap:8px;margin-bottom:16px}.numbering-preview th:first-child{width:30%}
</style>
