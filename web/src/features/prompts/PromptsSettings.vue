<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue'
import {PenLine,Plus,Save,Trash2} from 'lucide-vue-next'
import {api,time,type PromptProfile} from '../../app/api'
import Modal from '../../components/Modal.vue'
import './prompts.css'
const fixed=ref(''),savedFixed=ref(''),rows=ref<PromptProfile[]>([]),loading=ref(true),savingFixed=ref(false),error=ref(''),fixedMessage=ref('')
const dirtyFixed=computed(()=>fixed.value!==savedFixed.value)
watch(fixed,()=>{fixedMessage.value=''})
const editing=ref(false),saving=ref(false),dialogError=ref(''),editId=ref(0),editVersion=ref(0),edit=ref({title:'',body:''})
const deleteTarget=ref<PromptProfile>(),deleteError=ref(''),deleting=ref(false)
async function load(){loading.value=true;error.value='';try{const page=await api<{fixed_prompt:string;prompts:PromptProfile[]}>('/prompts');fixed.value=page.fixed_prompt;savedFixed.value=page.fixed_prompt;rows.value=page.prompts}catch(e){error.value=(e as Error).message}finally{loading.value=false}}
async function saveFixed(){if(savingFixed.value||!dirtyFixed.value)return;savingFixed.value=true;error.value='';fixedMessage.value='';try{const out=await api<{fixed_prompt:string}>('/prompts/fixed','PUT',{fixed_prompt:fixed.value});fixed.value=out.fixed_prompt;savedFixed.value=out.fixed_prompt;fixedMessage.value='固定提示词已保存。之后创建的运行会把它附加在所选提示词末尾发送。'}catch(e){error.value=(e as Error).message}finally{savingFixed.value=false}}
function open(p?:PromptProfile){editId.value=p?.id||0;editVersion.value=p?.version||0;edit.value={title:p?.title||'',body:p?.body||''};dialogError.value='';editing.value=true}
async function save(){if(saving.value)return;saving.value=true;dialogError.value='';try{await api(editId.value?`/prompts/${editId.value}`:'/prompts',editId.value?'PATCH':'POST',{...edit.value,version:editVersion.value});editing.value=false;await load()}catch(e){dialogError.value=(e as Error).message}finally{saving.value=false}}
async function remove(){if(!deleteTarget.value||deleting.value)return;deleting.value=true;deleteError.value='';try{await api(`/prompts/${deleteTarget.value.id}`,'DELETE',{version:deleteTarget.value.version});deleteTarget.value=undefined;await load()}catch(e){deleteError.value=(e as Error).message}finally{deleting.value=false}}
onMounted(load)
</script>
<template><section class="prompts-page">
<div v-if="error" class="notice error" role="alert">{{error}}<button @click="load">重试</button></div>
<p v-if="loading" role="status">正在加载提示词…</p>
<template v-else>
<section class="panel processing-card prompts-fixed-card"><div><span class="eyebrow">01 · 固定提示词</span><h2>所有请求共用的输出约束</h2><p class="help">无论模型配置选择哪条提示词，发送请求时都会把这段文字附加在所选提示词的最下方，作为统一的硬性输出约束。</p></div>
<div class="prompts-fixed-controls"><textarea v-model="fixed" aria-label="固定提示词" rows="9" maxlength="16000" required/><div v-if="fixedMessage" class="notice success" role="status">{{fixedMessage}}</div><footer class="processing-footer"><p class="help">保存后仅影响之后创建的运行；排队与运行中的批次继续使用创建时的快照。</p><button class="primary" :disabled="savingFixed||!dirtyFixed" @click="saveFixed"><Save :size="16"/>{{savingFixed?'正在保存…':'保存固定提示词'}}</button></footer></div></section>
<section class="panel prompts-library"><header class="prompts-library-head"><div><span class="eyebrow">02 · 提示词库</span><h2>可复用的命名提示词</h2><p class="help">模型配置从这里选择提示词；被引用的提示词不能删除，需先在模型配置中更换。</p></div><button class="primary" @click="open()"><Plus :size="17"/>新增提示词</button></header>
<div v-if="rows.length" class="prompt-list"><article v-for="p in rows" :key="p.id" class="panel prompt-card"><header><div><h2>{{p.title}}</h2><span class="subtle">更新于 {{time(p.updated)}}</span></div><div class="prompt-card-actions"><button :aria-label="`编辑提示词 ${p.title}`" @click="open(p)"><PenLine :size="15"/>编辑</button><button :aria-label="`删除提示词 ${p.title}`" class="model-delete" @click="deleteTarget=p;deleteError=''"><Trash2 :size="15"/>删除</button></div></header><p class="prompt-body">{{p.body}}</p></article></div>
<p v-else class="help">还没有提示词。新增一条后即可在模型配置中选择。</p></section>
</template>
</section>
<Modal v-if="editing" :title="editId?'编辑提示词':'新增提示词'" @close="!saving&&(editing=false)"><form @submit.prevent="save" class="prompt-form"><label>提示词名称<input v-model="edit.title" aria-label="提示词名称" required maxlength="80" placeholder="例如：旅行视频标题"/></label><label>提示词内容<textarea v-model="edit.body" aria-label="提示词内容" required rows="10" maxlength="16000"/></label><p class="help">请求时会自动在这条提示词末尾附加固定提示词，作为统一输出约束；模型配置仅保存对这条提示词的引用。</p><div v-if="dialogError" role="alert" class="notice error">{{dialogError}}</div><div class="modal-actions"><button type="button" :disabled="saving" @click="editing=false">取消</button><button class="primary" :disabled="saving">{{saving?'正在保存…':'保存提示词'}}</button></div></form></Modal>
<Modal v-if="deleteTarget" title="删除提示词" @close="!deleting&&(deleteTarget=undefined)"><p>确定删除“{{deleteTarget.title}}”？</p><p class="help">删除后无法恢复。被模型配置引用的提示词需先更换配置中的选择；进行中的批次继续使用创建时的快照。</p><div v-if="deleteError" role="alert" class="notice error">{{deleteError}}</div><div class="modal-actions"><button :disabled="deleting" @click="deleteTarget=undefined">取消</button><button class="cancel-run" :disabled="deleting" @click="remove"><Trash2 :size="16"/>{{deleting?'正在删除…':'确认删除'}}</button></div></Modal>
</template>
