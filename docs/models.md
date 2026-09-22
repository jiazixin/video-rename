# 模型配置

页面每条配置包含：名称、服务类型、基础 URL、API 密钥、模型 ID、温度、最大输出 token、请求超时和命名提示词。提供智谱、DeepSeek、OpenAI、Claude、Ollama 与自定义预设。URL 预设可修改；模型 ID 可手工输入，不绑定易过时的固定型号。

## 使用

1. 打开“模型配置”，编辑初始记录或新增配置，填写服务 URL、模型和提示词。
2. 可点击“检测可用模型”获取该服务返回的列表并选择。检测只执行模型列表请求，不发送视频，不证明模型支持图片或账户有推理配额。未提供列表接口的服务可直接手工输入 ID。
3. 点击圆点设为全局默认。新运行 / 计划预选默认；已有计划保留原绑定，每次触发时读取该配置的最新值。
4. 每个运行在创建时保存模型和提示词快照，排队后编辑配置仅并发数同步到该运行，其他参数仍使用快照。运行详情展示不含密钥的参数摘要与提示词。

OpenAI 和 Claude 预设默认不发送 temperature，以兼容不支持自定义温度的型号。其他预设默认温度 0.7、输出上限 1024、超时 180s；均可在页面调整。Anthropic 温度范围为 0–1，其余为 0–2。输出上限 1–32768；超时 1 秒至 30 分钟。服务端仍可能有更严格的模型限制。

## 地址和协议

| 类型 | 默认基础 URL | 协议 |
| --- | --- | --- |
| 智谱 | https://open.bigmodel.cn/api/paas/v4 | OpenAI 兼容 |
| DeepSeek | https://api.deepseek.com/v1 | OpenAI 兼容 |
| OpenAI | https://api.openai.com/v1 | Chat Completions |
| Claude | https://api.anthropic.com/v1 | Anthropic Messages |
| Ollama | http://127.0.0.1:11434/v1 | OpenAI 兼容 |
| 自定义 | 手工填写 | OpenAI 兼容或 Anthropic Messages |

URL 不包括最终的 /chat/completions 或 /messages。Ollama 地址由服务端访问：容器内的 127.0.0.1 是容器自身。检测按钮旁可选择 5s / 10s / 15s，默认 5s，选择对后续检测生效；此选项只在当前页面保留，与命名的“请求超时”独立。检测到时自动结束并提示超时，整个检测（含分页）共用所选时限、单页 1 MiB、1000 个结果，禁止跟随重定向。保留本地 / 局域网地址支持；所有操作均需登录且写入 / 检测需通过 CSRF 校验。

## 密钥与持久化

密钥不在列表、编辑响应或运行详情中返回；编辑时留空保持原值，勾选“清除已保存密钥”才删除。修改 URL、服务类型或协议时，不能把旧密钥自动转发给新目的地，需要重新输入或明确清除。

模型密钥和任务模型快照在 SQLite 中用 AES-GCM 加密。随机主密钥保存在数据库旁的 `<storage>/data.sqlite.key`（0600）。数据库目录应持久化；备份 / 迁移必须同时包含数据库和对应的 .key 文件。丢失 .key 且数据库已有加密数据时启动会明确报错，不自动生成替代密钥。

## 数据与配置入口

YAML 不再接受 server、模型名、URL、密钥、温度、输出 token、超时或 prompt 字段。不兼容、不导入旧 YAML。SQLite v4 保留此前保存的提示词 ID / 正文，并扩展为待完善模型配置；填写服务设置后才可运行。默认初始化记录没有可直接使用的模型连接。

每条配置名称最多 80 字、提示词最多 16000 字节，最多 200 条配置。编辑采用版本校验。每个视频独立请求，规则在 system，所有截图在同一条 user 素材消息；详见 [模型请求](model-request.md)。

## 思考模式

每套模型配置可选择跟随模型默认、关闭思考、开启思考，随任务配置一起快照保存。旧配置保持服务端默认，不自动关闭思考。思考仍计入输出额度；不会自动提高输出上限。开启时省略 temperature，避免思考模型拒绝采样参数。

