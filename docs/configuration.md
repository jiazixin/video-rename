# Web 服务配置

完整可复制示例在 [configs/config.example.yaml](../configs/config.example.yaml)。所有 YAML 配置路径相对于配置文件所在目录；默认读取 `configs/config.local.yaml`，也可通过 `--config/-f` 显式指定。不读取环境变量覆盖，未知字段、重复 key 和非法媒体参数报错。

| 字段 | 默认 / 要求 | 作用 |
| --- | --- | --- |
| listen | 127.0.0.1，IP 地址 | HTTP 监听地址，局域网可配置 0.0.0.0 |
| port | 8790，1–65535 | HTTP 端口 |
| path | 必填，已存在的真实目录 | 媒体根目录，不能通过 HTTP 越界访问 |
| storage | ../data | 数据目录，自动创建，必须在媒体根目录外；内部数据库文件名固定 |
| auth.username | 必填 | 管理员账号 |
| auth.password | 非空，拒绝占位密码，无最短长度限制 | 管理员密码，仅后端读取，不写入数据库或 API 响应 |
| sampling.strategy | hybrid | hybrid / size / duration |
| sampling.max_frames | 12，1–32 | 每个视频最大采样帧数 |
| sampling.skip_start_ratio / skip_end_ratio | 各 0.05 | 跳过首尾比例，和必须小于 1 |
| sampling.duration_rules / size_rules | 现有默认分档 | 显式填写则整表替换；最后一档为 null 兜底 |
| images.max_dimension | 768，128–2048 | JPEG 最长边，不放大小图 |
| images.jpeg_quality | 4，2–15 | FFmpeg JPEG 质量 |

`logging` 已退出 Web 服务协议。成功 / 失败记录、操作历史和处理标记统一使用 SQLite。已有内部流水线测试仍使用旧日志结构，它不是 Web 服务的配置入口。

模型协议、图片预算、有限重试、标题清洗和 FFmpeg 调用规则沿用 [媒体处理](processing.md)。工具路径从 PATH 查找，不通过 YAML 拼接命令。

监听、根目录、认证和数据库配置变更需要重启。模型 URL、密钥、模型名、温度、输出上限和超时全部在“模型配置”页面保存；YAML 不支持 server，不兼容、不导入旧字段。计划规则和目标目录在页面管理并持久化到 SQLite，不同步写回 YAML。

并发任务数在模型配置页面保存（1–32，默认 2），YAML 不再支持 workers。保存后并发数同步应用于该模型配置的排队和运行中批次，其他请求参数保留原快照。


`storage` 省略时使用 `../data`（相对于配置文件目录）；项目默认配置位于 `configs/`，因此数据落在项目根目录的 `data/`。填写自定义位置时只提供目录，例如 `storage: "/srv/video-rename/data"`。程序在该目录中管理 `data.sqlite`、密钥、锁和 SQLite 辅助文件，备份时保留整个目录。

