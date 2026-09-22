<script setup lang="ts">
import {computed,ref} from 'vue'
import {api} from '../../app/api'
const props=defineProps<{id:number;processing?:boolean}>()
const active=ref(''),data=ref<Record<string,string>>(),error=ref(''),busy=ref(false),copied=ref(false)
const parsed=computed(()=>{try{return JSON.parse(data.value?.request||'{}')}catch{return {}}})
const context=computed(()=>parsed.value._local_context)
const actualInput=computed(()=>{
 try{const usage=JSON.parse(data.value?.response||'{}').usage
 if(typeof usage?.prompt_tokens==='number')return usage.prompt_tokens
 if(typeof usage?.input_tokens==='number')return usage.input_tokens+(usage.cache_creation_input_tokens||0)+(usage.cache_read_input_tokens||0)
 }catch{}
 return undefined
})
const k=(n:number)=>`${(n/1000).toFixed(2)}K`
async function toggle(kind:string){active.value=active.value===kind?'':kind;copied.value=false;if(!active.value||data.value||busy.value)return;busy.value=true;error.value='';try{data.value=await api(`/logs/${props.id}/diagnostics`)}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
async function copy(){const text=data.value?.[active.value]||'';try{if(navigator.clipboard&&window.isSecureContext)await navigator.clipboard.writeText(text);else{const el=document.createElement('textarea');el.value=text;el.style.cssText='position:fixed;opacity:0';document.body.append(el);el.select();try{if(!document.execCommand('copy'))throw new Error('复制失败，请手动选取文本')}finally{el.remove()}}copied.value=true}catch(e){error.value=(e as Error).message}}
</script>
<template><div class="failure-diagnostics"><div class="diagnostic-buttons"><button v-if="processing" :aria-expanded="active==='processing'" @click="toggle('processing')">抽帧 / 媒体诊断日志</button><button :aria-expanded="active==='request'" @click="toggle('request')">原始请求</button><button :aria-expanded="active==='response'" @click="toggle('response')">原始响应</button></div><section v-if="active" class="diagnostic-content"><p v-if="busy">正在读取…</p><p v-else-if="error" role="alert">{{error}}</p><template v-else-if="data?.[active]"><header><span>{{active==='processing'?'媒体处理诊断（包含失败采样点与工具报错）':active==='request'?'请求体（图片数据省略、密钥脱敏）':'响应体（密钥脱敏）'}}</span><button @click="copy">{{copied?'已复制':'复制'}}</button></header><div v-if="active==='request'" class="context-summary">
<p v-if="actualInput!==undefined"><strong>实际输入上下文：{{k(actualInput)}} Token</strong>（{{actualInput}} Token，服务返回）</p>
<template v-if="context"><p>文字粗估：约 {{k(context.text_tokens_estimate)}} Token · 图片 {{context.images}} 张 · 输出上限 {{context.max_output_tokens?k(context.max_output_tokens)+' Token':'未设置'}}</p><p>请求体：{{(context.request_bytes/1024).toFixed(1)}} KiB（含图片编码，与上下文 Token 不同）</p><p class="help">文字粗估不含图片 Token 和协议开销，不能当作总上下文；总输入未知时无法判断是否超限。不同模型的分词和图片处理规则不同，1K = 1000 Token。</p></template>
<p v-else-if="actualInput===undefined" class="help">此旧记录未保存上下文统计；图片数据已省略，无法还原总输入 Token。</p>
</div><pre tabindex="0">{{data[active]}}</pre></template><p v-else>{{active==='processing'?'未保存媒体诊断日志。旧记录无法补录，可重试后查看新记录。':active==='request'?'未记录请求体（旧记录或尚未发起模型请求）。':'未记录响应体（旧记录或服务未返回响应）。'}}</p></section></div></template>
<style scoped>
.context-summary{margin-top:10px;padding:8px 12px;background:#eef4f0;border-radius:5px}.context-summary p{margin:5px 0}.diagnostic-buttons{display:flex;gap:8px;flex-wrap:wrap}.diagnostic-buttons button{font-size:11px;padding:4px 10px}.diagnostic-buttons button[aria-expanded=true]{border-color:#619982;color:#28684e}.diagnostic-content{margin-top:10px;border:1px solid var(--line);border-radius:6px;padding:12px;background:#f8faf9}.diagnostic-content header{display:flex;align-items:center;justify-content:space-between;color:var(--steel);font-size:11px}.diagnostic-content pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:360px;overflow:auto;font:12px/1.6 monospace;margin:10px 0 0}.diagnostic-content p{font-size:12px}
</style>
