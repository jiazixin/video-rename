<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue'
import {RefreshCw,Save,Trash2,CircleHelp} from 'lucide-vue-next'
import {api,type ModelProfile} from '../../app/api'
import Modal from '../../components/Modal.vue'
import SelectField from '../../components/SelectField.vue'
type Settings={extract_workers:number;cache_gb:number;stability_seconds:number;retry_enabled:boolean;retry_count:number;fallback_model_id:number;success_cache_mode:string;success_cache_hours:number;idle_cache_hours:number}
type State={settings:Settings;cache:{path:string;used_bytes:number;reserved_bytes:number;entries:number}}
const form=ref<Settings>(),state=ref<State>(),saved=ref(''),loading=ref(true),saving=ref(false),error=ref(''),message=ref('')
const retryHelp=ref('')
const models=ref<ModelProfile[]>([]),modelError=ref(''),clearDialog=ref(false),clearing=ref(false),clearError=ref('')
const successUnit=ref(24),idleUnit=ref(24)
const units=[{value:1,label:'小时'},{value:24,label:'天'}]
const dirty=computed(()=>!!form.value&&JSON.stringify(form.value)!==saved.value)
const usage=computed(()=>Math.min(100,100*(state.value?.cache.used_bytes||0)/((state.value?.settings.cache_gb||10)*1e9)))
const gb=(n:number)=>(n/1e9).toLocaleString('zh-CN',{maximumFractionDigits:3})
const duration=(hours:number)=>hours%24===0?`${hours/24} 天`:`${hours} 小时`
const successTime=computed({get:()=>form.value?form.value.success_cache_hours/successUnit.value:3,set:(v:number)=>{if(form.value)form.value.success_cache_hours=v*successUnit.value}})
const idleTime=computed({get:()=>form.value?form.value.idle_cache_hours/idleUnit.value:3,set:(v:number)=>{if(form.value)form.value.idle_cache_hours=v*idleUnit.value}})
const idleEnabled=computed({get:()=>!!form.value?.idle_cache_hours,set:(v:boolean)=>{if(form.value)form.value.idle_cache_hours=v?72:0;idleUnit.value=24}})
const fallbackOptions=computed(()=>[{value:0,label:'不使用兜底模型'},...models.value.filter(p=>p.ready).map(p=>({value:p.id,label:`${p.title} · ${p.model}`})),...(form.value?.fallback_model_id&&!models.value.some(p=>p.ready&&p.id===form.value!.fallback_model_id)?[{value:form.value.fallback_model_id,label:'已选配置不可用，请重新选择'}]:[])])
const cacheSummary=computed(()=>{
 const s=state.value?.settings;if(!s)return ''
 const success=s.success_cache_mode==='keep'?'不因改名删除截图。':s.success_cache_mode==='delayed'?`成功改名后保留 ${duration(s.success_cache_hours)}。`:'成功改名后立即删除截图。'
 return success+(s.idle_cache_hours?`连续 ${duration(s.idle_cache_hours)}未使用的截图自动删除。`:'不按闲置时间自动删除。')
})
watch(form,()=>{message.value=''}, {deep:true})
function changeUnit(kind:'success'|'idle',unit:string|number){if(!form.value)return;const next=Number(unit);if(kind==='success'){const value=successTime.value;successUnit.value=next;successTime.value=value}else{const value=idleTime.value;idleUnit.value=next;idleTime.value=value}}
async function loadModels(){try{models.value=await api<ModelProfile[]>('/model-configs');modelError.value=''}catch(e){modelError.value=(e as Error).message}}
async function load(){loading.value=true;error.value='';try{state.value=await api<State>('/system/settings');form.value={...state.value.settings};successUnit.value=form.value.success_cache_hours%24===0?24:1;idleUnit.value=form.value.idle_cache_hours%24===0?24:1;saved.value=JSON.stringify(form.value)}catch(e){error.value=(e as Error).message}finally{loading.value=false}}
async function refresh(){error.value='';try{state.value=await api<State>('/system/settings')}catch(e){error.value=(e as Error).message}}
async function save(){if(!form.value||saving.value)return;saving.value=true;error.value='';try{form.value=await api<Settings>('/system/settings','PUT',form.value);saved.value=JSON.stringify(form.value);await refresh();message.value='设置已保存。重试规则用于新批次，缓存规则立即生效，使用中的截图始终保留。'}catch(e){error.value=(e as Error).message}finally{saving.value=false}}
function askClear(){clearError.value='';clearDialog.value=true}
async function clearCache(){if(clearing.value)return;clearing.value=true;clearError.value='';try{const result=await api<{deleted:number;freed_bytes:number;retained:number}>('/system/cache/clear','POST',{confirm:true});message.value=`已清理 ${result.deleted} 个视频的截图，释放约 ${gb(result.freed_bytes)} GB；保留 ${result.retained} 个正在使用或抽帧的视频。`;clearDialog.value=false}catch(e){clearError.value=(e as Error).message}finally{await refresh();clearing.value=false}}
onMounted(()=>{void load();void loadModels()})
</script>
<template>
<section class="processing-settings">
<div v-if="error" class="notice error" role="alert">{{error}}<button v-if="!form" @click="load">重新加载</button></div>
<p v-if="loading" role="status">正在加载设置…</p>
<form v-else-if="form&&state" @submit.prevent="save">
<fieldset :disabled="saving||clearing" class="processing-fieldset">
<section class="panel processing-card"><div><span class="eyebrow">01 · 处理</span><h2>并发抽帧</h2><p class="help">模型生成标题时，继续为后续视频准备截图。</p></div><label>同时抽帧的视频数<input v-model.number="form.extract_workers" aria-label="同时抽帧的视频数" type="number" min="1" max="32" step="1" required/><small class="help">1–32 个，默认 8 个。所有运行批次共享此上限；模型请求并发在模型配置中设置。</small></label></section>
<section class="panel processing-card retry-card"><div><span class="eyebrow">02 · 重试</span><h2>失败自动重试</h2><p class="help">复用已准备的截图重新请求模型，最后一次重试可交给兜底模型。</p><p class="help">重试规则和模型配置在新批次创建时固定。抽帧、文件操作失败与用户取消不会触发模型重试。</p></div><div class="processing-fields"><label class="processing-check"><input v-model="form.retry_enabled" type="checkbox"/>开启失败自动重试</label><fieldset :disabled="!form.retry_enabled" class="processing-fields"><div class="retry-config-row">
<div class="retry-field"><div class="retry-field-heading"><label for="retry-count">最大重试次数</label><span class="retry-help" @mouseenter="retryHelp='count'" @mouseleave="retryHelp=''"><button type="button" aria-label="最大重试次数说明" :aria-expanded="retryHelp==='count'" aria-describedby="retry-count-help" @focus="retryHelp='count'" @blur="retryHelp=''" @click="retryHelp='count'" @keydown.esc="retryHelp=''"><CircleHelp :size="15"/></button><span v-show="retryHelp==='count'" id="retry-count-help" class="retry-tooltip" role="tooltip">首次请求之外额外重试 {{form.retry_count}} 次，最多请求 {{1+Number(form.retry_count)}} 次。</span></span></div><input id="retry-count" v-model.number="form.retry_count" aria-label="最大重试次数" type="number" min="1" max="10" step="1" required/></div>
<div class="retry-field"><div class="retry-field-heading"><span>兜底模型配置</span><span class="retry-help" @mouseenter="retryHelp='fallback'" @mouseleave="retryHelp=''"><button type="button" aria-label="兜底模型配置说明" :aria-expanded="retryHelp==='fallback'" aria-describedby="retry-fallback-help" @focus="retryHelp='fallback'" @blur="retryHelp=''" @click="retryHelp='fallback'" @keydown.esc="retryHelp=''"><CircleHelp :size="15"/></button><span v-show="retryHelp==='fallback'" id="retry-fallback-help" class="retry-tooltip" role="tooltip">兜底使用该配置的模型和提示词，仅尝试一次。与主模型相同时沿用主模型；明确的配置错误会直接尝试兜底。</span></span></div><SelectField v-model="form.fallback_model_id" label="兜底模型配置" :options="fallbackOptions"/></div>
</div><div v-if="modelError" class="notice error" role="alert">{{modelError}}<button type="button" @click="loadModels">重试加载模型</button></div></fieldset></div></section>
<section class="panel processing-card"><div><span class="eyebrow">03 · 文件保护</span><h2>文件稳定性检查</h2><p class="help">扫描后等待一段时间，再核对文件是否仍在写入。</p></div><label>稳定性等待时长（秒）<input v-model.number="form.stability_seconds" aria-label="稳定性等待时长（秒）" type="number" min="0" max="3600" step="1" required/><small class="help">默认 30 秒，0 禁用等待，处理前与改名前仍核对文件变化。已开始的等待保持原时长。</small></label></section>
<section class="panel processing-card"><div><span class="eyebrow">04 · 缓存</span><h2>缓存管理</h2><p class="help">保留截图可供还原后重新命名，切换模型或提示词仍可复用。</p><p class="help">成功后清理和闲置清理分别判断，先到期的规则生效。容量不足时仍会优先清理闲置旧截图。</p></div><div class="processing-fields"><label>临时目录容量（GB）<input v-model.number="form.cache_gb" aria-label="临时目录容量（GB）" type="number" min="1" max="10000" step="1" required/><small class="help">默认 10 GB，1 GB = 1,000,000,000 字节，包含抽帧预留空间。</small></label><fieldset class="processing-options"><legend>重命名成功后</legend><label class="processing-check"><input v-model="form.success_cache_mode" type="radio" value="immediate" name="success-cache"/>立即删除（默认）</label><label class="processing-check"><input v-model="form.success_cache_mode" type="radio" value="delayed" name="success-cache"/>延时删除</label><div v-if="form.success_cache_mode==='delayed'" class="cache-duration"><label>成功后保留时长<input v-model.number="successTime" aria-label="成功后保留时长" type="number" min="1" :max="Math.floor(8760/successUnit)" step="1" required/></label><SelectField :model-value="successUnit" @update:model-value="changeUnit('success',$event)" label="成功后保留时间单位" :options="units"/></div><label class="processing-check"><input v-model="form.success_cache_mode" type="radio" value="keep" name="success-cache"/>不因改名删除</label></fieldset><fieldset class="processing-options"><legend>闲置缓存清理</legend><label class="processing-check"><input v-model="idleEnabled" type="checkbox"/>按闲置时间自动清理</label><div v-if="idleEnabled" class="cache-duration"><label>连续未使用时长<input v-model.number="idleTime" aria-label="连续未使用时长" type="number" min="1" :max="Math.floor(8760/idleUnit)" step="1" required/></label><SelectField :model-value="idleUnit" @update:model-value="changeUnit('idle',$event)" label="闲置清理时间单位" :options="units"/></div><small class="help">默认 3 天。每次复用重新计时；取消勾选后不按闲置时间删除。</small></fieldset></div></section>
</fieldset>
<section class="panel processing-cache"><header><h2>缓存状态</h2><div class="cache-actions"><button type="button" class="cache-clear" :disabled="clearing||saving" @click="askClear"><Trash2 :size="15"/>{{clearing?'正在清理…':'清空缓存'}}</button><button type="button" :disabled="clearing" @click="refresh"><RefreshCw :size="15"/>刷新状态</button></div></header><div class="cache-usage"><strong>{{gb(state.cache.used_bytes)}} <small>/ {{state.settings.cache_gb}} GB</small></strong><span>{{state.cache.entries}} 个视频 · 抽帧预留 {{gb(state.cache.reserved_bytes)}} GB</span></div><progress :value="usage" max="100" aria-label="截图缓存容量使用比例"/><dl><dt>缓存目录</dt><dd>{{state.cache.path}}</dd><dt>当前清理规则</dt><dd>{{cacheSummary}}容量不足时优先清理闲置旧截图；使用中的截图保留，新抽帧等待空间释放。</dd></dl><p class="help">目录位置由配置文件中的 tmp-storage 指定，修改后重启服务。自动清理每分钟检查一次。</p></section>
<div v-if="message" class="notice success" role="status">{{message}}</div>
<footer class="processing-footer"><p class="help">保存后缓存规则立即生效，已到期的闲置缓存会被清理。</p><button class="primary" :disabled="saving||clearing||!dirty"><Save :size="16"/>{{saving?'正在保存…':'保存系统设置'}}</button></footer>
</form>
<Modal v-if="clearDialog" title="确认清空缓存？" @close="!clearing&&(clearDialog=false)"><p>将删除当前未被任务使用的截图缓存。后续命名需要重新抽帧，不影响原视频、命名结果和运行记录。</p><p class="help">正在使用或正在抽帧的缓存会保留。</p><div v-if="clearError" class="notice error" role="alert">{{clearError}}</div><div class="modal-actions"><button :disabled="clearing" @click="clearDialog=false">取消</button><button class="cache-clear" :disabled="clearing" @click="clearCache">{{clearing?'正在清理…':'清空缓存'}}</button></div></Modal>
</section>
</template>
<style scoped>
.retry-config-row{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,200px),1fr));gap:18px 16px;max-width:496px;width:100%;align-items:start}
.retry-card{overflow:visible}
.retry-field{min-width:0;display:flex;flex-direction:column;gap:10px}
.retry-field-heading{position:relative;display:flex;align-items:center;gap:6px;font-size:13px;font-weight:500}
.retry-field-heading label{margin:0}
.retry-help{display:inline-flex;align-items:center}
.retry-help button{padding:2px;border:0;background:transparent;color:var(--ash);border-radius:4px}
.retry-help button:hover,.retry-help button:focus-visible{color:#287452;background:#edf7f0}
.retry-tooltip{position:absolute;left:0;top:100%;margin-top:6px;z-index:20;width:100%;padding:12px 14px;border:1px solid var(--line);border-radius:8px;background:white;box-shadow:0 6px 20px #182c201a;color:var(--steel);font-size:12px;font-weight:400;line-height:1.7;white-space:normal}
.retry-tooltip::before{content:'';position:absolute;bottom:100%;left:0;right:0;height:8px}
.retry-config-row input,.retry-config-row .select-field{width:100%;min-width:0;max-width:none}
.retry-config-row :deep(.select-trigger){min-height:40px}
.retry-config-row .help{margin-bottom:0}

.processing-fields{display:flex;flex-direction:column;gap:18px;min-width:0;padding:0;margin:0;border:0}.processing-fields label,.processing-options label{margin-bottom:0}.processing-card .processing-check{display:flex;flex-direction:row;align-items:center;gap:10px;font-weight:400}.processing-check input{width:16px;height:16px;min-height:16px;flex-shrink:0;accent-color:#45866c}.processing-options{display:flex;flex-direction:column;gap:14px;border:1px solid var(--line);border-radius:8px;padding:16px;min-width:0}.processing-options legend{padding:0 6px;font-weight:600;font-size:13px}.cache-duration{display:flex;gap:12px;align-items:flex-end}.cache-duration>label{min-width:0;flex:1}.cache-duration input{width:100%;box-sizing:border-box}.cache-duration>.select-field{min-width:80px}.cache-actions{display:flex;gap:10px;flex-wrap:wrap}.cache-clear{background:#b53d38;color:white;border-color:#b53d38}.cache-clear:hover:not(:disabled){background:#96302d;border-color:#96302d}.cache-clear:disabled{opacity:.55}@media(max-width:760px){.processing-cache header{align-items:flex-start;flex-direction:column}.cache-actions{width:100%}.cache-actions>button{flex:1}}
</style>
