<script setup lang="ts">
  defineProps<{ status: string }>();
</script>
<template>
  <span class="status-badge" :data-status="status"
    ><span class="status-dot" aria-hidden="true" /><slot
  /></span>
</template>
