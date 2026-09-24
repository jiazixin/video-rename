<script setup lang="ts">
import {computed,ref} from 'vue'
import {Hash,History,Undo2} from 'lucide-vue-next'
import {api,time} from '../../app/api'
import Modal from '../../components/Modal.vue'
import './media-numbering.css'
type Batch={id:string;parent:number;status:string;created:string;undo_of:string;error:string;total:number;changed:number;items?:{entry:number;old_name:string;new_name:string}[]}
const props=defineProps<{directory:number;query:string;sort:string;disabled:boolean;media:'image'|'audio'}>()
const noun=computed(()=>props.media==='audio'?'音频':'图片')
const untouched=computed(()=>props.media==='audio'?'视频、图片和目录不变':'视频和目录不变')
const emit=defineEmits<{changed:[]}>()
const busy=ref(false),show=ref(false),history=ref(false),error=ref(''),result=ref(''),preview=ref<Batch>(),records=ref<Batch[]>([]),page=ref(0)
const visible=computed(()=>preview.value?.items?.slice(page.value*100,(page.value+1)*100)||[])
const states:Record<string,string>={committed:'已完成',restored:'已还原',rolled_back:'已恢复原名',review:'待核对',running:'执行中',rollback:'正在恢复'}
let parent=0
async function open(restore?:Batch){
 parent=props.directory;show.value=true;history.value=false;error.value='';result.value='';preview.value=undefined;page.value=0;busy.value=true
 try{preview.value=restore?await api<Batch>(`/numberings/${restore.id}/restore-preview`,'POST',{}):await api<Batch>(`/directories/${parent}/numberings/preview`,'POST',{media:props.media,query:props.query,sort:props.sort})}
 catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
async function loadHistory(){records.value=await api<Batch[]>(`/directories/${parent}/numberings?media=${props.media}`)}
async function openHistory(){parent=props.directory;show.value=true;history.value=true;preview.value=undefined;error.value='';result.value='';busy.value=true;try{await loadHistory()}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
async function apply(){if(!preview.value||busy.value)return;busy.value=true;error.value=''
 try{const batch=await api<Batch>(`/numberings/${preview.value.id}/apply`,'POST',{});result.value=`${batch.undo_of?'已还原':'已编号'} ${batch.changed} 张${noun.value}`;preview.value=undefined;history.value=true;await loadHistory()}
 catch(e){error.value=(e as Error).message}
 finally{busy.value=false;emit('changed')}
}
async function recover(batch:Batch){busy.value=true;error.value='';result.value='';try{const updated=await api<Batch>(`/numberings/${batch.id}/recover`,'POST',{});if(updated.status==='review')error.value=updated.error;else result.value=updated.error;await loadHistory()}catch(e){error.value=(e as Error).message}finally{busy.value=false;emit('changed')}}
</script>
<template>
 <button :disabled="disabled||busy" @click="open()"><Hash :size="16"/>一键编号</button>
 <button :disabled="!directory||busy" @click="openHistory"><History :size="16"/>编号记录</button>
 <Modal v-if="show" :title="history?`${noun}编号记录`:preview?.undo_of?`还原${noun}编号`:`${noun}编号预览`" wide @close="!busy&&(show=false)">
  <p v-if="busy" role="status">正在处理，请稍候…</p>
  <p v-if="result" class="notice success" role="status">{{result}}</p>
  <div v-if="error" class="notice error" role="alert">{{error}}</div>
  <template v-if="preview">
   <p>共 {{preview.total}} 张{{noun}}，{{preview.changed}} 张需要{{preview.undo_of?'还原':'修改'}}，{{preview.total-preview.changed}} 张保持不变。</p>
   <p v-if="!preview.undo_of" class="help">仅当前目录内符合搜索条件的{{noun}}，包含所有分页。已有唯一编号保留，重复编号按当前排序保留第一张，其余从最大编号后续排；不同格式共用序号，扩展名小写。搜索隐藏的{{noun}}会保留其号码。{{untouched}}。</p>
   <p v-else class="help">整批恢复本次编号前的名称和扩展名大小写。文件有后续修改或名称被占用时会停止。</p>
   <div class="table-scroll numbering-preview"><table><thead><tr><th>原名</th><th>{{preview.undo_of?'还原为':'新名'}}</th></tr></thead><tbody><tr v-for="item in visible" :key="item.entry"><td>{{item.old_name}}</td><td>{{item.new_name}}<small v-if="item.old_name===item.new_name">（不变）</small></td></tr></tbody></table></div>
   <div class="pagination"><span>第 {{page+1}} / {{Math.max(1,Math.ceil(preview.total/100))}} 页</span><button :disabled="page===0||busy" @click="page--">上页</button><button :disabled="(page+1)*100>=preview.total||busy" @click="page++">下页</button></div>
   <div class="modal-actions"><button :disabled="busy" @click="show=false">取消</button><button class="primary" :disabled="busy||!preview.changed" @click="apply">{{preview.undo_of?'确认整批还原':'确认编号'}}</button></div>
  </template>
  <template v-if="history">
   <p class="help">当前目录最近 50 次操作。未完成的操作会在重启时尝试恢复，也可手动重试恢复。</p>
   <article v-for="batch in records" :key="batch.id" class="numbering-record"><div><strong>{{batch.undo_of?'整批还原':`${noun}编号`}} · {{states[batch.status]||batch.status}}</strong><p>{{time(batch.created)}} · {{batch.changed}} 张修改 / {{batch.total}} 张{{noun}}</p><p v-if="batch.error">{{batch.error}}</p></div><button v-if="batch.status==='committed'&&!batch.undo_of&&batch.changed" :disabled="busy" @click="open(batch)"><Undo2 :size="15"/>还原本次编号</button><button v-if="['review','running','rollback'].includes(batch.status)" :disabled="busy" @click="recover(batch)">重试恢复原名</button></article>
   <p v-if="!records.length&&!busy" class="empty compact">暂无编号记录</p>
  </template>
  <div v-if="!preview" class="modal-actions"><button :disabled="busy" @click="show=false">关闭</button></div>
 </Modal>
</template>
