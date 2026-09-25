<script setup lang="ts">
import {computed,ref,onMounted,onUnmounted} from 'vue'
import {CheckCircle2,AlertCircle,Search,Info,RefreshCw,ChevronLeft,ChevronRight,ScrollText,Copy,Undo2,Trash2,Images} from 'lucide-vue-next'
import {api,key,time,datePart,clockPart,actionNames} from '../../app/api'
import DateField from '../../components/DateField.vue'
import Modal from '../../components/Modal.vue'
import SelectField from '../../components/SelectField.vue'
import FailureDiagnostics from './FailureDiagnostics.vue'
import ExtractionHistory from './ExtractionHistory.vue'
const extractionHistory=ref<InstanceType<typeof ExtractionHistory>>()
const category=ref('success'),rows=ref<any[]>([]),busy=ref(false),error=ref(''),q=ref(''),action=ref(''),from=ref(''),to=ref(''),before=ref(0),pages=ref<number[]>([]),selected=ref<any>(),detailError=ref(''),message=ref('')
const limit=ref(50),total=ref(0),nextCursor=ref(0),retrying=ref(false),retryRuns=ref<number[]>([])
const retryable=computed(()=>rows.value.filter(r=>chosen.value.has(r.id)&&r.action==='automatic'&&r.entry>0))
const run=ref(''),batches=ref<number[]>([])
const batchOptions=computed(()=>[{value:'',label:'全部批次'},...batches.value.map(id=>({value:String(id),label:`批次 #${id}`}))])
const chosen=ref(new Set<number>()),clearOpen=ref(false),clearAll=ref(false),clearing=ref(false),clearError=ref('')
function toggleRow(id:number){chosen.value.has(id)?chosen.value.delete(id):chosen.value.add(id)}
function selectPage(){if(rows.value.every(r=>chosen.value.has(r.id)))chosen.value.clear();else chosen.value=new Set(rows.value.map(r=>r.id))}
function openClear(){clearAll.value=!chosen.value.size;clearError.value='';clearOpen.value=true}
async function clearHistory(){if(clearing.value)return;clearing.value=true;clearError.value='';try{const result=await api('/logs/clear','POST',{category:category.value,all:clearAll.value,ids:clearAll.value?[]:[...chosen.value]});message.value=result.message;clearOpen.value=false;reset()}catch(e){clearError.value=(e as Error).message}finally{clearing.value=false}}
let seq=0,debounce:ReturnType<typeof setTimeout>|undefined
async function load(preserve=false){if(category.value==='extraction')return;if(!preserve)chosen.value.clear();const n=++seq;busy.value=true;if(!preserve)error.value='';try{const params=new URLSearchParams({paged:'1',limit:String(limit.value),category:category.value,run:run.value,q:q.value,action:action.value,before:String(before.value),from:from.value?new Date(from.value+'T00:00:00').toISOString():'',to:to.value?new Date(to.value+'T23:59:59.999').toISOString():''});const [data,available]=await Promise.all([api<any>(`/logs?${params}`),api<number[]>('/logs/batches')]);if(n===seq){rows.value=Array.isArray(data)?data:data.items;total.value=Array.isArray(data)?data.length:data.total;nextCursor.value=Array.isArray(data)?0:data.next;chosen.value=new Set([...chosen.value].filter(id=>rows.value.some(r=>r.id===id)));batches.value=available;if(run.value&&!available.includes(Number(run.value)))batches.value=[...available,Number(run.value)].sort((a,b)=>b-a)}}catch(e){if(n===seq)error.value=(e as Error).message}finally{if(n===seq)busy.value=false}}
function tab(value:string){seq++;busy.value=false;error.value='';message.value='';category.value=value;if(value!=='extraction')reset()}
function pathParts(path: string){
 if(!path)return {directory:'—',name:'—'}
 const index=path.lastIndexOf('/')
 return {directory:index<0?'媒体库':path.slice(0,index)||'媒体库',name:path.slice(index+1)||'媒体库'}
}
function modelName(row:any){return row.model_config_title||row.model||'—'}
function modelHint(row:any){return [row.model_config_title,row.model].filter(Boolean).join(' · ')||'未记录模型'}
function tokens(n:number){return n>=999_950?`${(n/1e6).toFixed(2).replace(/\.?0+$/,'')}M`:n>=1000?`${(n/1000).toFixed(1).replace(/\.?0+$/,'')}K`:String(n)}
function reset(){before.value=0;pages.value=[];load()}
function search(){clearTimeout(debounce);debounce=setTimeout(reset,250)}
function next(){pages.value.push(before.value);before.value=nextCursor.value;load()}
async function copy(){try{await navigator.clipboard.writeText(JSON.stringify(selected.value,null,2));message.value='已复制任务历史详情'}catch{detailError.value='浏览器不允许复制，请手动选取文本'}}
async function restore(){if(!selected.value)return;busy.value=true;try{await api(`/operations/${selected.value.operation}/restore`,'POST',{key:key()});message.value='名称已还原';selected.value=undefined;reset()}catch(e){detailError.value=(e as Error).message}finally{busy.value=false}}
async function retry(){
 if(retrying.value||!retryable.value.length)return
 retrying.value=true;error.value=''
 try{const result=await api('/logs/retry','POST',{ids:retryable.value.map(r=>r.id),key:key()});retryRuns.value.push(...result.runs);message.value=`已提交 ${result.accepted.length} 条失败记录重试，成功后会自动移除。`;if(result.errors.length)error.value=result.errors.join('；');result.accepted.forEach((id:number)=>chosen.value.delete(id))}
 catch(e){error.value=(e as Error).message}finally{retrying.value=false}
}
let polling=false
const timer=setInterval(async()=>{
 if(polling||busy.value)return
 polling=true
 try{
  if(retryRuns.value.length){const results=await Promise.all(retryRuns.value.map(id=>api(`/runs/${id}`)));retryRuns.value=results.filter(r=>['queued','scanning','running','cancelling'].includes(r.status)).map(r=>r.id)}
  if(category.value==='failure'){await load(true);if(!rows.value.length&&pages.value.length){before.value=pages.value.pop()||0;await load(true)}}
 }catch{}finally{polling=false}
},3000)
onMounted(()=>load());onUnmounted(()=>{seq++;clearTimeout(debounce);clearInterval(timer)})
</script>
<template><section>
<div class="page-heading"><div><h1>任务历史<span class="title-dot">.</span></h1></div><div class="history-heading-actions"><button @click="category==='extraction'?extractionHistory?.openClear():openClear()"><Trash2 :size="16"/>清除记录</button><button :disabled="category==='extraction'?extractionHistory?.busy:busy" @click="category==='extraction'?extractionHistory?.reset():reset()"><RefreshCw :size="16" :class="{spin:category==='extraction'?extractionHistory?.busy:busy}"/>刷新历史</button></div></div>
<div class="log-tabs"><button :class="{active:category==='success'}" @click="tab('success')"><CheckCircle2 :size="18"/>成功记录</button><button :class="{active:category==='failure'}" @click="tab('failure')"><AlertCircle :size="18"/>失败记录</button><button :class="{active:category==='extraction'}" @click="tab('extraction')"><Images :size="18"/>抽帧记录</button></div>
<ExtractionHistory v-if="category==='extraction'" ref="extractionHistory"/>
<template v-else>
<div v-if="error" role="alert" class="notice error">{{error}}<button @click="load()">重试</button></div><div v-if="message" role="status" class="notice success">{{message}}<button @click="message=''">关闭</button></div>
<div class="panel"><div class="filterbar log-filters"><div class="search-input"><Search :size="16"/><input v-model="q" placeholder="搜索名称或任务历史内容…" aria-label="搜索任务历史" @input="search"/></div><SelectField v-model="action" @change="reset" label="操作类型" :options="[{value:'',label:'全部操作'},...Object.entries(actionNames).map(([value,label])=>({value,label}))]"/><SelectField v-model="run" label="运行批次" :options="batchOptions" @change="reset"/><DateField v-model="from" label="起始日期" :max="to" @change="reset"/><span class="subtle">至</span><DateField v-model="to" label="结束日期" :min="from" @change="reset"/><div class="filter-spacer"/><span class="subtle">共 {{total}} 项 · {{rows.length}} 项 / 当前页</span></div>
<div v-if="category==='failure'" class="batch-toolbar"><strong>已选 {{chosen.size}} 项</strong><button :disabled="busy||retrying||!retryable.length" @click="retry"><RefreshCw :size="15"/>{{retrying?'正在提交…':`批量重试（${retryable.length}）`}}</button><span class="subtle">{{retryRuns.length?'重试运行中，成功后自动移除失败记录':'自动命名失败可重试，使用原模型配置的最新设置'}}</span></div>
<div class="table-scroll"><table class="history-table" :class="category"><thead><tr><th class="history-select"><input type="checkbox" aria-label="选择本页记录" :checked="rows.length>0&&chosen.size===rows.length" @change="selectPage"/></th><th>时间</th><th>操作</th><th>模型配置</th><th>目录</th><th>旧文件名</th><th>{{category==='success'?'新文件名':'运行批次'}}</th><th v-if="category==='success'">运行批次</th><th>token数</th><th v-if="category==='failure'" class="history-reason">失败原因</th><th></th></tr></thead>
<tbody><template v-for="row in rows" :key="row.id"><tr><td class="history-select"><input type="checkbox" :aria-label="`选择记录 ${row.id}`" :checked="chosen.has(row.id)" @change="toggleRow(row.id)"/></td><td class="subtle date-cell"><time v-if="row.created" class="stacked-date-time" :datetime="row.created" :title="time(row.created)"><span>{{datePart(row.created)}}</span><span>{{clockPart(row.created)}}</span></time><span v-else>—</span></td><td><span class="action-badge" :data-action="row.action">{{actionNames[row.action]||row.action}}</span></td><td><span class="table-model" :title="modelHint(row)">{{modelName(row)}}</span></td><td><span class="log-directory" :title="pathParts(row.old_path||row.new_path).directory">{{pathParts(row.old_path||row.new_path).directory}}</span></td><td><span class="history-filename" :title="pathParts(row.old_path).name">{{pathParts(row.old_path).name}}</span></td><td><span v-if="category==='success'" class="history-filename" :title="pathParts(row.new_path).name">{{pathParts(row.new_path).name}}</span><span v-else class="subtle">{{row.run?`#${row.run}`:'—'}}</span></td><td v-if="category==='success'" class="subtle">{{row.run?`#${row.run}`:'手动操作'}}</td><td class="subtle run-tokens" :title="row.tokens?(row.token_estimated?'按文字粗估的 token':'来自模型 usage 的 token'):'没有关联的模型请求'">{{row.tokens?(row.token_estimated?'≈':'')+tokens(row.tokens):'—'}}</td><td v-if="category==='failure'" class="subtle"><span class="error-summary" :title="row.message">{{row.message}}</span></td><td><button class="detail-button icon-only-action" aria-label="详情" title="详情" @click="selected=row;detailError=''"><Info :size="16"/></button></td></tr><tr><td colspan="10"><FailureDiagnostics :id="row.id"/></td></tr></template></tbody></table>
<div v-if="busy&&!rows.length" class="empty" role="status"><ScrollText :size="35"/><h3>正在读取任务历史…</h3></div><div v-else-if="!rows.length" class="empty"><ScrollText :size="35"/><h3>{{category==='success'?'还没有成功记录':'没有失败记录'}}</h3><p>{{q||action||run||from||to?'可以调整筛选条件后再试。':category==='success'?'完成一次命名后，就能在这里查看。':'后续运行中的问题会单独记录在这里。'}}</p></div></div>
<div class="pagination"><span class="subtle">按时间从新到旧</span><SelectField v-model="limit" @change="reset" label="每页条目数" :options="[{value:50,label:'50 项 / 页'},{value:100,label:'100 项 / 页'},{value:200,label:'200 项 / 页'}]"/><button :disabled="!pages.length||busy" aria-label="上一页任务历史" @click="before=pages.pop()||0;load()"><ChevronLeft :size="16"/></button><span>{{pages.length+1}}</span><button :disabled="!nextCursor||busy" aria-label="下一页任务历史" @click="next"><ChevronRight :size="16"/></button></div></div>
</template></section>
<Modal v-if="selected" title="任务历史详情" @close="selected=undefined"><dl class="detail-list"><dt>时间</dt><dd>{{time(selected.created)}}</dd><dt>操作类型</dt><dd>{{actionNames[selected.action]||selected.action}}</dd><dt>原路径</dt><dd>{{selected.old_path||'—'}}</dd><dt>新路径</dt><dd>{{selected.new_path||'—'}}</dd><dt>运行批次</dt><dd>{{selected.run?`#${selected.run}`:'—'}}</dd><dt>模型配置</dt><dd :title="modelHint(selected)">{{modelName(selected)}}</dd><dt>详情</dt><dd>{{selected.message||'操作已成功完成'}}</dd></dl><FailureDiagnostics :key="selected.id" :id="selected.id" :processing="selected.category==='failure'"/><div v-if="detailError" role="alert" class="notice error">{{detailError}}</div><p v-if="selected.category==='success'&&selected.operation" class="help">仅最近一次尚未撤销的改名可还原；提交前将重新核验文件和名称占用。</p><div class="modal-actions"><button @click="copy"><Copy :size="15"/>复制详情</button><button v-if="selected.category==='success'&&selected.operation&&selected.action!=='restore'" :disabled="busy" class="primary" @click="restore"><Undo2 :size="15"/>还原此次命名</button></div></Modal>
<Modal v-if="clearOpen" title="清除任务历史" @close="!clearing&&(clearOpen=false)"><p>清除{{category==='success'?'成功':'失败'}}记录</p><label class="check-label"><input type="radio" name="clear-scope" :checked="!clearAll" :disabled="!chosen.size" @change="clearAll=false"/>已选 {{chosen.size}} 条</label><label class="check-label"><input type="radio" name="clear-scope" :checked="clearAll" @change="clearAll=true"/>当前分类的全部记录（包括其他页，不受筛选条件限制）</label><p class="help">清除后无法恢复。文件、还原数据和已处理标记不受影响。</p><div v-if="clearError" class="notice error" role="alert">{{clearError}}</div><div class="modal-actions"><button :disabled="clearing" @click="clearOpen=false">取消</button><button class="cancel-run" :disabled="clearing||(!clearAll&&!chosen.size)" @click="clearHistory"><Trash2 :size="16"/>{{clearing?'正在清除…':'确认清除'}}</button></div></Modal>
</template>
