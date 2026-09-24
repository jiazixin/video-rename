<script setup lang="ts">
import {computed,onUnmounted,ref,watch} from 'vue'
import {RefreshCw,RotateCcw,ChevronRight} from 'lucide-vue-next'
import {api,time,statusNames,type UsageReport} from '../../app/api'
import SelectField from '../../components/SelectField.vue'
import DateField from '../../components/DateField.vue'
import StatusBadge from '../../components/StatusBadge.vue'
import UsageTrend from './UsageTrend.vue'
import './usage.css'
type Preset='1h'|'3h'|'6h'|'12h'|'24h'|'today'|'7d'|'30d'|'all'|'custom'
const preset=ref<Preset>('24h'),fromDate=ref(''),fromTime=ref(''),toDate=ref(''),toTime=ref('')
const model=ref(''),profile=ref(0),source=ref(''),run=ref(0),sort=ref<'tokens'|'recent'>('tokens'),page=ref(1)
const report=ref<UsageReport>(),error=ref(''),loading=ref(false)
let seq=0,controller:AbortController|undefined
const timezone=Intl.DateTimeFormat().resolvedOptions().timeZone
const presets:{value:Preset;label:string}[]=[{value:'1h',label:'最近 1 小时'},{value:'3h',label:'最近 3 小时'},{value:'6h',label:'最近 6 小时'},{value:'12h',label:'最近 12 小时'},{value:'24h',label:'最近 24 小时'},{value:'today',label:'今天'},{value:'7d',label:'最近 7 天'},{value:'30d',label:'最近 30 天'},{value:'all',label:'全部'},{value:'custom',label:'自定义'}]
const intervalNames:Record<string,string>={'15m':'15 分钟','hour':'1 小时','day':'1 天','month':'1 个月','year':'1 年'}
function tokens(n:number){return n>=999_950?`${(n/1e6).toFixed(2).replace(/\.?0+$/,'')}M`:n>=1000?`${(n/1000).toFixed(1).replace(/\.?0+$/,'')}K`:String(n)}
function rangeTimes():{from?:string;to:string}|string{
 const now=new Date()
 if(preset.value==='custom'){
  if(!fromDate.value||!toDate.value)return '请选择自定义区间的起止日期'
  const from=new Date(`${fromDate.value}T${fromTime.value||'00:00'}`),to=new Date(`${toDate.value}T${toTime.value||'23:59'}`)
  if(Number.isNaN(from.getTime())||Number.isNaN(to.getTime()))return '自定义时间无效'
  if(from>=to)return '结束时间需晚于起始时间'
  return {from:from.toISOString(),to:to.toISOString()}
 }
 if(preset.value==='all')return {to:now.toISOString()}
 if(preset.value==='today'){const from=new Date(now);from.setHours(0,0,0,0);return {from:from.toISOString(),to:now.toISOString()}}
 const hours:{[k in Preset]?:number}={'1h':1,'3h':3,'6h':6,'12h':12,'24h':24}
 if(hours[preset.value])return {from:new Date(now.getTime()-(hours[preset.value]||0)*3600_000).toISOString(),to:now.toISOString()}
 const from=new Date(now);from.setDate(from.getDate()-(preset.value==='7d'?7:30));from.setHours(0,0,0,0)
 return {from:from.toISOString(),to:now.toISOString()}
}
async function load(){
 const range=rangeTimes()
 if(typeof range==='string'){error.value=range;return}
 const params=new URLSearchParams({to:range.to,timezone})
 if(range.from)params.set('from',range.from)
 if(model.value)params.set('model',model.value)
 if(profile.value)params.set('profile',String(profile.value))
 if(source.value)params.set('source',source.value)
 if(run.value)params.set('run',String(run.value))
 params.set('sort',sort.value)
 params.set('page',String(page.value))
 controller?.abort()
 controller=new AbortController()
 const mine=++seq
 loading.value=true
 try{
  const data=await api<UsageReport>(`/usage/analytics?${params.toString()}`,'GET',undefined,controller.signal)
  if(mine===seq){report.value=data;error.value=''}
 }catch(e){
  if(mine===seq&&(e as Error).name!=='AbortError')error.value=(e as Error).message
 }finally{
  if(mine===seq)loading.value=false
 }
}
function refresh(){page.value=1;load()}
function reset(){preset.value='24h';fromDate.value=fromTime.value=toDate.value=toTime.value='';model.value='';profile.value=0;source.value='';run.value=0;sort.value='tokens';page.value=1;load()}
function focusBatch(id:number){run.value=id;page.value=1}
watch([preset,model,profile,source,run,sort],()=>{page.value=1;load()})
watch(page,()=>load())
onUnmounted(()=>controller?.abort())
load()
const totals=computed(()=>report.value?.totals)
const average=computed(()=>totals.value&&totals.value.records?totals.value.tokens/totals.value.records:0)
const rangeText=computed(()=>report.value?`${time(report.value.range.from)} 至 ${time(report.value.range.to)}`:'')
const modelOptions=computed(()=>[{value:'',label:'全部模型'},...(report.value?.options.models.map(m=>({value:m.key,label:m.model||'未记录模型'}))||[])])
const profileOptions=computed(()=>[{value:0,label:'全部配置'},...(report.value?.options.profiles.map(p=>({value:p.id,label:p.title||`配置 #${p.id}`}))||[])])
const visibleModels=computed(()=>report.value?report.value.models.slice(0,8):[])
const restModels=computed(()=>report.value?report.value.models.slice(8):[])
const totalPages=computed(()=>report.value?Math.max(1,Math.ceil(report.value.batches.total/report.value.batches.size)):1)
const hasData=computed(()=>!!totals.value&&totals.value.records>0)
</script>
<template>
 <div>
 <div v-if="error" role="alert" class="notice error">{{error}}<button @click="load">重试</button></div>
 <div class="usage-filters">
  <div class="filterbar log-filters">
   <SelectField v-model="preset" label="时间范围" :options="presets"/>
   <SelectField v-model="model" label="模型" :options="modelOptions"/>
   <SelectField v-model="profile" label="模型配置" :options="profileOptions"/>
   <SelectField v-model="source" label="运行来源" :options="[{value:'',label:'全部来源'},{value:'manual',label:'手动运行'},{value:'scheduled',label:'自动计划'}]"/>
   <template v-if="preset==='custom'">
    <DateField v-model="fromDate" label="起始日期"/>
    <input v-model="fromTime" type="time" aria-label="起始时刻"/>
    <DateField v-model="toDate" label="结束日期"/>
    <input v-model="toTime" type="time" aria-label="结束时刻"/>
    <button class="primary" @click="refresh"><ChevronRight :size="14"/>查询</button>
   </template>
   <span v-if="run" class="usage-run-chip">批次 #{{run}}<button aria-label="清除批次筛选" @click="run=0">×</button></span>
   <span class="filter-spacer"/>
   <button @click="refresh"><RefreshCw :size="14" :class="{spin:loading}"/>刷新</button>
   <button class="text-button" @click="reset"><RotateCcw :size="14"/>重置</button>
  </div>
 </div>
 <template v-if="report">
  <div class="usage-kpis">
   <div class="usage-kpi"><span>累计 token</span><strong>{{tokens(totals?.tokens||0)}}</strong><small>{{(totals?.tokens||0).toLocaleString()}}</small></div>
   <div class="usage-kpi"><span>已记录请求</span><strong>{{(totals?.records||0).toLocaleString()}}</strong><small v-if="totals?.estimated_records">含粗估 {{totals.estimated_records.toLocaleString()}} 条</small><small v-else>全部来自模型 usage</small></div>
   <div class="usage-kpi"><span>涉及批次</span><strong>{{(totals?.batches||0).toLocaleString()}}</strong><small>区间内有记录的批次</small></div>
   <div class="usage-kpi"><span>平均每请求</span><strong>{{tokens(Math.round(average))}}</strong><small>{{Math.round(average).toLocaleString()}} token / 条</small></div>
  </div>
  <p class="help usage-notes">
   {{rangeText}} · 按{{intervalNames[report.range.interval]||report.range.interval}}分桶（{{report.range.timezone}}）。
   <template v-if="totals?.legacy_records">其中 {{totals.legacy_records.toLocaleString()}} 条为升级前的批次汇总，按批次时间归属，无法精确到请求时刻。</template>
   文字粗估不含图片 token；未记录的内部重试和模型测试不计入。
  </p>
  <div class="usage-stack">
  <div class="panel">
   <div class="panel-toolbar"><h2>消耗趋势</h2><span class="subtle">{{hasData?'':'暂无数据'}}</span></div>
   <div class="usage-trend-wrap" :class="{refreshing:loading}"><UsageTrend :buckets="report.trend" :interval="report.range.interval"/></div>
   <details class="usage-table-toggle">
    <summary>数据表</summary>
    <div class="table-scroll"><table class="usage-trend-table">
     <thead><tr><th>时间段</th><th>token</th><th>请求</th><th>粗估</th><th>历史汇总</th></tr></thead>
     <tbody><tr v-for="b in report.trend" :key="b.from"><td class="date-cell">{{time(b.from)}} – {{time(b.to)}}</td><td>{{b.tokens.toLocaleString()}}</td><td>{{b.records.toLocaleString()}}</td><td>{{b.estimated_records||'—'}}</td><td>{{b.legacy_records||'—'}}</td></tr></tbody>
    </table></div>
   </details>
  </div>
  <div class="panel">
   <div class="panel-toolbar"><h2>模型用量对比</h2><span class="subtle" v-if="report.sources.length">{{report.sources.map(s=>`${s.key==='manual'?'手动':'自动计划'} ${tokens(s.tokens)} token（${s.batches} 批）`).join(' · ')}}</span></div>
   <div v-if="visibleModels.length" class="usage-models">
    <div v-for="m in visibleModels" :key="m.key" class="usage-model">
     <div class="usage-model-head"><strong :title="[m.provider,m.protocol].filter(Boolean).join(' · ')">{{m.model||'未记录模型'}}</strong><span>{{tokens(m.tokens)}} token · {{m.records.toLocaleString()}} 条 · 平均 {{tokens(Math.round(m.tokens/Math.max(m.records,1)))}}</span></div>
     <div class="usage-bar-track"><div class="usage-bar" :style="{width:`${totals?.tokens?Math.max(1.5,m.tokens/totals.tokens*100):0}%`}"/></div>
     <small class="subtle">占区间总量 {{totals?.tokens?(m.tokens/totals.tokens*100).toFixed(1):'0.0'}}%<template v-if="m.provider"> · {{m.provider}} / {{m.protocol}}</template></small>
    </div>
    <p v-if="restModels.length" class="help">其余 {{restModels.length}} 个模型合计 {{tokens(restModels.reduce((sum,m)=>sum+m.tokens,0))}} token。</p>
   </div>
   <div v-else class="empty compact">区间内没有模型用量记录</div>
  </div>
  <div class="panel">
   <div class="panel-toolbar"><h2>高消耗批次</h2><span class="subtle">按区间内 token 排序</span></div>
   <div v-if="report.top_batches.length" class="table-scroll"><table>
    <thead><tr><th>批次</th><th>模型</th><th>配置</th><th>来源</th><th>区间 token</th><th>占比</th><th>累计 token</th><th></th></tr></thead>
    <tbody><tr v-for="b in report.top_batches" :key="b.id">
     <td><strong>#{{b.id}}</strong></td>
     <td><span class="table-model" :title="[b.provider,b.protocol,b.model].filter(Boolean).join(' · ')">{{b.model||'未记录模型'}}</span></td>
     <td class="subtle">{{b.profile_title||`配置 #${b.profile_id}`}}</td>
     <td class="subtle">{{b.source==='manual'?'手动':'自动计划'}}</td>
     <td class="run-tokens">{{tokens(b.tokens)}}</td>
     <td class="subtle">{{totals?.tokens?(b.tokens/totals.tokens*100).toFixed(1):'0.0'}}%</td>
     <td class="subtle run-tokens" :title="`批次累计 ${b.lifetime_tokens.toLocaleString()} token / ${b.lifetime_records} 条`">{{tokens(b.lifetime_tokens)}}</td>
     <td><button class="text-button" @click="focusBatch(b.id)">仅看此批次</button></td>
    </tr></tbody>
   </table></div>
   <div v-else class="empty compact">区间内没有批次用量记录</div>
  </div>
  <div class="panel">
   <div class="panel-toolbar">
    <h2>批次明细</h2>
    <div class="usage-detail-tools">
     <button class="table-sort" :class="{active:sort!=='tokens'}" @click="sort=sort==='tokens'?'recent':'tokens'">{{sort==='tokens'?'按用量排序':'按时间排序'}}</button>
     <span class="subtle">共 {{report.batches.total}} 个批次 · 第 {{report.batches.page}} / {{totalPages}} 页</span>
     <button :disabled="page<=1||loading" @click="page--">上一页</button>
     <button :disabled="page>=totalPages||loading" @click="page++">下一页</button>
    </div>
   </div>
   <div v-if="report.batches.items.length" class="table-scroll"><table>
    <thead><tr><th>批次</th><th>模型</th><th>来源</th><th>状态</th><th>请求</th><th>区间 token</th><th>累计 token</th><th>最近记录</th></tr></thead>
    <tbody><tr v-for="b in report.batches.items" :key="b.id">
     <td><strong>#{{b.id}}</strong><small class="run-source">{{b.profile_title||`配置 #${b.profile_id}`}}</small></td>
     <td><span class="table-model" :title="[b.provider,b.protocol,b.model].filter(Boolean).join(' · ')">{{b.model||'未记录模型'}}</span></td>
     <td class="subtle">{{b.source==='manual'?'手动':'自动计划'}}</td>
     <td><StatusBadge :status="b.status">{{statusNames[b.status]||b.status}}</StatusBadge></td>
     <td>{{b.records.toLocaleString()}}<template v-if="b.legacy_records"> <small class="subtle">含汇总 {{b.legacy_records}}</small></template></td>
     <td class="run-tokens" :title="b.tokens.toLocaleString()">{{tokens(b.tokens)}}</td>
     <td class="subtle run-tokens" :title="`批次累计 ${b.lifetime_tokens.toLocaleString()} token / ${b.lifetime_records} 条`">{{tokens(b.lifetime_tokens)}}</td>
     <td class="subtle date-cell"><time :datetime="b.last_recorded_at" :title="time(b.last_recorded_at)">{{time(b.last_recorded_at)}}</time></td>
    </tr></tbody>
   </table></div>
   <div v-else class="empty compact">当前筛选没有批次用量记录</div>
  </div>
  </div>
 </template>
 <div v-else-if="loading" class="panel empty"><RefreshCw :size="22" class="spin"/><h3>正在统计用量…</h3></div>
 </div>
</template>
