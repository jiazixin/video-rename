<script setup lang="ts">
import {computed,onMounted,ref} from 'vue'
import {api,type ModelProfile} from '../../app/api'
import SelectField from '../../components/SelectField.vue'
const props=defineProps<{modelValue:number}>()
const emit=defineEmits<{'update:modelValue':[number]}>()
const rows=ref<ModelProfile[]>([]),error=ref(''),loading=ref(true)
const selected=computed(()=>rows.value.find(p=>p.id===props.modelValue))
async function load(){loading.value=true;try{rows.value=(await api<ModelProfile[]>('/model-configs')).filter(p=>p.ready);error.value='';if(!props.modelValue&&rows.value.length)emit('update:modelValue',(rows.value.find(p=>p.is_default)||rows.value[0]).id)}catch(e){error.value=(e as Error).message}finally{loading.value=false}}
onMounted(load)
</script>
<template><div class="prompt-picker"><label>模型配置<SelectField :model-value="modelValue" :options="rows.map(p=>({value:p.id,label:p.title}))" label="模型配置" @update:model-value="emit('update:modelValue',Number($event))"/></label><div v-if="error" role="alert" class="notice error">{{error}}<button type="button" @click="load">重试</button></div><p v-else-if="loading" class="help">正在读取模型配置…</p><p v-else-if="!selected" class="help">请先在“模型配置”页面完善配置，然后在这里选择。</p><details v-else class="prompt-preview"><summary>查看命名提示词</summary><p>{{selected.body}}</p></details></div></template>
