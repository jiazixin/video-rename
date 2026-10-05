<script setup lang="ts">
  import './login.css';
  import { ref } from 'vue';
  import { ArrowRight, Eye, EyeOff, Folder, FileVideo, Check, ShieldCheck } from '@lucide/vue';
  import { api, session } from '../../app/api';
  const username = ref(''),
    password = ref(''),
    visible = ref(false),
    busy = ref(false),
    error = ref('');
  async function login() {
    busy.value = true;
    error.value = '';
    try {
      session.value = await api('/auth/login', 'POST', {
        username: username.value,
        password: password.value
      });
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      busy.value = false;
    }
  }
</script>
<template>
  <div class="login-page"
    ><div class="login-story"
      ><a class="brand" href="/"
        ><span class="brand-mark"><img src="/logo.png" alt="" width="32" height="32" /></span
        >video-rename<span class="edition">LOCAL WORKSPACE</span></a
      ><div class="story-copy"
        ><h1>视频重命名</h1
        ><div class="story-demo" aria-hidden="true"
          ><div class="demo-heading"
            ><Folder :size="18" /><span>我的影像 / 旅行记录</span
            ><span class="demo-dots">···</span></div
          ><div class="demo-file"
            ><FileVideo :size="24" /><div
              ><span class="strike">VID_20260920_001.mp4</span
              ><strong>山间晨雾与日出.mp4</strong></div
            ><span class="check-round"><Check :size="15" /></span></div
          ><div class="demo-footer"><span>自动命名</span></div></div
        ></div
      ></div
    ><main class="login-main"
      ><div class="login-form"
        ><h2>登录工作台</h2><p>使用配置文件中设置的账号，管理你的视频库。</p
        ><form @submit.prevent="login"
          ><label
            >用户名<input
              v-model="username"
              name="username"
              autocomplete="username"
              required
              placeholder="输入用户名" /></label
          ><label
            >密码<div class="password-wrap"
              ><input
                v-model="password"
                name="password"
                :type="visible ? 'text' : 'password'"
                autocomplete="current-password"
                required
                placeholder="输入密码" /><button
                type="button"
                :aria-label="visible ? '隐藏密码' : '显示密码'"
                @click="visible = !visible"
                ><EyeOff v-if="visible" :size="18" /><Eye v-else :size="18" /></button></div></label
          ><div v-if="error" role="alert" class="notice error">{{ error }}</div
          ><button class="primary login-submit" :disabled="busy"
            >{{ busy ? '正在登录…' : '登录' }}<ArrowRight :size="17" /></button></form
        ><div class="login-hint"><ShieldCheck :size="16" /> 仅限已授权的媒体库管理员访问</div></div
      ><div class="login-bottom">video-rename </div></main
    ></div
  >
</template>
