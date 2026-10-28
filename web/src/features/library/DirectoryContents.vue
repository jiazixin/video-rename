<script setup lang="ts">
  import { formatCount } from '../../lib/format/number';
  import { Folder, FileVideo, Image, FileAudio } from '@lucide/vue';
  import { type Entry } from '../../app/contracts';
  const { contents = null, inline } = defineProps<{
    contents?: Entry['contents'];
    inline?: boolean;
  }>();
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
    role="list"
    :aria-label="inline ? '当前目录内容（仅直接子项）' : '目录内容（仅直接子项）'"
    title="最近完整索引的直接子项数量，包含全部分页，不受搜索和类型筛选影响"
  >
    <span
      v-for="type in types"
      :key="type.key"
      class="content-stat"
      role="listitem"
      :class="type.key"
      :title="`${type.label}：${formatCount(contents[type.key])}`"
      :aria-label="`${type.label}：${formatCount(contents[type.key])}`"
    >
      <component :is="type.icon" :size="14" aria-hidden="true" />
      <span v-if="inline">{{ type.label }}</span>
      <strong>{{ formatCount(contents[type.key]) }}</strong>
    </span>
  </div>
  <span v-else class="contents-pending" title="目录内容待统计">待统计</span>
</template>

<style scoped>
  .directory-contents {
    display: inline-grid;
    grid-template-columns: repeat(2, max-content);
    align-items: center;
    gap: var(--space-3) var(--space-12);
    max-width: 100%;
    vertical-align: middle;
  }
  .directory-contents.contents-inline {
    display: inline-flex;
    flex-wrap: nowrap;
    gap: var(--space-12);
  }
  .contents-inline .content-stat {
    flex-shrink: 0;
  }
  .content-stat {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    white-space: nowrap;
    font-size: var(--font-size-11);
    line-height: var(--line-height-18);
    color: var(--steel);
  }
  .content-stat strong {
    margin-left: auto;
  }
  .content-stat strong {
    font-size: var(--font-size-12);
    font-weight: var(--font-weight-600);
    font-variant-numeric: tabular-nums;
    color: var(--palette-303940);
  }
  .directories svg {
    color: var(--palette-657b6c);
  }
  .videos svg {
    color: var(--palette-366ba0);
  }
  .images svg {
    color: var(--palette-785ba0);
  }
  .audio svg {
    color: var(--palette-896018);
  }
  .contents-pending {
    font-size: var(--font-size-11);
    color: var(--steel);
    white-space: nowrap;
  }
  @media (max-width: 600px) {
    .directory-contents.contents-inline {
      gap: var(--space-8);
    }
  }
</style>
