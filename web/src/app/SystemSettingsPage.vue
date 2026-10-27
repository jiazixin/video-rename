<script setup lang="ts">
  import {
    Bell,
    Film,
    FileText,
    FolderSearch,
    Hash,
    MessageSquareText,
    Settings2
  } from '@lucide/vue';
  import { NumberingSettings, ProcessingSettings } from '../features/settings';
  import { LibraryScanSettings } from '../features/library';
  import { useRouter } from 'vue-router';
  import ModelsPage from '../features/models/ModelsPage.vue';
  import PromptsSettings from '../features/prompts/PromptsSettings.vue';
  import SamplingPage from '../features/sampling/SamplingPage.vue';
  import NotificationsPage from '../features/notifications/NotificationsPage.vue';
  import { vTabIndicator } from '../lib/motion/tab-indicator';
  import './system-settings.css';
  const { tab } = defineProps<{ tab: string }>();
  defineEmits<{ changed: [] }>();
  const tabs = [
    { id: 'processing', label: '处理与缓存', icon: Settings2 },
    { id: 'scan', label: '后台目录扫描', icon: FolderSearch },
    { id: 'numbering', label: '编号规则', icon: Hash },
    { id: 'prompts', label: 'Prompt配置', icon: FileText },
    { id: 'models', label: '模型配置', icon: MessageSquareText },
    { id: 'sampling', label: '抽帧设置', icon: Film },
    { id: 'notifications', label: '通知设置', icon: Bell }
  ];
  const router = useRouter();
  function select(id: string) {
    void router.push(`/settings/${id}`);
  }
</script>
<template>
  <section class="system-settings"
    ><div class="page-heading"
      ><div
        ><h1>系统设置<span class="title-dot">.</span></h1
        ><p>管理处理资源、后台目录扫描、编号规则、Prompt、模型、抽帧规则与通知。</p></div
      ></div
    ><div v-tab-indicator class="log-tabs settings-tabs" role="group" aria-label="系统设置分类"
      ><button
        v-for="item in tabs"
        :key="item.id"
        :class="{ active: tab === item.id }"
        :aria-pressed="tab === item.id"
        @click="select(item.id)"
        ><component :is="item.icon" :size="18" />{{ item.label }}</button
      ></div
    ><Transition name="panel" mode="out-in"
      ><ProcessingSettings v-if="tab === 'processing'" /><LibraryScanSettings
        v-else-if="tab === 'scan'" /><NumberingSettings
        v-else-if="tab === 'numbering'" /><PromptsSettings
        v-else-if="tab === 'prompts'" /><ModelsPage
        v-else-if="tab === 'models'"
        @changed="$emit('changed')" /><SamplingPage
        v-else-if="tab === 'sampling'" /><NotificationsPage
        v-else-if="tab === 'notifications'" /></Transition
  ></section>
</template>