- DeepSeek、智谱：发送 `thinking.type=enabled/disabled`，须使用支持该参数的型号。
- OpenAI：使用 `reasoning_effort`。已适配 GPT-5.1 / 5.2 / 5.4 / 5.5 的普通系列；专用 pro、codex、chat 型号和未知型号请使用默认。GPT-5 初代、o 系列仅支持开启强度控制，不能将低强度当成关闭。
- Ollama：当前 `/v1/chat/completions` 使用 `reasoning_effort`（关闭为 none，开启为 low/medium/high）。实际支持取决于 Ollama 版本与模型，非思考模型无需启用控制。
- Claude：已识别的 4.6 及后续系列用 adaptive，旧 3.7 / 4 系列用固定预算；固定预算至少 1024，且小于总输出上限。Mythos / Fable 不允许关闭；未知型号要求跟随默认。预算只对固定预算型号有效。
- 自定义：必须显式选择服务支持的参数类型。OpenAI 协议可选 thinking、reasoning_effort、本地模板 chat_template_kwargs.enable_thinking；Anthropic 协议可选自适应或固定预算。不探测、不假设服务一定支持这些参数，服务拒绝会记录在失败诊断中。

页面保存不调用真实模型验证能力；模型列表接口不能可靠提供思考开关能力。新任务使用保存后的模式，已创建任务不变。

参数依据：[DeepSeek](https://api-docs.deepseek.com/api/create-chat-completion/)、[智谱 GLM-4.6V](https://docs.bigmodel.cn/cn/guide/models/vlm/glm-4.6v)、[Ollama OpenAI 兼容接口](https://docs.ollama.com/api/openai-compatibility)、[OpenAI GPT-5.1](https://developers.openai.com/api/docs/models/gpt-5.1)、[Claude 思考配置](https://platform.claude.com/docs/en/build-with-claude/extended-thinking)。

## 并发任务数

每套模型配置保存 workers（页面名称“并发任务数”），范围 1–32，默认 2。新建手动任务与计划触发时记录创建时的并发数；排队与运行中的批次跟随原模型配置的最新并发数，无需重启或重建批次。运行中约每 500 毫秒检查更新：提高并发补充任务，降低并发等待在途任务完成后按新上限继续，不中断已有请求。切换全局默认不影响已创建批次；原模型配置删除后保持最近读取到的并发数（尚未读取则用创建快照）。模型、地址、密钥、提示词及其他请求参数始终使用批次快照。旧 YAML 的 workers 必须删除，不做配置导入。

## 复制与删除

卡片提供复制与删除按钮。复制立即创建独立副本并打开编辑，保留提示词、加密密钥、并发和思考等参数，但不复制全局默认状态；取消编辑不会撤销已创建的副本。删除前确认，带版本校验；全局默认配置和被任意计划引用的配置需先解除关联。历史与任务快照保留，不受配置删除影响。

## 测试图片识别与 Coding Plan

编辑表单的“测试图片识别”无需先保存，使用当前地址、密钥、模型、输出额度和思考设置，发送一张程序生成的随机四色块 JPEG。命名提示词替换为固定识别题，不使用媒体库文件。每次测试只调用一次、不重试，可能产生服务费用或套餐消耗。

显示实际回答、预期颜色顺序、耗时、HTTP 状态及错误码。答对只证明本次识别成功；HTTP 成功但答错显示不符合预期，不据此认定模型支持或不支持多模态。修改配置会清除过期测试结果。

智谱 Coding Plan 单独使用 `https://open.bigmodel.cn/api/coding/paas/v4`，普通智谱仍用 `/api/paas/v4`。地址依据 [智谱官方 OpenCode 配置说明](https://docs.bigmodel.cn/cn/coding-plan/tool/opencode)。套餐是否允许直接图片输入需实测；本项目不调用 Coding Plan 视觉 MCP，也不将测试结果解释为套餐权限保证。
