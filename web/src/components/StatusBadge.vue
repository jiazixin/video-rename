<script setup lang="ts">
  const { status } = defineProps<{ status: string }>();
</script>
<template>
  <span class="status-badge" :data-status="status"
    ><slot name="icon"><span class="status-dot" aria-hidden="true" /></slot><slot
  /></span>
</template>
