<script setup lang="ts">
  import { RouterLink } from 'vue-router';
  import { errorText } from '../app/errors';
  import {
    Settings2,
    FolderOpen,
    ScrollText,
    Clock3,
    LogOut,
    BarChart3,
    Images,
    ListVideo,
    Coins
  } from '@lucide/vue';
  import { api, session, setSession } from '../app/api';
  import { computed, ref } from 'vue';
  const { page, model, ready } = defineProps<{ page: string; model: string; ready: boolean }>();
  const emit = defineEmits<{ navigate: [page: string] }>();
  const error = ref('');
  const groups = [
    { label: '工作台', items: [{ id: 'library', name: '文件目录', icon: FolderOpen }] },
    {
      label: '任务记录',
      items: [
        { id: 'extractions', name: '抽帧记录', icon: Images },
        { id: 'history', name: '运行记录', icon: ScrollText }
      ]
    },
    {
      label: '运行管理',
      items: [
        { id: 'plans', name: '计划管理', icon: Clock3 },
        { id: 'runs', name: '批次记录', icon: ListVideo }
      ]
    },
    {
      label: '分析',
      items: [
        { id: 'usage/tokens', name: '模型用量', icon: Coins },
        { id: 'usage/analysis', name: '运行分析', icon: BarChart3 }
      ]
    },
    { label: '系统', items: [{ id: 'settings', name: '系统设置', icon: Settings2 }] }
  ];
  const modelLabel = computed(() => model.split(/[\\/]/).filter(Boolean).at(-1) || '未连接');
  async function logout() {
    try {
      await api<unknown>('/auth/logout', { method: 'POST', body: {} });
      setSession(null);
    } catch (e) {
      error.value = errorText(e);
    }
  }
</script>
<template>
  <div class="workspace">
    <aside class="sidebar">
      <RouterLink to="/library" class="brand"
        ><span class="brand-mark"><img src="/logo.png" alt="" width="40" height="40" /></span
        ><span>video-rename</span></RouterLink
      >
      <nav aria-label="主导航">
        <div
          v-for="group in groups"
          :key="group.label"
          class="nav-group"
          role="group"
          :aria-label="group.label"
        >
          <div v-if="group.label" class="sidebar-caption">{{ group.label }}</div>
          <button
            v-for="item in group.items"
            :key="item.id"
            :class="{ active: page === item.id }"
            :aria-label="item.name"
            :aria-current="page === item.id ? 'page' : undefined"
            @click="emit('navigate', item.id)"
            ><component :is="item.icon" :size="20" :stroke-width="1.8" /><span>{{
              item.name
            }}</span></button
          >
        </div>
      </nav>
      <div class="sidebar-footer">
        <div class="model-box"
          ><div class="model-name"
            ><i class="signal" :class="{ off: !ready }" /><strong
              :title="ready ? model : '自动命名暂不可用'"
              >{{ ready ? modelLabel : '模型未连接' }}</strong
            ></div
          ><button class="model-settings" @click="emit('navigate', 'settings/models')">{{
            ready ? '管理模型' : '配置模型'
          }}</button></div
        >
        <div class="account"
          ><div class="avatar">{{ session?.username.slice(0, 1).toUpperCase() }}</div
          ><div class="account-info"
            ><strong :title="session?.username">{{ session?.username }}</strong
            ><small>管理员</small></div
          ><button class="icon-btn" aria-label="退出登录" title="退出登录" @click="logout"
            ><LogOut :size="20" :stroke-width="1.8" /></button></div
        ><p v-if="error" class="error">{{ error }}</p>
      </div>
    </aside>
    <div class="workspace-main"
      ><main class="page-main"><slot /></main
    ></div>
  </div>
