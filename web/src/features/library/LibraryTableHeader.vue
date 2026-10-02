<script setup lang="ts">
import { ArrowDownUp } from 'lucide-vue-next'
defineProps<{multi:boolean;sort:string;sizing?:boolean}>()
const emit=defineEmits<{sort:[field:string]}>()
const columns=[
 {field:'name',label:'名称',class:'filename-cell'},
 {field:'size',label:'大小'},
 {field:'contents',label:'目录内容',class:'contents-cell',title:'仅统计该目录的直接子项；进入目录完成索引后显示'},
 {field:'status',label:'处理状态'},
 {field:'mtime',label:'文件修改时间'},
 {field:'renamed',label:'重命名时间'},
 {field:'actions',label:'操作'}
]
const sortable=(field:string)=>['name','size','mtime','renamed'].includes(field)
</script>

<template>
 <thead :aria-hidden="sizing||undefined" :inert="sizing||undefined"><tr>
  <th v-if="multi" class="selection-cell" scope="col">选择</th>
  <th v-for="column in columns" :key="column.field" :class="column.class" :title="column.title" scope="col" :aria-sort="sortable(column.field)?(sort.startsWith(column.field+'_')?(sort.endsWith('_desc')?'descending':'ascending'):'none'):undefined">
   <button v-if="sortable(column.field)" class="table-sort" :aria-label="`切换${column.label}排序`" @click="emit('sort',column.field)"><span>{{column.label}}<template v-if="sort.startsWith(column.field+'_')"> · {{sort.endsWith('_desc')?'降序':'升序'}}</template></span><ArrowDownUp :size="12"/></button>
   <template v-else>{{column.label}}</template>
  </th>
 </tr></thead>
</template>
