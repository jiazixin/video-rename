<script setup lang="ts">
  import ToastNotice from '../../components/ToastNotice.vue';
  import DateTime from '../../components/DateTime.vue';
  import PageNavigation from '../../components/PageNavigation.vue';
  import SelectField from '../../components/SelectField.vue';
  import { computed, ref, onMounted, onUnmounted } from 'vue';
  import { Save, Send, RefreshCw, CircleHelp } from '@lucide/vue';
  import { api } from '../../app/api';
  import './notifications.css';
  const toast = ref<InstanceType<typeof ToastNotice>>();
  type Channel = {
    kind: string;
    enabled: boolean;
    failure_only: boolean;
    has_endpoint: boolean;
    has_token: boolean;
    has_secret: boolean;
    chat_id: string;
    endpoint: string;
    token: string;
    secret: string;
    clear_secret: boolean;
  };
  const names: Record<string, string> = {
    wecom: '企业微信',
    telegram: 'Telegram',
    dingtalk: '钉钉',
    bark: 'Bark',
    webhook: '自定义'
  };
  const hints: Record<string, string> = {
    wecom: '企业微信群机器人 Webhook，包含 key。',
    telegram: '填写 BotFather 创建的 Bot Token 和接收者 Chat ID，先与机器人发起会话。',
    dingtalk: '填写群机器人 Webhook；如启用加签，请同时填写密钥。关键词校验可设置 video-rename。',
    bark: '填写以设备 Key 结尾的地址，如 https://api.day.app/设备Key/；不要包含示例标题或正文，支持自建服务。',
    webhook: '向此地址 POST JSON，包含 event、title 和 body 字段。'
  };
  const help: Record<string, string[]> = {
    wecom: [
      '在企业微信内部群的群设置中，选择「群机器人 → 添加机器人」。',
      '创建后复制完整 Webhook 到「推送地址」：https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=你的key'
    ],
    telegram: [
      '在 Telegram 找 @BotFather，发送 /newbot 创建机器人，将返回的 Token 填入「Bot Token」。',
      '私聊机器人发送 /start；群通知先把机器人加入群，再在群里发送 /start@机器人用户名。',
      '访问 https://api.telegram.org/bot你的Token/getUpdates，复制对应消息的 message.chat.id 到「Chat ID」（群 ID 保留负号）。已绑定 Webhook 的机器人不能用 getUpdates 获取。'
    ],
    dingtalk: [
      '在钉钉群设置的「智能群助手／机器人」中，添加「自定义」机器人。',
      '复制完整 Webhook 到「推送地址」：https://oapi.dingtalk.com/robot/send?access_token=你的token',
      '安全设置可选关键词 video-rename；若选择「加签」，把 SEC 开头的密钥一并填入「加签密钥」。'
    ],
    bark: [
      '打开 Bark App，复制推送地址，只保留到设备 Key：https://api.day.app/你的设备Key/',
      '删除 Key 后面的示例标题和正文。自建服务同样填写「服务地址/设备Key/」。'
    ],
    webhook: [
      '填写接收通知的完整 HTTP/HTTPS 接口地址。',
      '本系统以 POST JSON 发送 event、title、body；接收端读取 title 和 body，返回 HTTP 2xx 表示成功。'
    ]
  };
  const hoverHelp = ref(''),
    openHelp = ref('');
  function closeHelp() {
    hoverHelp.value = '';
    openHelp.value = '';
  }
  type Delivery = {
    id: number;
    kind: string;
    run: number;
    status: string;
    attempts: number;
    updated: string;
    error: string;
  };
  type HistoryPage = { items: Delivery[]; total: number; page: number; size: number };
  const pageNumber = ref(1),
    size = ref(50),
    total = ref(0),
    historyLoading = ref(false),
    historyError = ref('');
  const totalPages = computed(() => Math.max(1, Math.ceil(total.value / size.value)));
  let historySeq = 0;
  const channels = ref<Channel[]>([]),
    history = ref<Delivery[]>([]),
    error = ref(''),
    errors = ref<Record<string, string>>({}),
    busy = ref<Record<string, boolean>>({});
  let alive = true,
    timer: ReturnType<typeof setTimeout> | undefined;
  async function load() {
    try {
      const data = await api<Channel[]>('/notifications');
      channels.value = data.map((c) => ({
        ...c,
        endpoint: '',
        token: '',
        secret: '',
        clear_secret: false
      }));
      error.value = '';
    } catch (e) {
      error.value = (e as Error).message;
    }
  }
  async function logs() {
    const seq = ++historySeq;
    historyLoading.value = true;
    try {
      const data = await api<HistoryPage>(
        `/notifications/history?page=${pageNumber.value}&size=${size.value}`
      );
      if (alive && seq === historySeq) {
        history.value = data.items;
        total.value = data.total;
        pageNumber.value = data.page;
        size.value = data.size;
        historyError.value = '';
      }
    } catch (e) {
      if (alive && seq === historySeq) historyError.value = (e as Error).message;
    } finally {
      if (alive && seq === historySeq) historyLoading.value = false;
    }
  }
  function changePage(value: number) {
    pageNumber.value = value;
    logs();
  }
  function changeSize() {
    pageNumber.value = 1;
    logs();
  }
  async function poll() {
    if (!historyLoading.value) await logs();
    if (alive) timer = setTimeout(poll, 5000);
  }
  async function save(c: Channel) {
    busy.value[c.kind] = true;
    errors.value[c.kind] = '';
    try {
      await api(`/notifications/${c.kind}`, 'PUT', {
        enabled: c.enabled,
        failure_only: c.failure_only,
        endpoint: c.endpoint,
        token: c.token,
        secret: c.secret,
        chat_id: c.chat_id,
        clear_secret: c.clear_secret
      });
      const saved = (await api<Channel[]>('/notifications')).find((x) => x.kind === c.kind)!;
      Object.assign(c, saved, { endpoint: '', token: '', secret: '', clear_secret: false });
      toast.value?.show(`${names[c.kind]}设置已保存`);
    } catch (e) {
      errors.value[c.kind] = (e as Error).message;
    } finally {
      busy.value[c.kind] = false;
    }
  }
  async function test(c: Channel) {
    busy.value[c.kind] = true;
    errors.value[c.kind] = '';
    try {
      await api(`/notifications/${c.kind}/test`, 'POST', {});
      toast.value?.show(`${names[c.kind]}测试通知已发送（使用已保存的配置）`);
    } catch (e) {
      errors.value[c.kind] = (e as Error).message;
    } finally {
      busy.value[c.kind] = false;
    }
  }
  onMounted(() => {
    load();
    poll();
  });
  onUnmounted(() => {
    alive = false;
    historySeq++;
    clearTimeout(timer);
  });
  const userTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const statuses: Record<string, string> = {
    pending: '等待发送',
    sending: '正在发送',
    sent: '已发送',
    failed: '发送失败',
    cancelled: '已停止'
  };
