<script setup lang="ts">
import {ref,onMounted,onUnmounted,computed,watch} from 'vue'
import {Play,Plus,Clock3,Folder,PenLine,Trash2,Info,X,RefreshCw,CalendarDays,Check,Undo2,Images,ChevronRight} from 'lucide-vue-next'
import {api,key,time,clockPart,statusNames,type Plan,type Run} from '../../app/api'
import {DirectoryPicker} from '../library'
import {ModelPicker} from '../models'
import StatusBadge from '../../components/StatusBadge.vue'
import './runs.css'
import Modal from '../../components/Modal.vue'
import SelectField from '../../components/SelectField.vue'
const modelConfigID=ref(0)
const category=ref<'manual'|'automatic'>('manual')
type RunPage={items:Run[];total:number;page:number;size:number}
const pagination=ref({manual:{page:1,size:50,total:0},automatic:{page:1,size:50,total:0}})
const currentPage=computed(()=>pagination.value[category.value])
const pageCount=computed(()=>Math.max(1,Math.ceil(currentPage.value.total/currentPage.value.size)))
const loading=ref(false)
let loadSequence=0
function changePage(page:number){void load(page)}
function changeSize(){currentPage.value.page=1;void load()}
watch(category,()=>{runs.value=[];void load()})
const props=defineProps<{ready:boolean}>()
const runs=ref<Run[]>([]),plans=ref<Plan[]>([]),error=ref(''),notice=ref(''),mode=ref(''),busy=ref(false),dialogError=ref(''),targets=ref<number[]>([]),recursive=ref(true)
const edit=ref<Plan>(),name=ref(''),kind=ref('daily'),hour=ref(2),minute=ref(30),days=ref([1]),cron=ref('0 */6 * * *'),timezone=ref('Asia/Shanghai'),enabled=ref(true),preview=ref<string[]>([])
type RestoreResult={restored:number;skipped:number;failed:number;errors:string[]}
const restoreTarget=ref<Run>(),restoring=ref(false),restoreError=ref(''),restoreResult=ref<RestoreResult>()
function askRestore(r:Run){restoreTarget.value=r;restoreError.value='';restoreResult.value=undefined}
async function restoreBatch(){if(!restoreTarget.value||restoring.value)return;restoring.value=true;restoreError.value='';try{restoreResult.value=await api<RestoreResult>(`/runs/${restoreTarget.value.id}/restore`,'POST',{});await load()}catch(e){restoreError.value=(e as Error).message}finally{restoring.value=false}}
const cancelTarget=ref<Run>(),cancelling=ref(false),cancelError=ref('')
const itemAfter=ref(0),itemPages=ref<number[]>([]),itemBusy=ref(false)
const detail=ref<Run>(),items=ref<any[]>([]),deletePlan=ref<Plan>();let timer:ReturnType<typeof setTimeout>|undefined,alive=true
const expression=computed(()=>kind.value==='hourly'?`${minute.value} * * * *`:kind.value==='daily'?`${minute.value} ${hour.value} * * *`:kind.value==='weekly'?`${minute.value} ${hour.value} * * ${days.value.join(',')}`:cron.value)
const sourceLabel=(r:Run)=>r.source==='schedule'?'定时触发':r.plan?'计划立即运行':'手动触发'
function groupDays(list:Run[]){
 const days:{key:string;label:string;runs:Run[]}[]=[]
 for(const r of list){
  const stamp=r.started||r.created
  const date=stamp?new Date(stamp):null
  const key=date?`${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`:'pending'
  const label=date?date.toLocaleDateString('zh-CN',{year:'numeric',month:'long',day:'numeric'}):'等待开始'
  const day=days.find(d=>d.key===key)
  if(day)day.runs.push(r);else days.push({key,label,runs:[r]})
 }
 return days
}
const groups=computed(()=>[{id:'automatic',title:'自动计划',description:'按设定时间自动执行，持续整理新增视频。',runs:runs.value.filter(r=>r.source==='schedule'||r.plan>0)},{id:'manual',title:'手动运行',description:'选择目录立即执行一次，运行结束后不会重复触发。',runs:runs.value.filter(r=>r.source!=='schedule'&&!r.plan)}].map(group=>({...group,days:groupDays(group.runs)})))
const active=(r:Run)=>['queued','scanning','running','cancelling'].includes(r.status)
const running=(r:Run)=>active(r)&&!!r.started&&!r.finished
const modelName=(r:Run)=>r.model_config_title||r.request_config?.model||'—'
const modelHint=(r:Run)=>[r.model_config_title,r.request_config?.model].filter(Boolean).join(' · ')||'未记录模型'
function tokens(n:number){if(n>=999_950)return `${(n/1_000_000).toFixed(2).replace(/\.?0+$/,'')}M`;if(n>=1_000)return `${(n/1_000).toFixed(1).replace(/\.?0+$/,'')}K`;return String(n)}
function tokenHint(r:Run){return r.token_estimated_records?`累计 ${r.token_count.toLocaleString()} token；${r.token_estimated_records} 条请求使用文字粗估，未计图片 token 和无法还原的重试。`:`累计 ${r.token_count.toLocaleString()} token；来自模型返回的 usage。`}
const now=ref(Date.now())
const processed=(r:Run)=>(r.done||0)+(r.skipped||0)+(r.failed||0)
const progress=(r:Run)=>r.discovered>0?Math.min(100,Math.round(processed(r)/r.discovered*100)):0
const runClock=(r:Run)=>{const stamp=r.started||r.created;return stamp?clockPart(stamp).slice(0,5):'—'}
function elapsed(r:Run){
 if(!r.started)return r.status==='queued'?'等待开始':'—'
 const start=Date.parse(r.started),end=r.finished?Date.parse(r.finished):active(r)?now.value:NaN
 if(!Number.isFinite(start)||!Number.isFinite(end))return '—'
 const seconds=Math.max(0,Math.floor((end-start)/1000))
 const days=Math.floor(seconds/86400),hours=Math.floor(seconds%86400/3600),minutes=Math.floor(seconds%3600/60)
 const parts:string[]=[]
 if(days)parts.push(`${days}d`)
 if(days||hours)parts.push(`${hours}h`)
 if(days||hours||minutes)parts.push(`${minutes}m`)
 parts.push(`${seconds%60}s`)
 return parts.join('')
}
const ETA_ALPHA=0.3
const etaState=new Map<number,{base:number;at:number;prev:number;prevAt:number;rate:number}>()
const etas=ref<Record<number,string>>({})
function etaWarmup(total:number){return total<=2?Infinity:total<=10?Math.max(2,Math.ceil(total/2)):10}
function etaFormat(seconds:number){const s=Math.max(0,Math.round(seconds));if(s<60)return `${s}s`;if(s<3600)return `${Math.floor(s/60)}m${String(s%60).padStart(2,'0')}s`;return `${Math.floor(s/3600)}h${String(Math.floor(s%3600/60)).padStart(2,'0')}m`}
function trackEta(list:Run[]){
 const live=list.filter(r=>r.status==='running'),next:Record<number,string>={}
 for(const r of live){
  const total=r.discovered||0,p=processed(r),t=Date.now()
  let s=etaState.get(r.id)
  if(!s){s={base:p,at:t,prev:p,prevAt:t,rate:0};etaState.set(r.id,s)}
  else{const items=p-s.prev,span=(t-s.prevAt)/1000;if(items>0&&span>0){const instant=items/span;s.rate=s.rate>0?ETA_ALPHA*instant+(1-ETA_ALPHA)*s.rate:instant}s.prev=p;s.prevAt=t}
  const runFor=(t-s.at)/1000,remaining=Math.max(total-p,0),warm=etaWarmup(total)
  let rate=s.rate
  if(!(rate>0)&&p-s.base>0)rate=(p-s.base)/runFor
  next[r.id]=total>0&&p-s.base>=warm&&runFor>=5&&rate>0?(remaining>0?etaFormat(remaining/rate):'<1s'):'预计时间计算中…'
 }
 for(const id of [...etaState.keys()])if(!live.some(r=>r.id===id))etaState.delete(id)
 etas.value=next
}
async function load(requestedPage?:number){const sequence=++loadSequence,selected=category.value,{size}=currentPage.value,page=requestedPage??currentPage.value.page;loading.value=true;try{const [r,p]=await Promise.all([api<RunPage>(`/runs?category=${selected}&page=${page}&size=${size}`),api<Plan[]>('/schedules')]);if(!alive||sequence!==loadSequence)return;now.value=Date.now();runs.value=r.items;pagination.value[selected]={page:r.page,size:r.size,total:r.total};plans.value=p;trackEta(r.items);error.value='';if(detail.value){const id=detail.value.id,after=itemAfter.value;const [run,files]=await Promise.all([api<Run>(`/runs/${id}`),api<any[]>(`/runs/${id}/items?after=${after}`)]);if(alive&&sequence===loadSequence&&detail.value?.id===id&&itemAfter.value===after){detail.value=run;items.value=files}}}catch(e){if(alive&&sequence===loadSequence)error.value=(e as Error).message}finally{if(alive&&sequence===loadSequence)loading.value=false}}
async function poll(){if(document.visibilityState==='visible'&&!loading.value)await load();if(alive)timer=setTimeout(poll,3000)}
function manual(task='rename'){modelConfigID.value=0;mode.value=task==='extract'?'extract':'manual';targets.value=[];recursive.value=true;dialogError.value=''}
function plan(p?:Plan){modelConfigID.value=p?.model_config_id||0;edit.value=p;name.value=p?.name||'';targets.value=p?[...p.targets]:[];recursive.value=p?.recursive??true;kind.value=p?.kind||'daily';timezone.value=p?.timezone||'Asia/Shanghai';enabled.value=p?.enabled??true;cron.value=p?.expression||'0 */6 * * *';const parts=p?.expression.split(' ');minute.value=parts?Number(parts[0])||0:30;hour.value=parts?Number(parts[1])||0:2;days.value=parts&&p?.kind==='weekly'?parts[4].split(',').map(Number):[1];preview.value=[];dialogError.value='';mode.value='plan'}
async function showPreview(){dialogError.value='';try{preview.value=await api('/schedules/preview','POST',{expression:expression.value,timezone:timezone.value})}catch(e){dialogError.value=(e as Error).message}}
async function submit(){busy.value=true;dialogError.value='';try{if(mode.value==='manual'||mode.value==='extract'){const extracting=mode.value==='extract';const r=await api('/runs','POST',{task:extracting?'extract':'rename',model_config_id:extracting?0:modelConfigID.value,targets:targets.value,recursive:recursive.value,key:key()});notice.value=`${extracting?'抽帧':'重命名'}批次 #${r.id} 已加入队列`}else{await api(edit.value?`/schedules/${edit.value.id}`:'/schedules',edit.value?'PATCH':'POST',{name:name.value,model_config_id:modelConfigID.value,targets:targets.value,recursive:recursive.value,kind:kind.value,expression:expression.value,timezone:timezone.value,enabled:enabled.value,version:edit.value?.version||0});notice.value='运行计划已保存'}mode.value='';await load()}catch(e){dialogError.value=(e as Error).message}finally{busy.value=false}}
async function toggle(p:Plan){try{await api(`/schedules/${p.id}`,'PATCH',{...p,enabled:!p.enabled});await load()}catch(e){error.value=(e as Error).message}}
async function runPlan(p:Plan){try{const r=await api(`/schedules/${p.id}/run`,'POST',{key:key()});notice.value=`运行 #${r.id} 已加入队列`;await load()}catch(e){error.value=(e as Error).message}}
async function remove(){if(!deletePlan.value)return;try{await api(`/schedules/${deletePlan.value.id}`,'DELETE',{});deletePlan.value=undefined;await load()}catch(e){error.value=(e as Error).message}}
function askCancel(r:Run){cancelTarget.value=r;cancelError.value='';detail.value=undefined}
async function confirmCancel(){if(!cancelTarget.value||cancelling.value)return;cancelling.value=true;try{await api(`/runs/${cancelTarget.value.id}/cancel`,'POST',{});cancelTarget.value=undefined;notice.value='已提交取消请求';await load()}catch(e){cancelError.value=(e as Error).message}finally{cancelling.value=false}}
async function itemPage(after:number){if(!detail.value)return;const id=detail.value.id;itemAfter.value=after;itemBusy.value=true;try{const files=await api<any[]>(`/runs/${id}/items?after=${after}`);if(detail.value?.id===id&&itemAfter.value===after)items.value=files}catch(e){error.value=(e as Error).message}finally{itemBusy.value=false}}
async function inspect(r:Run){detail.value=r;items.value=[];itemAfter.value=0;itemPages.value=[];try{const run=await api<Run>(`/runs/${r.id}`);if(detail.value?.id===r.id){detail.value=run;await itemPage(0)}}catch(e){error.value=(e as Error).message}}
let ticker:ReturnType<typeof setInterval>|undefined
onMounted(()=>{poll();ticker=setInterval(()=>{if(runs.value.some(r=>running(r)))now.value=Date.now()},1000)})
onUnmounted(()=>{alive=false;clearTimeout(timer);clearInterval(ticker)})
</script>
<template><section>
<div class="page-heading"><div><h1>运行计划<span class="title-dot">.</span></h1></div></div><div v-if="error" role="alert" class="notice error">{{error}}<button @click="error='';load()">重试</button></div><div v-if="notice" role="status" class="notice success">{{notice}}<button @click="notice=''">关闭</button></div>
<div class="log-tabs run-tabs" aria-label="运行分类"><button :class="{active:category==='manual'}" :aria-pressed="category==='manual'" @click="category='manual'"><Play :size="18"/>手动计划和运行记录</button><button :class="{active:category==='automatic'}" :aria-pressed="category==='automatic'" @click="category='automatic'"><CalendarDays :size="18"/>自动计划和运行记录</button></div>
<section v-for="group in groups.filter(g=>g.id===category)" :key="group.id" class="run-section" :aria-label="group.title">
<div class="section-heading run-section-heading"><div><h2>{{group.title}} <span v-if="group.id==='automatic'" class="count">{{plans.length}}</span></h2><p>{{group.description}}</p></div><button v-if="group.id==='automatic'" class="primary" @click="plan()"><Plus :size="17"/>新建计划</button><div v-else class="heading-actions"><button class="primary" :disabled="!ready" @click="manual()"><PenLine :size="17"/>重命名</button><button @click="manual('extract')"><Images :size="17"/>抽帧</button></div></div>
<template v-if="group.id==='automatic'"><div v-if="!plans.length" class="panel empty plans-empty"><CalendarDays :size="31"/><h3>暂无自动计划</h3><p>支持按小时、日、周或 Cron 执行。</p></div><div v-else class="plan-grid"><article v-for="p in plans" :key="p.id" class="panel plan-card"><header><span class="plan-icon"><Clock3 :size="19"/></span><button class="switch" :class="{on:p.enabled}" role="switch" :aria-checked="p.enabled" :aria-label="`${p.enabled?'暂停':'启用'} ${p.name}`" @click="toggle(p)"><span/></button></header><h3>{{p.name}}</h3><p class="plan-expression">{{p.kind==='hourly'?'按小时':p.kind==='daily'?'按日':p.kind==='weekly'?'按周':'Cron'}} <code>{{p.expression}}</code></p><div class="plan-meta"><span><Folder :size="14"/>{{p.targets.length}} 个目录 · {{p.recursive?'含子目录':'仅当前层'}}</span><span class="plan-paths" :title="p.target_names?.join(' · ')">{{p.target_names?.join(' · ')}}</span><span>{{p.timezone}}</span></div><div class="plan-next"><span>下次运行</span><strong>{{p.enabled?time(p.next):'已暂停'}}</strong></div><p v-if="p.last" class="help">上次：{{p.last}}</p><footer><button :disabled="!ready" @click="runPlan(p)"><Play :size="14"/>立即运行</button><button class="icon-btn" :aria-label="`编辑 ${p.name}`" @click="plan(p)"><PenLine :size="15"/></button><button class="icon-btn danger-button" :aria-label="`删除 ${p.name}`" @click="deletePlan=p"><Trash2 :size="15"/></button></footer></article></div></template>
<div class="section-heading recent-heading"><h3>运行记录</h3><button class="text-button" :disabled="loading" @click="load()"><RefreshCw :size="14"/>刷新</button></div>
<div class="panel run-timeline"><template v-for="day in group.days" :key="day.key"><h4 class="run-day-label"><CalendarDays :size="13" aria-hidden="true"/>{{day.label}}</h4>
<article v-for="(r,runIndex) in day.runs" :key="r.id" class="run-entry" :class="{'is-active':active(r),'is-running':running(r),'is-full':!active(r)&&progress(r)>=100,'is-tail':runIndex===day.runs.length-1}"><time class="run-time" :datetime="r.started||r.created" :title="time(r.started||r.created||'')">{{runClock(r)}}</time><span class="run-node" aria-hidden="true"><i class="run-node-dot"/></span><div class="run-body">
<div class="run-head"><strong class="run-id">#{{r.id}}</strong><span class="badge">{{r.task==='extract'?'抽帧':'重命名'}}</span><StatusBadge :status="r.status">{{statusNames[r.status]||r.status}}</StatusBadge><span class="run-duration" title="从实际开始到结束，不含排队等待；进行中的批次显示已耗时与按近期处理速度估算的剩余时间"><span class="run-duration-value"><template v-if="etas[r.id]">已运行 </template>{{elapsed(r)}}<span v-if="etas[r.id]" class="run-eta-sep" aria-hidden="true">·</span><span v-if="etas[r.id]" class="run-eta">剩余约 {{etas[r.id]}}</span></span></span></div>
<p class="run-meta"><span>{{sourceLabel(r)}}</span><span class="run-meta-sep">·</span><span class="table-model" :title="modelHint(r)">{{modelName(r)}}</span><span class="run-meta-sep">·</span><span class="run-tokens" :title="r.token_records?tokenHint(r):'暂无模型请求记录'">{{r.token_records?(r.token_estimated_records?'≈':'')+tokens(r.token_count):'—'}}</span></p>
<div class="run-progress" role="progressbar" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="progress(r)" :aria-label="`运行 #${r.id} 进度：已处理 ${processed(r)} / ${r.discovered}`"><span class="run-progress-bar"><span class="run-progress-fill" :style="{width:progress(r)+'%'}"/></span><span class="run-progress-pct">{{progress(r)}}%</span></div>
<div class="run-foot"><div class="run-stat-row"><span class="run-stat" data-stat="total">总数 {{r.discovered}}</span><span class="run-stat" data-stat="done">成功 {{r.done}}</span><span class="run-stat" data-stat="skipped">跳过 {{r.skipped}}</span><span class="run-stat" data-stat="failed">失败 {{r.failed}}</span></div><div class="row-actions run-actions"><button v-if="r.task!=='extract'" class="restore-run icon-only-action" aria-label="还原批次" :disabled="active(r)||!r.done" :title="active(r)?'请先取消并等待运行结束':'还原本批次成功改名的文件'" @click="askRestore(r)"><Undo2 :size="14"/></button><button :disabled="!active(r)||r.status==='cancelling'" class="cancel-run icon-only-action" aria-label="取消" :title="r.status==='cancelling'?'取消中':'取消运行'" @click="askCancel(r)"><X :size="16"/></button><button class="detail-button icon-only-action" aria-label="详情" title="详情" @click="inspect(r)"><Info :size="16"/></button></div></div>
</div></article></template><div v-if="!group.runs.length" class="empty compact">{{loading?'正在加载运行记录…':'暂无运行记录'}}</div></div>
<div class="pagination" aria-label="运行记录分页"><span class="subtle" role="status">共 {{currentPage.total}} 个批次 · 第 {{currentPage.page}} / {{pageCount}} 页 · 当前页 {{runs.length}} 条</span><SelectField v-model="currentPage.size" label="每页批次数" :options="[{value:50,label:'50 条 / 页'},{value:100,label:'100 条 / 页'},{value:200,label:'200 条 / 页'}]" @change="changeSize"/><button :disabled="loading||currentPage.page<=1" @click="changePage(currentPage.page-1)">上一页</button><button :disabled="loading||currentPage.page>=pageCount" @click="changePage(currentPage.page+1)">下一页</button></div></section></section>
<Modal v-if="mode" :title="mode==='extract'?'手动抽帧':mode==='manual'?'手动运行':edit?'编辑运行计划':'新建运行计划'" wide @close="!busy&&(mode='')"><form @submit.prevent="submit">
<template v-if="mode==='plan'"><label>计划名称<input v-model="name" required maxlength="80" placeholder="例如：每天整理旅行视频"/></label><div class="form-grid"><label>执行频率<SelectField v-model="kind" @change="preview=[]" label="执行频率" :options="[{value:'hourly',label:'按小时'},{value:'daily',label:'按日'},{value:'weekly',label:'按周'},{value:'cron',label:'Cron 表达式'}]"/></label><label>时区<input v-model="timezone" required placeholder="Asia/Shanghai"/></label></div><div v-if="kind!=='cron'" class="form-grid"><label v-if="kind!=='hourly'">小时<input type="number" v-model="hour" min="0" max="23" required/></label><label>分钟<input type="number" v-model="minute" min="0" max="59" required/></label></div><div v-if="kind==='weekly'" class="weekday-picker"><label v-for="(d,i) in ['日','一','二','三','四','五','六']" :key="i"><input type="checkbox" :value="i" v-model="days"/>周{{d}}</label></div><label v-if="kind==='cron'">Cron 表达式<input v-model="cron" aria-label="Cron 表达式" required placeholder="0 */6 * * *" class="mono"/><small class="help">五段：分 时 日 月 周。日与周同时限制时，任一匹配即触发。</small></label><div class="preview-rule"><code>{{expression}}</code><button type="button" @click="showPreview">预览执行时间<ChevronRight :size="14"/></button></div><ol v-if="preview.length" class="schedule-preview"><li v-for="p in preview" :key="p">{{p}}</li></ol></template>
<ModelPicker v-if="mode!=='extract'" v-model="modelConfigID"/><label class="field-heading">选择目标目录</label><DirectoryPicker v-model="targets"/><label class="check-label"><input type="checkbox" v-model="recursive"/>包含所选目录的子目录</label><label v-if="mode==='plan'" class="check-label"><input type="checkbox" v-model="enabled"/>保存后启用计划</label>
<p class="help">{{mode==='extract'?'为所选目录中的视频预先生成截图，包括已处理视频；有效缓存直接复用，供之后重命名使用。抽帧结果在任务历史的“抽帧记录”查看。':'已命名或手动标记已处理的视频会跳过；还原后的视频可再次处理。新文件会先进行稳定性检查。'}}</p><div v-if="dialogError" class="notice error" role="alert">{{dialogError}}</div><div class="modal-actions"><button type="button" :disabled="busy" @click="mode=''">取消</button><button class="primary" :disabled="busy||!targets.length||mode!=='extract'&&!modelConfigID||mode==='manual'&&!props.ready">{{busy?'正在保存…':mode==='extract'?'抽帧':mode==='manual'?'重命名':'保存计划'}}</button></div></form></Modal>
<Modal v-if="restoreTarget" :title="`还原批次 #${restoreTarget.id}`" @close="!restoring&&(restoreTarget=undefined)"><p>将本批次成功改名的文件恢复为本批次处理前的名称。</p><p class="help">已还原的文件会跳过；后续又改名、内容变化或原名被占用的文件不会强制覆盖。还原成功后可重新自动命名，已启用的定时计划也可能再次处理。</p><div v-if="restoreError" class="notice error" role="alert">{{restoreError}}</div><div v-if="restoreResult" role="status" class="notice" :class="restoreResult.failed?'error':'success'">已还原 {{restoreResult.restored}} 项 · 已还原跳过 {{restoreResult.skipped}} 项 · 失败 {{restoreResult.failed}} 项<ul v-if="restoreResult.errors.length"><li v-for="message in restoreResult.errors" :key="message">{{message}}</li></ul><p v-if="restoreResult.failed>100">仅显示前 100 条失败详情。</p></div><div class="modal-actions"><button :disabled="restoring" @click="restoreTarget=undefined">{{restoreResult?'关闭':'暂不还原'}}</button><button v-if="!restoreResult||restoreResult.failed" class="primary" :disabled="restoring" @click="restoreBatch">{{restoring?'正在还原…':restoreResult?'重试失败项':'确认还原批次'}}</button></div></Modal>
<Modal v-if="cancelTarget" title="取消运行" @close="!cancelling&&(cancelTarget=undefined)"><p>确定取消运行 #{{cancelTarget.id}}？</p><p class="help">未完成的任务将停止，已完成的改名不会撤销。定时计划仍保持原设置。</p><div v-if="cancelError" class="notice error" role="alert">{{cancelError}}</div><div class="modal-actions"><button :disabled="cancelling" @click="cancelTarget=undefined">继续运行</button><button class="cancel-run" :disabled="cancelling" @click="confirmCancel"><X :size="16"/>{{cancelling?'正在取消…':'确认取消'}}</button></div></Modal>
<Modal v-if="deletePlan" title="删除运行计划" @close="deletePlan=undefined"><p>删除“{{deletePlan.name}}”后，将不再自动触发。已有运行和历史记录会保留。</p><div class="modal-actions"><button @click="deletePlan=undefined">取消</button><button class="danger-button" @click="remove"><Trash2 :size="16"/>删除计划</button></div></Modal>
<Modal v-if="detail" :title="`运行 #${detail.id}`" wide @close="detail=undefined"><div class="run-summary"><div><span>已完成</span><strong>{{detail.done}}</strong></div><div><span>已跳过</span><strong>{{detail.skipped}}</strong></div><div><span>失败</span><strong>{{detail.failed}}</strong></div></div><details v-if="detail.prompt_body" class="prompt-preview"><summary>提示词：{{detail.model_config_title}}</summary><p>{{detail.prompt_body}}</p></details><div class="run-targets"><h3>目标目录与文件</h3><ul><li v-for="(target,i) in detail.targets" :key="target">{{detail.target_names?.[i]||`条目 #${target}`}}</li></ul><p class="help">{{detail.recursive?'包含子目录':'仅所选目标的当前层'}}</p></div><p>{{statusNames[detail.status]}} · 提交时间：{{time(detail.created)}}</p><p class="help">开始时间：{{detail.started?time(detail.started):'—'}} · 结束时间：{{detail.finished?time(detail.finished):'—'}}</p><p class="help">总耗时：{{elapsed(detail)}}（不含排队等待）</p><div v-if="detail.error" class="notice error">{{detail.error}}</div><div v-for="item in items" :key="item.id" class="run-item"><div class="run-file-paths"><strong>{{item.original_path||item.current_path||`文件 #${item.entry}`}}</strong><small v-if="item.current_path&&item.current_path!==item.original_path">当前路径：{{item.current_path}}</small></div><StatusBadge :status="item.status">{{statusNames[item.status]||item.status}}</StatusBadge><p v-if="item.message">{{item.message}}</p></div><p v-if="!items.length" class="help">{{active(detail)?'正在扫描，发现视频后会显示在这里。':'当前页没有视频任务。'}}</p><div class="pagination"><span class="subtle">第 {{itemPages.length+1}} 页 · 每页最多 100 项</span><button :disabled="!itemPages.length||itemBusy" @click="itemPage(itemPages.pop()||0)">上页文件</button><button :disabled="items.length<100||itemBusy" @click="itemPages.push(itemAfter);itemPage(items[items.length-1].id)">下页文件</button></div><div class="modal-actions"><button v-if="active(detail)" class="cancel-run" @click="askCancel(detail)"><X :size="16"/>取消运行</button><button class="primary" @click="detail=undefined">关闭</button></div></Modal></template>
