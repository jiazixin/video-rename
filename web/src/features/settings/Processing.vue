<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue'
import {RefreshCw,Save} from 'lucide-vue-next'
import {api} from '../../app/api'
type Settings={extract_workers:number;cache_gb:number;stability_seconds:number}
type State={settings:Settings;cache:{path:string;used_bytes:number;reserved_bytes:number;entries:number};retention_days:number}
const form=ref<Settings>(),state=ref<State>(),saved=ref(''),loading=ref(true),saving=ref(false),error=ref(''),message=ref('')
const dirty=computed(()=>!!form.value&&JSON.stringify(form.value)!==saved.value)
const usage=computed(()=>Math.min(100,100*(state.value?.cache.used_bytes||0)/((state.value?.settings.cache_gb||10)*1e9)))
const gb=(n:number)=>(n/1e9).toLocaleString('zh-CN',{maximumFractionDigits:3})
watch(form,()=>{message.value=''}, {deep:true})
async function load(){loading.value=true;error.value='';try{state.value=await api<State>('/system/settings');form.value={...state.value.settings};saved.value=JSON.stringify(form.value)}catch(e){error.value=(e as Error).message}finally{loading.value=false}}
async function refresh(){error.value='';try{state.value=await api<State>('/system/settings')}catch(e){error.value=(e as Error).message}}
async function save(){if(!form.value||saving.value)return;saving.value=true;error.value='';try{form.value=await api<Settings>('/system/settings','PUT',form.value);saved.value=JSON.stringify(form.value);await refresh();message.value='设置已保存，后续开始的批次使用新的等待时长，并发与容量限额用于后续调度。'}catch(e){error.value=(e as Error).message}finally{saving.value=false}}
onMounted(load)
</script>
<template>
<section class="processing-settings">
<div v-if="error" class="notice error" role="alert">{{error}}<button v-if="!form" @click="load">重新加载</button></div>
<p v-if="loading" role="status">正在加载设置…</p>
<form v-else-if="form&&state" @submit.prevent="save">
<fieldset :disabled="saving" class="processing-fieldset">
<section class="panel processing-card"><div><span class="eyebrow">01 · 处理</span><h2>并发抽帧</h2><p class="help">模型生成标题时，继续为后续视频准备截图。</p></div><label>同时抽帧的视频数<input v-model.number="form.extract_workers" aria-label="同时抽帧的视频数" type="number" min="1" max="32" step="1" required/><small class="help">1–32 个，默认 8 个。所有运行批次共享此上限；模型请求并发在模型配置中设置。</small></label></section>
<section class="panel processing-card"><div><span class="eyebrow">02 · 缓存</span><h2>临时目录大小</h2><p class="help">完整截图可在失败或取消后重用，改名成功后自动删除。</p></div><label>临时目录容量（GB）<input v-model.number="form.cache_gb" aria-label="临时目录容量（GB）" type="number" min="1" max="10000" step="1" required/><small class="help">默认 10 GB，1 GB = 1,000,000,000 字节。容量包含抽帧预留空间。</small></label></section>
<section class="panel processing-card"><div><span class="eyebrow">03 · 文件保护</span><h2>文件稳定性检查</h2><p class="help">扫描后等待一段时间，再核对文件是否仍在写入。</p></div><label>稳定性等待时长（秒）<input v-model.number="form.stability_seconds" aria-label="稳定性等待时长（秒）" type="number" min="0" max="3600" step="1" required/><small class="help">默认 30 秒，可设置 0–3600 秒。设为 0 禁用等待，处理前与改名前仍核对文件变化。</small><small class="help">保存后用于后续开始的批次；已经开始的等待保持原时长。</small></label></section>
</fieldset>
<section class="panel processing-cache"><header><h2>缓存状态</h2><button type="button" @click="refresh"><RefreshCw :size="15"/>刷新状态</button></header><div class="cache-usage"><strong>{{gb(state.cache.used_bytes)}} <small>/ {{state.settings.cache_gb}} GB</small></strong><span>{{state.cache.entries}} 个视频 · 抽帧预留 {{gb(state.cache.reserved_bytes)}} GB</span></div><progress :value="usage" max="100" aria-label="截图缓存容量使用比例"/><dl><dt>缓存目录</dt><dd>{{state.cache.path}}</dd><dt>自动清理</dt><dd>连续 {{state.retention_days}} 天未使用的截图自动删除。容量不足时优先清理闲置旧截图；使用中的截图保留，新抽帧等待空间释放。</dd></dl><p class="help">目录位置由配置文件中的 tmp-storage 指定，修改后重启服务。</p></section>
<div v-if="message" class="notice success" role="status">{{message}}</div>
<footer class="processing-footer"><p class="help">降低并发数会等待正在抽帧的视频完成；已有模型请求继续执行。</p><button class="primary" :disabled="saving||!dirty"><Save :size="16"/>{{saving?'正在保存…':'保存系统设置'}}</button></footer>
</form>
</section>
</template>
