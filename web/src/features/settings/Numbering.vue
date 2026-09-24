<script setup lang="ts">
import {computed,nextTick,onMounted,ref,watch} from 'vue'
import {Save} from 'lucide-vue-next'
import {api} from '../../app/api'
type Rules={padding:number;lowercase:boolean;prefix:string;compact:boolean}
type Settings={image:Rules;audio:Rules;shared:boolean}
const form=ref<Settings>(),saved=ref(''),loading=ref(true),saving=ref(false),error=ref(''),message=ref('')
const dirty=computed(()=>!!form.value&&JSON.stringify(form.value)!==saved.value)
watch(form,()=>{message.value=''},{deep:true})
async function load(){loading.value=true;error.value='';try{const state=await api<{settings:Settings}>('/numbering/settings');form.value={image:{...state.settings.image},audio:{...state.settings.audio},shared:state.settings.shared};saved.value=JSON.stringify(form.value)}catch(e){error.value=(e as Error).message}finally{loading.value=false}}
async function save(){if(!form.value||saving.value)return;saving.value=true;error.value='';try{form.value=await api<Settings>('/numbering/settings','PUT',form.value);saved.value=JSON.stringify(form.value);await nextTick();message.value='编号规则已保存。之后的预览与编号使用新规则；已生成的批次和整批还原不受影响。'}catch(e){error.value=(e as Error).message}finally{saving.value=false}}
onMounted(load)
</script>
<template>
<section class="numbering-settings">
<div v-if="error" class="notice error" role="alert">{{error}}<button v-if="!form" @click="load">重新加载</button></div>
<p v-if="loading" role="status">正在加载编号规则…</p>
<form v-else-if="form" @submit.prevent="save">
<fieldset :disabled="saving" class="processing-fieldset numbering-fieldset">
<section class="panel processing-card numbering-card"><div><span class="eyebrow">01 · 图片编号</span><h2>图片编号规则</h2><p class="help">作用于文件目录中图片视图的“一键编号”。</p></div>
<div class="numbering-controls">
<label>编号最小位数<input v-model.number="form.image.padding" aria-label="图片编号最小位数" type="number" min="1" max="9" step="1" required/><small class="help">1–9 位，默认 1 位（不补零）。设为 3 时号码写成 001、002；号码本身更长时保持原样，已有编号也按该位数补零。</small></label>
<label>编号前缀<input v-model="form.image.prefix" aria-label="图片编号前缀" type="text" maxlength="32" placeholder="如 IMG_"/><small class="help">填写后只有“前缀 + 数字”的文件视为已编号并保留号码，其余从最大编号后续排；留空时匹配纯数字名。</small></label>
<label class="check-label">扩展名转小写<input type="checkbox" v-model="form.image.lowercase"/><small class="help">默认开启，编号时统一小写；关闭后保留原扩展名大小写。</small></label>
<label class="check-label">紧凑重排（补空号）<input type="checkbox" v-model="form.image.compact"/><small class="help">默认关闭。开启后忽略空号，从 1 开始连续编号；未参与本次编号的同类文件仍保留号码，新号自动跳过。</small></label>
<label class="check-label">与音频共用序号<input type="checkbox" v-model="form.shared"/><small class="help">默认关闭。开启后同一目录内图片和音频互相保留对方已占用的号码，新号从两者最大编号之后续排；不会改名另一类文件。</small></label>
</div></section>
<section class="panel processing-card numbering-card"><div><span class="eyebrow">02 · 音频编号</span><h2>音频编号规则</h2><p class="help">作用于文件目录中音频视图的“一键编号”；共用序号在上方图片区块设置。</p></div>
<div class="numbering-controls">
<label>编号最小位数<input v-model.number="form.audio.padding" aria-label="音频编号最小位数" type="number" min="1" max="9" step="1" required/><small class="help">1–9 位，默认 1 位（不补零）。设为 3 时号码写成 001、002；号码本身更长时保持原样，已有编号也按该位数补零。</small></label>
<label>编号前缀<input v-model="form.audio.prefix" aria-label="音频编号前缀" type="text" maxlength="32" placeholder="如 AUD_"/><small class="help">填写后只有“前缀 + 数字”的文件视为已编号并保留号码，其余从最大编号后续排；留空时匹配纯数字名。</small></label>
<label class="check-label">扩展名转小写<input type="checkbox" v-model="form.audio.lowercase"/><small class="help">默认开启，编号时统一小写；关闭后保留原扩展名大小写。</small></label>
<label class="check-label">紧凑重排（补空号）<input type="checkbox" v-model="form.audio.compact"/><small class="help">默认关闭。开启后忽略空号，从 1 开始连续编号；未参与本次编号的同类文件仍保留号码，新号自动跳过。</small></label>
</div></section>
</fieldset>
<div v-if="message" class="notice success" role="status">{{message}}</div>
<footer class="processing-footer"><p class="help">规则保存后用于之后的编号预览；预览确认时生成的名单已冻结，保存规则的后续修改不影响进行中的批次。</p><button class="primary" :disabled="saving||!dirty"><Save :size="16"/>{{saving?'正在保存…':'保存编号规则'}}</button></footer>
</form>
</section>
</template>
<style scoped>
.numbering-fieldset{align-items:start}
.numbering-card{grid-template-columns:minmax(200px,1fr) minmax(0,2.2fr);align-items:start}
.numbering-controls{display:flex;flex-direction:column;gap:20px}
.numbering-controls label{display:flex;flex-direction:column;gap:9px}
.numbering-controls input[type=text],.numbering-controls input[type=number]{max-width:240px}
.numbering-controls .check-label{flex-direction:row;align-items:flex-start;gap:10px;margin:0}
.numbering-controls .check-label input{width:16px;margin-top:3px}
.numbering-controls .check-label small{flex:1}
@media(max-width:760px){.numbering-card{grid-template-columns:1fr;gap:16px;padding:20px}}
</style>
