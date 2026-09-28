<script setup lang="ts">
import ToastNotice from '../../components/ToastNotice.vue'
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {AlertCircle,CheckCircle2,ChevronLeft,ChevronRight,Copy,Search,RefreshCw,Trash2} from 'lucide-vue-next'
import {api} from '../../app/api'
import DateTime from '../../components/DateTime.vue'
import DateField from '../../components/DateField.vue'
import SelectField from '../../components/SelectField.vue'
import Modal from '../../components/Modal.vue'

type RecordItem={id:number;run:number;entry:number;path:string;source:string;status:string;started:string;finished:string;duration_ms:number;cached:boolean;frames:number;message:string}
type Group={items:RecordItem[];total:number;next:number}
type Report={success:Group;failure:Group;running:number}
type Category='success'|'failure'
const groups=[{id:'success' as Category,label:'抽帧成功'},{id:'failure' as Category,label:'抽帧失败'}]
const category=ref<Category>('success')
const report=ref<Report>({success:{items:[],total:0,next:0},failure:{items:[],total:0,next:0},running:0})
const busy=ref(false),error=ref(''),message=ref(''),q=ref(''),run=ref(''),from=ref(''),to=ref(''),limit=ref(50),batches=ref<number[]>([])
const before=ref({success:0,failure:0}),pages=ref<Record<Category,number[]>>({success:[],failure:[]})
const batchOptions=computed(()=>[{value:'',label:'全部批次'},...batches.value.map(id=>({value:String(id),label:`批次 #${id}`}))])
const selected=ref<RecordItem>(),diagnostics=ref(''),detailError=ref(''),detailBusy=ref(false)
const clearOpen=ref(false),clearing=ref(false),clearError=ref('')
let seq=0,detailSeq=0,controller:AbortController|undefined,debounce:ReturnType<typeof setTimeout>|undefined
function duration(ms:number){if(ms<1000)return `${ms}ms`;const s=Math.floor(ms/1000);return [s>=86400?`${Math.floor(s/86400)}d`:'',s>=3600?`${Math.floor(s%86400/3600)}h`:'',s>=60?`${Math.floor(s%3600/60)}m`:'',`${s%60}s`].join('')}
function pathParts(path:string){const i=path.lastIndexOf('/');return {directory:i<0?'媒体库':path.slice(0,i)||'媒体库',name:path.slice(i+1)||'—'}}
async function load(){
 const n=++seq;controller?.abort();controller=new AbortController();busy.value=true;error.value=''
 try{
  const params=new URLSearchParams({limit:String(limit.value),q:q.value,run:run.value,success_before:String(before.value.success),failure_before:String(before.value.failure),from:from.value?new Date(from.value+'T00:00:00').toISOString():'',to:to.value?new Date(to.value+'T23:59:59.999').toISOString():''})
  const [data,ids]=await Promise.all([api<Report>(`/extractions?${params}`,'GET',undefined,controller.signal),api<number[]>('/logs/batches?category=extraction','GET',undefined,controller.signal)])
  if(n===seq){report.value=data;batches.value=ids;if(run.value&&!ids.includes(Number(run.value)))batches.value=[...ids,Number(run.value)].sort((a,b)=>b-a)}
 }catch(e){if(n===seq)error.value=(e as Error).message}finally{if(n===seq)busy.value=false}
}
function reset(){clearTimeout(debounce);before.value={success:0,failure:0};pages.value={success:[],failure:[]};load()}
function search(){clearTimeout(debounce);debounce=setTimeout(reset,250)}
function next(group:Category){pages.value[group].push(before.value[group]);before.value[group]=report.value[group].next;load()}
function previous(group:Category){before.value[group]=pages.value[group].pop()||0;load()}
async function inspect(row:RecordItem){
 selected.value=row;diagnostics.value='';detailError.value='';detailBusy.value=true;const n=++detailSeq
 try{const result=await api<{processing_log:string}>(`/extractions/${row.id}`);if(n===detailSeq)diagnostics.value=result.processing_log}catch(e){if(n===detailSeq)detailError.value=(e as Error).message}finally{if(n===detailSeq)detailBusy.value=false}
}
async function copy(){try{await navigator.clipboard.writeText(JSON.stringify({...selected.value,processing_log:diagnostics.value},null,2));message.value='已复制抽帧详情'}catch{detailError.value='浏览器不允许复制，请手动选取文本'}}
function openClear(){clearError.value='';clearOpen.value=true}
async function clear(){clearing.value=true;clearError.value='';try{const result=await api('/extractions/clear','POST',{all:true});message.value=result.message;clearOpen.value=false;reset()}catch(e){clearError.value=(e as Error).message}finally{clearing.value=false}}
const timer=setInterval(()=>{if(!busy.value&&document.visibilityState==='visible')load()},3000)
onMounted(load)
onUnmounted(()=>{seq++;detailSeq++;controller?.abort();clearInterval(timer);clearTimeout(debounce)})
</script>

