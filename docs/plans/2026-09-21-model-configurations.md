# 模型配置与任务历史实施计划

目标：将模型连接、参数和提示词统一放入页面 / SQLite；YAML 不再接受模型配置。任务历史按目录、旧文件名、新文件名分列。

架构：沿用 Go / Vue / SQLite。模型配置归属 internal/models 与 web/src/features/models，视觉协议归属 internal/vision，部署 YAML 仅保存服务、媒体与抽帧配置。保留已有提示词 ID / 正文并扩展为待完善的模型配置，不导入旧 YAML。

1. 数据与配置：SQLite v4 升级 prompts 为 model_configs，任务 / 计划引用同步改名；新增模型参数与加密密钥，任务保存不可变模型快照。删除 YAML server 入口及其示例、兼容逻辑。
2. 模型服务：六个 URL 预设（智谱、DeepSeek、OpenAI、Claude、自定义、Ollama），HTTP(S) 地址校验；模型列表检测有超时 / 响应上限、禁重定向，保存密钥不回传。修改 URL / 服务类型不能复用旧密钥。
3. 视觉请求：OpenAI 兼容请求与 Anthropic Messages 分别组装；保持独立视频上下文、固定规则在 system、所有图片在一条 user 素材消息。重试复用请求体。
4. 页面：模型配置列表、默认圆点、新建 / 编辑表单、模型检测选择和手工模型名；运行与计划绑定整套配置。任务历史旧 / 新文件名截断和悬停显示，操作分色。
5. 验证：部署配置拒绝旧字段、数据迁移、密钥不回传 / 重定向、参数快照、Claude 图文结构、检测结果 / 错误、页面编辑默认选择 / 历史列；生产构建、Go race / vet、浏览器回归、Linux 单文件入口构建。

接口参考：OpenAI Models、Anthropic Messages / Models、Ollama OpenAI compatibility、智谱模型文档与 DeepSeek API 文档。只使用隔离测试服务验证，不调用用户真实云端模型。
