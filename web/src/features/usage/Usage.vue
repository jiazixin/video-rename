<script setup lang="ts">
  import UsageTokens from './UsageTokens.vue';
  import UsageAnalysis from './UsageAnalysis.vue';
  defineProps<{ tab: string }>();
</script>
<template>
  <section>
    <div class="page-heading"
      ><div
        ><h1>{{ tab === 'analysis' ? '运行分析' : '模型用量' }}<span class="title-dot">.</span></h1
        ><p>{{
          tab === 'analysis' ? '查看处理效率、成功率与失败原因。' : '查看模型请求与 Token 消耗。'
        }}</p></div
      ></div
    >
    <UsageTokens v-if="tab !== 'analysis'" />
    <UsageAnalysis v-else />
  </section>
</template>
