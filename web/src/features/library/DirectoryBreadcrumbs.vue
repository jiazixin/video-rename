<script setup lang="ts">
  import { ChevronRight, Home } from '@lucide/vue';
  import SelectField from '../../components/SelectField.vue';
  import type { Page } from '../../app/contracts';
  const { page } = defineProps<{ page: Page | undefined }>();
  const emit = defineEmits<{ enter: [id: number] }>();
</script>
<template>
  <nav aria-label="目录路径">
    <button type="button" @click="emit('enter', page?.breadcrumbs[0]?.id || 1)">
      <Home :size="15" />{{ page?.media_roots?.length ? '当前根目录' : '媒体库' }}
    </button>
    <SelectField
      v-if="page?.media_roots?.length"
      label="媒体目录"
      :model-value="page.breadcrumbs[0]?.id || 1"
      :options="page.media_roots.map((root) => ({ value: root.id, label: root.name }))"
      @update:model-value="emit('enter', Number($event))"
    />
    <template v-for="crumb in page?.breadcrumbs.slice(1)" :key="crumb.id">
      <ChevronRight :size="13" /><button type="button" @click="emit('enter', crumb.id)">{{
        crumb.name
      }}</button>
    </template>
  </nav>
</template>
