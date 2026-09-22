# 模型请求排查（2026-09-21）

## 当前结构

- system.content 为所选提示词快照 + 上述固定输出格式要求。
- user.content 首段仅说明“以下截图来自同一个视频，按时间顺序排列。”，然后交替排列截图编号 / 时间与图片。
- 每个视频始终独立创建这两条消息。重试发送原请求体，不添加上次回答，不累计其他视频历史。
- 10 张图仍为 user.content 中的 21 个 part；若序列化请求超过预算，原有均匀删帧策略仍生效。
- 模型参数来自页面所选配置的不可变快照。新建运行的详情中可查看参数快照，旧任务没有记录的字段显示“未记录”。不保存图片 base64；API 不显示密钥，执行快照中的密钥加密保存。

请求结构示例（为便于阅读，仅展示两帧，图片值为占位符）：

```json
{
  "model": "configured-model",
  "messages": [
    {"role": "system", "content": "所选提示词\n\n请只输出一行标题文本作为最终回答,不要输出解释、编号、引号或代码围栏。"},
    {"role": "user", "content": [
      {"type": "text", "text": "以下截图来自同一个视频，按时间顺序排列。"},
      {"type": "text", "text": "第 1/2 张截图,时间点 00:01.000。"},
      {"type": "image_url", "image_url": {"url": "data:image/jpeg;base64,<frame-1>"}},
      {"type": "text", "text": "第 2/2 张截图,时间点 00:02.000。"},
      {"type": "image_url", "image_url": {"url": "data:image/jpeg;base64,<frame-2>"}}
    ]}
  ],
  "temperature": 0.2,
  "max_tokens": 128,
  "stream": false
}
```

## 创作标题提示词示例

新库默认提示词已更新为以下内容。已有提示词保留原文；需要使用时可在页面新增并设为默认，已有计划需单独选择。

> 你是一位视频标题编辑。结合整段视频的连续截图，为它创作一个自然、流畅、有辨识度的中文标题。抓住最有特点的情境或瞬间，将内容组织成完整表达，不要逐帧描述画面，也不要用名词、动作和场景堆砌关键词。可以有适度的表达创意，但不要编造人物身份、地点、关系或画面之外的事件。只输出一个标题，不加解释、引号或文件扩展名。

真实标题质量需用相同视频、模型与参数比较后判断；请求结构正确不等于创作质量已验证。

## 服务协议

- OpenAI 预设使用 `max_completion_tokens`；其他 OpenAI 兼容服务使用 `max_tokens`。勾选模型默认温度时不发送 temperature。
- Claude 使用 POST /messages、x-api-key 与 anthropic-version: 2023-06-01；system 是顶层字段，messages 仅有一条 user。JPEG 以 image/source/base64 内容块发送；max_tokens 截断、tool_use 等非正常标题响应不执行改名。
- 模型列表检测使用 GET /models；Anthropic 列表按 after_id 分页。不跟随重定向，错误不透传上游响应中的密钥。

接口依据（2026-09-21）：[OpenAI Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)、[OpenAI Models](https://developers.openai.com/api/reference/resources/models/methods/list)、[Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create)、[Anthropic Models](https://platform.claude.com/docs/en/api/models/list)、[Ollama 兼容接口](https://docs.ollama.com/api/openai-compatibility)、[DeepSeek Models](https://api-docs.deepseek.com/api/list-models/)、[智谱模型文档](https://docs.bigmodel.cn/cn/guide/start/model-overview)。
