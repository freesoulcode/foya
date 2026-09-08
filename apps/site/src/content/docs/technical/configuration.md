---
title: 配置系统
description: 了解 Foya 的默认值、环境变量、持久配置和作用域。
slug: docs/technical/configuration
---

Foya 配置分为启动配置、应用设置和项目级文件。不同配置有不同所有者和生命周期，
不存在一个包含所有状态的单一配置文件。

## 启动配置

Kernel Config 包含：

- Transport；
- Lifecycle；
- Socket Path 或 TCP Address；
- Data Directory；
- SubAgent Limits；
- OpenTelemetry 开关和服务标识；
- 是否禁用长生命周期外部集成；
- 初始 Provider 配置。

桌面 Sidecar 使用默认本地配置。短生命周期 CLI 管理命令会禁用自动启动飞书和
Cron 等外部集成。

## 环境变量

当前支持的核心变量：

| 变量 | 用途 |
|---|---|
| `FOYA_PROVIDER_BASE_URL` | 初始 OpenAI 兼容端点 |
| `FOYA_PROVIDER_API_KEY` | 初始 API Key |
| `FOYA_PROVIDER_MODEL` | 初始 Model |
| `FOYA_PROVIDER_CONTEXT_WINDOW` | 初始上下文窗口 |
| `FOYA_LISTEN_ADDR` | TCP 服务监听地址；设置后进入持久服务模式 |
| `FOYA_SOCKET_PATH` | 本地 Unix Socket 路径 |
| `FOYA_DATA_DIR` | 数据目录 |
| `FOYA_AUTH_TOKEN_SHA256` | 远程 Bearer Token 的 SHA-256 摘要 |
| `FOYA_AUTH_TOKEN_HASH_FILE` | 保存 Token 摘要的文件 |
| `FOYA_TLS_CERT_FILE` | TLS 证书链 |
| `FOYA_TLS_KEY_FILE` | TLS 私钥 |
| `FOYA_ALLOW_PLAINTEXT` | 允许可信反向代理网络中的明文 TCP |
| `FOYA_AGENT_MAX_GLOBAL_CONCURRENCY` | Child 全局并发 |
| `FOYA_AGENT_MAX_PER_ROOT` | 单 Root 并发 |
| `FOYA_AGENT_MAX_TREE_TOKENS` | Child Tree Token 上限 |
| `FOYA_OTEL_ENABLED` | 启用 OpenTelemetry |
| `FOYA_OTEL_TRACES_ENABLED` | 总开关启用后是否导出 Trace，默认 true |
| `FOYA_OTEL_METRICS_ENABLED` | 总开关启用后是否导出 Metrics，默认 false |
| `FOYA_OTEL_CAPTURE_CONTENT` | 总开关启用后是否导出 Prompt 和结果，默认 false |
| `FOYA_OTEL_ENVIRONMENT` | 部署环境标签 |
| `OTEL_SERVICE_NAME` | OTel Service Name，默认 `foya` |
| `OTEL_EXPORTER_OTLP_*` | 标准 OTLP Endpoint、Protocol、Header 和 TLS 配置 |

`FOYA_OTEL_ENABLED` 默认为 false；其余三个变量只是子开关，不会单独启用遥测。
Feishu CLI 读取的变量详见 [CLI 参考](../cli.md#外部能力)。

环境 Provider 只用于没有 Connection Catalog 时建立初始连接。持久 Connection
创建后，Session 使用具体 Connection ID。

## 持久设置

Foya 数据目录保存：

| 文件 | 设置 |
|---|---|
| `connections.json` | 模型端点与 Model 能力 |
| `default-models.json` | 各用途默认模型 |
| `agent-limits.json` | SubAgent 调度限制 |
| `projects.json` | Project Catalog |
| `skills.json` | Skill 启用与固定状态 |
| `web-search.json` | 搜索 Provider |
| `automations.json` | Automation |
| `feishu-channels.json` | 飞书 Channel |
| `workflows.json` | Plan、Spec 与 Goal |

全局用户扩展位于 `~/.foya` 或 `~/.agents`，项目扩展位于项目目录。

## 配置优先关系

常见解析顺序：

- Session 显式设置高于全局默认模型；
- Generation Node 设置高于默认 Image/Video Model；
- Project Command 高于 Global Command；
- Project Skill 高于 Global Skill；
- Project Agent Definition 高于 User 和 Built-in；
- Project Rule 与 Global Rule 同时生效，冲突时 Project 优先。

这些优先关系由各领域 Manager 定义，不应假设所有配置都使用同一覆盖规则。

## 写入

大多数配置使用完整替换或原子临时文件 Rename。服务会在写入前验证：

- 枚举值；
- 必填字段；
- 路径；
- 数值范围；
- 引用的 Project 或 Connection；
- 重复 ID 和名称。

客户端读取 Connection、MCP 和 Search 配置时不会返回明文凭证。

## 热更新

以下配置通常在下一次操作生效，无需重启：

- Connection 与默认模型；
- Session Model 和 Approval Mode；
- Rules、Memory、Skills；
- Hooks 和 Commands；
- MCP；
- Web Search；
- Automation 和 Channel。

OpenTelemetry Provider 在 Kernel 启动时创建，修改相关环境变量后需要重启。

Connection Catalog 更新会重建 Provider Client，并清除 Session 的内存 Context
预算基线。

## 当前边界

- 配置分布在 SQLite、JSON 和 Markdown 中。
- 没有统一 Schema Version 与迁移框架。
- 文件之间不具备跨文件事务。
- API Key 当前仍保存在用户私有文件。
- 远程内核连接已支持 SSH 和 HTTPS，但不提供多租户配置作用域。