</template>
<style scoped>
  .signal.off {
    background: var(--palette-a78240);
  }
  .model-box {
    padding: 16px 12px 22px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .model-box strong {
    font-size: 12px;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .edition {
    font-size: 9px;
    color: var(--ash);
    letter-spacing: 1px;
    margin-left: 14px;
    font-weight: 500;
  }
  .workspace {
    display: flex;
    min-height: 100vh;
  }
  .sidebar {
    width: 224px;
    position: fixed;
    top: 0;
    bottom: 0;
    left: 0;
    background: var(--surface);
    border-right: 1px solid var(--line);
    padding: 28px 18px 0;
    display: flex;
    flex-direction: column;
    z-index: 10;
  }
  .sidebar > .brand {
    padding: 0 8px;
    margin-bottom: 52px;
  }
  .sidebar-caption {
    font-size: 10px;
    color: var(--palette-92959b);
    letter-spacing: 1px;
    padding: 0 12px;
    margin-bottom: 12px;
  }
  .sidebar nav {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .sidebar nav button {
    justify-content: flex-start;
    border: 1px solid transparent;
    padding: 12px;
    background: transparent;
    color: var(--steel);
    font-weight: 400;
    font-size: 13px;
  }
  .sidebar nav button.active {
    background: var(--palette-ffffff);
    border-color: var(--line);
    color: var(--ink);
    font-weight: 500;
    box-shadow: 0 1px 2px var(--palette-00000003);
  }
  .sidebar-footer {
    margin-top: auto;
  }
  .account {
    border-top: 1px solid var(--line);
    padding: 20px 4px;
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .avatar {
    width: 32px;
    height: 32px;
    border-radius: 50%;
    background: var(--palette-e7e9ed);
    display: grid;
    place-items: center;
    font-size: 12px;
  }
  .account strong {
    font-size: 12px;
    display: block;
    max-width: 100px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .account small {
    font-size: 10px;
    color: var(--ash);
    display: block;
  }
  .account .icon-btn {
    margin-left: auto;
    color: var(--steel);
  }
  .workspace-main {
    margin-left: 224px;
    min-width: 0;
    flex: 1;
    background: var(--palette-ffffff);
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }
  .page-main {
    padding: 40px 40px 56px;
    max-width: 1600px;
    width: 100%;
    margin: 0 auto;
    flex: 1;
  }
  @media (min-width: 1600px) {
    .page-main {
      padding: 50px 60px;
    }
  }
  @media (max-width: 1150px) {
    .page-main {
      padding: 30px 24px;
    }
  }
  @media (max-width: 1150px) {
    .sidebar {
      width: 205px;
    }
  }
  @media (max-width: 1150px) {
    .workspace-main {
      margin-left: 205px;
    }
  }
  @media (max-width: 1150px) {
    .edition {
      display: none;
    }
  }
  @media (max-width: 800px) {
    .sidebar {
      width: 70px;
      padding: 20px 9px;
    }
  }
  @media (max-width: 800px) {
    .sidebar .brand > span:not(.brand-mark),
    .sidebar-caption,
    .sidebar nav button > span,
    .sidebar-footer .model-box,
    .account > div:not(.avatar),
    .sidebar .brand {
      display: none;
    }
  }
  @media (max-width: 800px) {
    .sidebar nav {
      margin-top: 70px;
    }
  }
  @media (max-width: 800px) {
    .sidebar nav button {
      justify-content: center;
      padding: 12px;
    }
  }
  @media (max-width: 800px) {
    .account {
      flex-direction: column;
      padding: 20px 0;
    }
  }
  @media (max-width: 800px) {
    .account .icon-btn {
      margin: 0;
    }
  }
  @media (max-width: 800px) {
    .workspace-main {
      margin-left: 70px;
    }
  }
  @media (max-width: 800px) {
    .page-main {
      padding: 26px 20px;
    }
  }

  .workspace {
    --sidebar-width: 256px;
    --sidebar-bg: var(--palette-fbfcfb);
    --sidebar-border: var(--palette-e7e9ec);
    --sidebar-muted: var(--palette-6b7570);
    --sidebar-hover: var(--palette-f2f4f5);
    --sidebar-active: var(--palette-edf6f2);
    --sidebar-accent: var(--palette-208a70);
  }
  .sidebar {
    width: var(--sidebar-width);
    height: 100vh;
    height: 100dvh;
    padding: 0 12px;
    background: var(--sidebar-bg);
    border-right: 1px solid var(--sidebar-border);
    overflow: hidden;
  }
  .workspace-main {
    margin-left: var(--sidebar-width);
  }
  .sidebar > .brand {
    display: flex;
    gap: 12px;
    min-height: 72px;
    padding: 0 8px;
    margin: 0 0 12px;
    flex-shrink: 0;
    font-size: 19px;
    font-weight: 700;
    letter-spacing: -0.4px;
    color: var(--palette-17191c);
  }
  .sidebar .brand-mark {
    width: 40px;
    height: 40px;
    min-width: 40px;
    padding: 0;
    background: transparent;
    border: 0;
    box-shadow: none;
    border-radius: 8px;
    overflow: hidden;
  }
  .sidebar .brand-mark img {
    display: block;
    width: 40px;
    height: 40px;
    object-fit: contain;
  }
  .sidebar nav {
    overflow-y: auto;
    overflow-x: hidden;
    min-height: 0;
    gap: 16px;
    padding: 0 0 12px;
    flex: 0 1 auto;
    scrollbar-width: thin;
  }
  .nav-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
    flex-shrink: 0;
  }
  .sidebar-caption {
    padding: 0 12px;
    margin: 0 0 2px;
    font-size: 12px;
    line-height: 16px;
    font-weight: 500;
    letter-spacing: 0.02em;
    color: var(--sidebar-muted);
  }
  .sidebar nav button {
    height: 40px;
    padding: 0 12px;
    gap: 12px;
    border: 0;
    border-radius: 8px;
    flex-shrink: 0;
    background: transparent;
    color: var(--palette-59616c);
    font-size: 14px;
    font-weight: 500;
    box-shadow: none;
    transition:
      background-color var(--motion-hover) linear,
      color var(--motion-hover) linear;
  }
  .sidebar nav button svg {
    color: var(--palette-707781);
    stroke-width: 1.8;
  }
  .sidebar nav button:hover {
    background: var(--sidebar-hover);
    color: var(--palette-282d32);
  }
  .sidebar nav button.active,
  .sidebar nav button.active:hover {
    background: var(--sidebar-active);
    color: var(--palette-185c4a);
    font-weight: 600;
    border: 0;
    box-shadow: none;
  }
  .sidebar nav button.active svg {
    color: var(--sidebar-accent);
  }
  .sidebar nav button:focus-visible {
    outline: 2px solid var(--sidebar-accent);
    outline-offset: -2px;
  }
  .sidebar-footer {
    margin-top: auto;
    flex-shrink: 0;
    min-width: 0;
    padding-top: 12px;
  }
  .sidebar .model-box {
    border-top: 1px solid var(--sidebar-border);
    padding: 14px 8px;
    gap: 8px;
    min-width: 0;
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
  }
  .sidebar .model-box .eyebrow {
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0;
    color: var(--sidebar-muted);
  }
  .model-name {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    flex: 1;
  }
  .sidebar .model-box strong {
    min-width: 0;
    font-size: 12px;
    font-weight: 500;
    color: var(--palette-526159);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sidebar .signal {
    width: 8px;
    height: 8px;
    background: var(--sidebar-accent);
  }
  .sidebar .signal.off {
    background: var(--sidebar-muted);
  }
  .sidebar .model-settings {
    border: 0;
    background: transparent;
    padding: 4px 0;
    font-size: 12px;
    color: var(--palette-20634e);
    flex-shrink: 0;
  }
  .sidebar .model-settings:hover {
    color: var(--palette-124d39);
    text-decoration: underline;
  }
  .sidebar .account {
    padding: 14px 8px;
    gap: 10px;
    border-top: 1px solid var(--sidebar-border);
    transition: background-color var(--motion-hover) linear;
  }
  .sidebar .account:hover {
    background: var(--palette-f4f5f6);
  }
  .sidebar .avatar {
    width: 36px;
    height: 36px;
    flex-shrink: 0;
    font-size: 14px;
    background: var(--palette-205c4b);
    color: var(--palette-ffffff);
  }
  .account-info {
    min-width: 0;
    flex: 1;
  }
  .sidebar .account strong {
    font-size: 14px;
    font-weight: 600;
    max-width: none;
    color: var(--palette-2b3035);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sidebar .account small {
    font-size: 12px;
    color: var(--palette-6c737c);
  }
  .sidebar .account .icon-btn {
    width: 32px;
    height: 32px;
    flex-shrink: 0;
    padding: 4px;
    margin-left: auto;
    color: var(--palette-707781);
    border: 0;
    background: transparent;
  }
  .sidebar .account .icon-btn:hover {
    color: var(--palette-282d32);
    background: var(--palette-e9edf0);
  }
  .sidebar .account svg {
    stroke-width: 1.8;
  }
  .sidebar-footer > .error {
    padding: 8px 12px;
    overflow-wrap: anywhere;
  }
  @media (max-width: 800px) {
    .workspace {
      --sidebar-width: 64px;
    }
    .sidebar {
      padding: 0 8px;
    }
    .sidebar > .brand {
      display: flex;
      min-height: 72px;
      padding: 0;
      justify-content: center;
      margin-bottom: 8px;
    }
    .sidebar nav {
      margin-top: 0;
      gap: 16px;
      padding-bottom: 8px;
    }
    .sidebar-caption {
      display: none;
    }
    .sidebar nav button {
      justify-content: center;
      padding: 0;
      height: 44px;
    }
    .sidebar-footer {
      padding-top: 8px;
    }
    .sidebar .model-box {
      display: none;
    }
    .sidebar .account {
      flex-direction: column;
      padding: 12px 0;
      gap: 8px;
    }
    .sidebar .account .icon-btn {
      margin: 0;
    }
  }
</style>