<template>
<section>
 <div class="page-heading"><div><h1>抽帧记录<span class="title-dot">.</span></h1><p>查看视频抽帧结果、缓存复用情况与失败原因。</p></div><div class="history-heading-actions"><button @click="openClear"><Trash2 :size="16"/>清除记录</button><button :disabled="busy" @click="reset"><RefreshCw :size="16" :class="{spin:busy}"/>刷新</button></div></div>
 <div class="log-tabs" aria-label="抽帧记录状态"><button :class="{active:category==='success'}" :aria-pressed="category==='success'" @click="category='success'"><CheckCircle2 :size="18"/>成功记录</button><button :class="{active:category==='failure'}" :aria-pressed="category==='failure'" @click="category='failure'"><AlertCircle :size="18"/>失败记录</button></div>
 <div class="extraction-history">
 <div v-if="error" role="alert" class="notice error">{{error}}<button @click="load">重试</button></div>
 <ToastNotice v-model="message" />
 <div class="panel filterbar extraction-filters">
  <div class="search-input"><Search :size="16"/><input v-model="q" aria-label="搜索抽帧记录" placeholder="搜索视频名称或失败原因…" @input="search"/></div>
  <SelectField v-model="run" label="抽帧批次" :options="batchOptions" @change="reset"/>
  <DateField v-model="from" label="起始日期" :max="to" @change="reset"/><span class="subtle">至</span><DateField v-model="to" label="结束日期" :min="from" @change="reset"/>
  <div class="filter-spacer"/><span class="subtle">共 {{report[category].total}} 项 · {{report[category].items.length}} 项 / 当前页</span>
 </div>
 <p class="help extraction-hint">手动抽帧与重命名过程中的自动抽帧统一记录；有效缓存复用会单独标注。<span v-if="report.running" role="status">当前 {{report.running}} 项正在抽帧，完成后自动更新。</span></p>
  <section v-for="group in groups.filter(g=>g.id===category)" :key="group.id" class="panel extraction-group" :aria-label="group.label">
  <div class="table-scroll"><table class="extraction-table">
   <thead><tr><th>开始时间</th><th>结束时间</th><th>总耗时</th><th>批次</th><th>来源</th><th>目录</th><th>文件名</th><th>{{group.id==='success'?'抽帧结果':'失败原因'}}</th></tr></thead>
   <tbody><tr v-for="row in report[group.id].items" :key="row.id">
    <td v-for="(stamp,index) in [row.started,row.finished]" :key="index" class="subtle date-cell"><DateTime :value="stamp"/></td>
    <td class="mono">{{duration(row.duration_ms)}}</td><td>{{row.run?`#${row.run}`:'—'}}</td><td><span class="action-badge" :data-action="row.source">{{row.source==='manual'?'手动抽帧':'自动抽帧'}}</span></td>
    <td><span class="log-directory" :title="pathParts(row.path).directory">{{pathParts(row.path).directory}}</span></td><td><span class="history-filename" :title="pathParts(row.path).name">{{pathParts(row.path).name}}</span></td>
    <td v-if="group.id==='success'"><span>{{row.cached?'复用缓存':'抽帧完成'}} · {{row.frames}} 帧</span></td><td v-else class="extraction-reason"><p>{{row.message||'抽帧失败'}}</p><button class="text-button" @click="inspect(row)">详细原因</button></td>
   </tr></tbody>
  </table><div v-if="!report[group.id].items.length" class="empty compact">{{busy?'正在读取抽帧记录…':`暂无${group.label}记录`}}</div></div>
   <div class="pagination"><span class="subtle">按时间从新到旧</span><SelectField v-model="limit" label="抽帧每页条目数" :options="[{value:50,label:'50 项 / 页'},{value:100,label:'100 项 / 页'},{value:200,label:'200 项 / 页'}]" @change="reset"/><button :disabled="busy||!pages[group.id].length" :aria-label="`上一页${group.label}`" @click="previous(group.id)"><ChevronLeft :size="16"/></button><span>{{pages[group.id].length+1}}</span><button :disabled="busy||!report[group.id].next" :aria-label="`下一页${group.label}`" @click="next(group.id)"><ChevronRight :size="16"/></button></div>
 </section>