</script>
<template>
  <section class="notification-settings"
    ><ToastNotice ref="toast" /><header class="notification-intro"
      ><span class="eyebrow">消息推送</span><h2>批次结束后，及时收到结果</h2
      ><p class="help">按渠道保存配置，可发送测试通知检查连接。</p></header
    ><div v-if="error || historyError" class="notice error" role="alert"
      >{{ error || historyError
      }}<button
        @click="
          load();
          logs();
        "
        >重试</button
      ></div
    ><div class="notice notification-note"
      >开启或重新开启后，对新创建的批次生效。通知包含目录、模型、处理数量、提交／开始／完成时间与耗时；发送失败最多尝试
      3 次，不影响文件处理。</div
    ><p class="help"
      >通知时间使用最近登录浏览器的时区（当前：{{ userTimezone }}），保存后关闭页面仍然生效。</p
    ><div class="notification-grid"
      ><form
        v-for="c in channels"
        :key="c.kind"
        class="panel notification-card"
        :aria-label="names[c.kind]"
        @submit.prevent="save(c)"
        ><header
          ><div class="notification-title"
            ><h2>{{ names[c.kind] }}</h2
            ><div
              class="notification-help"
              @mouseenter="hoverHelp = c.kind"
              @mouseleave="hoverHelp = ''"
              @keydown.esc="closeHelp"
              ><button
                type="button"
                class="notification-help-button"
                :aria-label="`${names[c.kind]}配置说明`"
                :aria-expanded="hoverHelp === c.kind || openHelp === c.kind"
                :aria-describedby="`notification-help-${c.kind}`"
                @focus="hoverHelp = c.kind"
                @blur="closeHelp"
                @click="openHelp = openHelp === c.kind ? '' : c.kind"
                ><CircleHelp :size="16" /></button
              ><div
                v-show="hoverHelp === c.kind || openHelp === c.kind"
                :id="`notification-help-${c.kind}`"
                class="notification-help-popover"
                role="tooltip"
                ><strong>{{ names[c.kind] }}配置方式</strong
                ><p v-for="(step, i) in help[c.kind]" :key="i">{{ i + 1 }}. {{ step }}</p
                ><p class="notification-help-last">填写后保存设置，再点击「发送测试」。</p></div
              ></div
            ></div
          ><label class="check-label"
            ><input
              type="checkbox"
              v-model="c.enabled"
              :aria-label="`启用${names[c.kind]}`"
            />启用</label
          ></header
        ><p class="help">{{ hints[c.kind] }}</p
        ><div class="notification-fields"
          ><template v-if="c.kind === 'telegram'"
            ><label
              >Bot Token<input
                type="password"
                v-model="c.token"
                :aria-label="`${names[c.kind]} Bot Token`"
                :placeholder="c.has_token ? '已保存，留空保持不变' : '123456:ABC…'"
                autocomplete="new-password" /></label
            ><label
              >Chat ID<input
                v-model="c.chat_id"
                :aria-label="`${names[c.kind]} Chat ID`"
                placeholder="用户或群组 ID" /></label></template
          ><label v-else
            >推送地址<input
              type="password"
              v-model="c.endpoint"
              :aria-label="`${names[c.kind]}推送地址`"
              :placeholder="c.has_endpoint ? '已保存，留空保持不变' : 'https://…'"
              autocomplete="new-password" /></label
          ><template v-if="c.kind === 'dingtalk'"
            ><label
              >加签密钥（可选）<input
                type="password"
                v-model="c.secret"
                aria-label="钉钉加签密钥"
                :placeholder="c.has_secret ? '已保存，留空保持不变' : 'SEC…'"
                autocomplete="new-password" /></label
            ><label v-if="c.has_secret" class="check-label"
              ><input type="checkbox" v-model="c.clear_secret" />清除已保存的加签密钥</label
            ></template
          ><label class="check-label"
            ><input type="checkbox" v-model="c.failure_only" />仅失败时通知</label
          ><small class="help">包含部分失败、失败与服务中断；关闭此选项也通知完成和取消。</small
          ><div v-if="errors[c.kind]" class="notice error" role="alert">{{ errors[c.kind] }}</div
          ><footer
            ><button class="primary" :disabled="busy[c.kind]"><Save :size="15" />保存设置</button
            ><button
              type="button"
              :disabled="busy[c.kind] || !(c.has_endpoint || c.has_token)"
              @click="test(c)"
              ><Send :size="15" />发送测试</button
            ></footer
          ></div
        ></form
      ></div
    >
    <div class="section-heading"
      ><h2>最近发送记录</h2
      ><button :disabled="historyLoading" @click="logs"><RefreshCw :size="14" />刷新</button></div
    ><div class="panel"
      ><div class="table-scroll"
        ><table
          ><thead
            ><tr
              ><th>批次</th><th>渠道</th><th>发送状态</th><th>尝试次数</th><th>更新时间</th
              ><th>说明</th></tr
            ></thead
          ><tbody
            ><tr v-for="d in history" :key="d.id"
              ><td>#{{ d.run }}</td
              ><td>{{ names[d.kind] }}</td
              ><td>{{ statuses[d.status] || d.status }}</td
              ><td>{{ d.attempts }}</td
              ><td class="date-cell"><DateTime :value="d.updated" /></td
              ><td>{{ d.error || '—' }}</td></tr
            ></tbody
          ></table
        ><p v-if="!history.length" class="empty compact"
          >暂无发送记录。保存并启用渠道后，新批次结束时会在这里显示结果。</p
        ></div
      ><div class="pagination" aria-label="通知发送记录分页"
        ><span class="subtle"
          >共 {{ total }} 条记录 · 第 {{ pageNumber }} / {{ totalPages }} 页 · 当前页
          {{ history.length }} 条</span
        ><SelectField
          v-model="size"
          label="每页发送记录数"
          :options="[
            { value: 50, label: '50 条 / 页' },
            { value: 100, label: '100 条 / 页' },
            { value: 200, label: '200 条 / 页' }
          ]"
          @change="changeSize" /><PageNavigation
          :page="pageNumber"
          :total-pages="totalPages"
          :disabled="historyLoading"
          previous-label="上一页发送记录"
          next-label="下一页发送记录"
          @change="changePage" /></div></div
  ></section>
</template>
