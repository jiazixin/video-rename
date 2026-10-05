<script setup lang="ts">
  import { Folder, FileVideo, Image, FileAudio } from '@lucide/vue';
  import { type Entry } from '../../app/contracts';
  defineProps<{ contents?: Entry['contents']; inline?: boolean }>();
  const types = [
    { key: 'directories', label: '子目录', icon: Folder },
    { key: 'videos', label: '视频', icon: FileVideo },
    { key: 'images', label: '图片', icon: Image },
    { key: 'audio', label: '音频', icon: FileAudio }
  ] as const;
</script>

<template>
  <div
    v-if="contents"
    class="directory-contents"
    :class="{ 'contents-inline': inline }"
    role="group"
    aria-label="目录内容（仅直接子项）"
    title="最近完整索引的直接子项数量，包含全部分页，不受搜索和类型筛选影响"
  >
    <span
      v-for="type in types"
      :key="type.key"
      class="content-stat"
      :class="type.key"
      :title="`${type.label}：${contents[type.key].toLocaleString()}`"
      :aria-label="`${type.label}：${contents[type.key].toLocaleString()}`"
    >
      <component :is="type.icon" :size="14" aria-hidden="true" />
      <span v-if="inline">{{ type.label }}</span>
      <strong>{{ contents[type.key].toLocaleString() }}</strong>
    </span>
  </div>
  <span v-else class="contents-pending" title="目录内容待统计">待统计</span>
</template>

<style scoped>
  .directory-contents {
    display: inline-grid;
    grid-template-columns: repeat(2, max-content);
    align-items: center;
    gap: 3px 12px;
    max-width: 100%;
    vertical-align: middle;
  }
  .directory-contents.contents-inline {
    display: inline-flex;
    flex-wrap: nowrap;
    gap: 12px;
  }
  .contents-inline .content-stat {
    flex-shrink: 0;
  }
  .content-stat {
    display: flex;
    align-items: center;
    gap: 4px;
    white-space: nowrap;
    font-size: 11px;
    line-height: 18px;
    color: #60646c;
  }
  .content-stat strong {
    margin-left: auto;
  }
  .content-stat strong {
    font-size: 12px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    color: #303940;
  }
  .directories svg {
    color: #657b6c;
  }
  .videos svg {
    color: #366ba0;
  }
  .images svg {
    color: #785ba0;
  }
  .audio svg {
    color: #896018;
  }
  .contents-pending {
    font-size: 11px;
    color: var(--steel);
    white-space: nowrap;
  }
  @media (max-width: 600px) {
    .directory-contents.contents-inline {
      gap: 8px;
    }
  }
</style>