</div>
</section>
<Modal v-if="selected" title="抽帧失败详情" wide @close="selected=undefined;detailSeq++">
 <dl class="detail-list"><dt>文件</dt><dd>{{selected.path}}</dd><dt>批次</dt><dd>{{selected.run?`#${selected.run}`:'—'}}</dd><dt>开始时间</dt><dd><DateTime :value="selected.started"/></dd><dt>结束时间</dt><dd><DateTime :value="selected.finished"/></dd><dt>总耗时</dt><dd>{{duration(selected.duration_ms)}}</dd><dt>失败原因</dt><dd>{{selected.message}}</dd></dl>
 <p v-if="detailBusy" role="status">正在读取详细原因…</p><div v-if="detailError" class="notice error" role="alert">{{detailError}}<button @click="inspect(selected)">重试</button></div><pre v-if="diagnostics" class="extraction-diagnostics">{{diagnostics}}</pre><p v-else-if="!detailBusy&&!detailError" class="help">{{selected.message}}</p>
 <div class="modal-actions"><button :disabled="detailBusy" @click="copy"><Copy :size="15"/>复制详情</button><button class="primary" @click="selected=undefined;detailSeq++">关闭</button></div>
</Modal>
<Modal v-if="clearOpen" title="清除抽帧记录" @close="!clearing&&(clearOpen=false)"><p>清除全部已结束的抽帧成功和失败记录（含其他页）。</p><p class="help">正在抽帧的记录保留。清除日志后，已有截图缓存仍可使用。</p><div v-if="clearError" class="notice error" role="alert">{{clearError}}</div><div class="modal-actions"><button :disabled="clearing" @click="clearOpen=false">取消</button><button class="cancel-run" :disabled="clearing" @click="clear">{{clearing?'正在清除…':'确认清除'}}</button></div></Modal>
</template>

<style scoped>
.extraction-history{display:grid;gap:18px;min-width:0}
.extraction-filters{flex-wrap:wrap;gap:12px}
.extraction-hint{margin:0}.extraction-hint span{margin-left:8px;color:var(--ink)}
.extraction-heading{display:flex;align-items:center;justify-content:space-between;padding:18px 22px;border-bottom:1px solid var(--line)}
.extraction-heading h2{display:flex;align-items:center;gap:8px;margin:0;font-size:15px}
.extraction-group[aria-label="抽帧成功"] h2{color:#0e8a5c}.extraction-group[aria-label="抽帧失败"] h2{color:#b14545}
.extraction-table{min-width:1080px;width:100%;font-size:12px}.extraction-table th{white-space:nowrap}
.extraction-table .history-filename,.extraction-table .log-directory{max-width:180px}
.extraction-reason{min-width:220px;max-width:330px}.extraction-reason p{margin:0 0 6px;overflow-wrap:anywhere;white-space:pre-wrap}
.extraction-diagnostics{max-height:420px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;background:var(--surface);padding:16px;border:1px solid var(--line);border-radius:8px;font-size:12px}
@media(max-width:700px){.extraction-heading{padding:14px}.extraction-filters>*{min-width:0}.extraction-filters .search-input{flex-basis:100%}.extraction-heading>.subtle{font-size:11px}}
</style>
