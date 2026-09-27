<script setup lang="ts">
import {ref,watch} from 'vue'
const props=defineProps<{message:string;diagnostics:string}>()
const copyStatus=ref('')
watch(()=>[props.message,props.diagnostics],()=>{copyStatus.value=''})
async function copy(){
 try{await navigator.clipboard.writeText(props.message+'\n'+props.diagnostics);copyStatus.value='已复制诊断'}
 catch{copyStatus.value='浏览器不允许复制，请手动选择下方文本'}
}
</script>
<template>
 <details class="numbering-diagnostics"><summary>查看诊断详情</summary><button @click="copy">复制诊断</button><span role="status">{{copyStatus}}</span><pre>{{diagnostics}}</pre></details>
</template>
<style scoped>
.numbering-diagnostics{margin:12px 0}.numbering-diagnostics summary{cursor:pointer;margin-bottom:8px}.numbering-diagnostics span{margin-left:8px}.numbering-diagnostics pre{max-height:320px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;background:var(--surface);padding:12px;border:1px solid var(--line);border-radius:8px;font-size:12px}
</style>
