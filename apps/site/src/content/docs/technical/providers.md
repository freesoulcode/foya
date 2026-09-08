---
title: Provider 与模型路由
description: 了解 Foya 如何管理模型连接、统一请求协议并隔离不同模型的运行状态。
slug: docs/technical/providers
---

Provider 是 Foya 与模型服务之间的协议适配层。Connection 保存用户配置的端点和
能力，Session 通过 Connection ID 与 Model 选择实际调用目标。

这种分离让多个 Session 可以同时使用不同服务，同时让 Agent Runtime 保持统一
调用方式。

## 三个概念

| 概念 | 含义 |
|---|---|
| Connection | 一个用户配置的账号或服务端点 |
| Model | Connection 中可选的具体模型 |
| Provider | 把 Foya 请求转换成该类服务协议的运行实现 |

Session 持久化 `connection_id` 和 `model`。修改全局默认模型不会自动改变已有
Session。

## Connection 类型

Foya 使用不同 Connection Type 区分用途：

| 类型 | 用途 |
|---|---|
| `language` | 对话、工具调用、标题、Guardian 和文本压缩 |
| `image` | Canvas 图片生成 |
| `video` | Canvas 视频生成 |

普通 Session 必须绑定 Language Connection。Image 和 Video Connection 不会被
当成聊天模型使用。

当前语言模型 Provider 以 OpenAI 兼容协议为主。Video Connection 还需要指定对应
生成协议。

## Connection 配置

一个 Connection 主要包含：

- 稳定 ID 和显示名称；
- Type 和 Provider Kind；
- Base URL；
- 认证方式和 API Key；
- 模型列表；
- 每个模型的能力设置；
- 界面排序。

当前认证方式只支持 `api_key`。空 API Key 可以用于不要求鉴权的本地兼容端点。

Connection 的顺序决定没有显式默认设置时，新 Session 优先选择哪个 Language
Connection。

## 模型能力

每个模型可以配置：

| 能力 | 影响 |
|---|---|
| Context Window | 上下文预算高水位 |
| Max Input Tokens | 更严格的输入上限 |
| Max Output Tokens | Provider 请求和压缩输出上限 |
| Image Input | 是否接受图片消息 |
| Tool Calling | 是否适合 Agent 工具循环 |
| Reasoning Effort | 可选择的推理强度 |
| Web Search | 是否支持 Provider 原生搜索 |
| Generation Flags | 是否可用于图片、视频或音频生成 |

模型列表端点通常不能提供所有能力。Foya 因此允许保存用户配置的能力声明。

当模型明确配置为不支持图片输入时，带图片的用户输入会在请求前被拒绝。没有能力
声明表示未知，不等同于明确不支持。

## 默认模型

默认模型按用途分别保存：

- Language：新 Session 的默认聊天模型；
- Fast：标题生成等短任务；
- Image：Canvas 默认图片模型；
- Video：Canvas 默认视频模型。

每个默认值都是 Connection ID 与 Model 的组合。Connection 被删除或模型不再存在
时，对应默认值会被清理。

## Provider 接口

所有语言 Provider 至少实现流式请求：

```text
Request
  → StreamEvent(text / reasoning / tool call / usage / done / error)
```

统一 Request 包含：

- Model；
- Reasoning Effort；
- Max Output Tokens；
- 已物化的消息；
- Tool Schema；
- 可选 Provider Context State。

Provider 将自己的协议响应转换成统一事件。Agent Runtime 不直接解析某个厂商的
HTTP 流格式。

## 可选能力

Provider 可以按需实现：

| 能力 | 用途 |
|---|---|
| Model Lister | 从端点读取模型列表 |
| Capability Resolver | 补充模型输入能力 |
| Completer | 标题和 Memory 等短文本任务 |
| Detailed Completer | 返回 Finish Reason 和 Usage，用于文本压缩 |
| Native Web Searcher | 使用 Provider 自己的搜索能力 |
| Native Context Compactor | 创建和重放 Provider 原生上下文状态 |

Agent Runtime 会在运行时检查具体 Provider 是否支持能力。不支持时使用明确的
Fallback 或返回不可用错误，不假定所有 OpenAI 兼容端点都支持相同扩展。

## OpenAI 兼容实现

内置语言 Provider 使用 OpenAI Chat Completions：

- 支持流式文本；
- 支持 Function Tool Call；
- 解析 Provider 返回的 Usage；
- 读取兼容端点常见的 `reasoning_content` 扩展；
- 支持文本与图片输入；
- 支持非流式 Completion。

Provider 原生 Web Search 使用 Responses API。兼容端点未实现 Responses 时，
调用方可以回退到已配置的外部 Web Search。

当前 Chat Completions 请求不会消费 Provider-native Compaction State，因此内置
Provider 的上下文压缩使用 Portable Text Checkpoint。

## 消息物化

Canonical Message 与 Provider Message 分离。

发送前，Foya 会：

1. 移除不应回灌模型的 UI 字段，例如 Diff 和普通推理展示；
2. 将 Assistant Tool Call 转为协议函数调用；
3. 将 Tool Message 与 Tool Call ID 关联；
4. 从 Artifact Store 加载允许发送的图片；
5. 把浏览器元素标记为不可信参考数据；
6. 跳过对模型没有有效内容的空 Assistant 消息。

这保证持久化模型不需要与某个 Provider SDK 的消息结构完全一致。

## Route

Provider Route 是模型运行状态的隔离标识。它由以下信息派生：

- Connection ID；
- Connection Type；
- Provider Kind；
- Auth Kind；
- Base URL；
- Model。

API Key 不进入 Route 摘要。

Route 用于隔离：

- 上一次真实输入和输出 Token；
- 请求 Payload 基线；
- Accepted Boundary；
- Provider-native Context State。

只使用 Model 名称不足以建立隔离，因为两个 Connection 可以提供同名模型但拥有
不同端点、上下文窗口或协议行为。

更新 Connection Catalog 后，Foya 会重建 Provider Client，并清除现有 Session 的
内存预算基线。持久 Accepted Boundary 仍按 Route 校验，不会错误复用到新端点。

## Usage

Provider 可以在流中返回：

- Input Tokens；
- Output Tokens；
- Total Tokens；
- Cached Tokens；
- 实际 Model。

Cached Tokens 是 Input Tokens 的子集，仍占用上下文窗口。

Usage 会写入 Session 事件与统计表，并用于下一次上下文估算。压缩模型请求产生的
Usage 也会记录，避免隐藏后台 Token 消耗。

若 Provider 不返回 Usage，Agent 仍可继续运行，但上下文预算只能使用本地估算。

## 错误处理

同步请求初始化失败可以直接返回错误。流启动后发生的问题通过统一 Error Event
传给 Runtime。

Runtime 只对明确的输入上下文溢出执行一次压缩恢复。以下错误不会触发压缩：

- Rate Limit；
- Quota；
- Throttling；
- Output Token Limit；
- 普通网络或认证错误。

这避免把所有 Provider 错误都错误解释为“历史太长”。

## Connection 生命周期

新增或修改 Connection 时，Kernel Service 会校验：

- Connection Type；
- Auth Kind；
- Video Protocol；
- Token 上限不能为负；
- 输入或输出上限不能超过已知 Context Window。

删除 Connection 时，使用它的 Session 会解除绑定。Session 历史仍然保留，但在
重新选择有效 Connection 和 Model 前无法继续调用语言模型。

## 凭证边界

Foya 使用用户自己的 API Key 直接连接配置的服务。读取 Connection 列表的客户端
接口不会返回明文 API Key，只返回是否已经配置。

当前凭证仍保存在用户私有数据目录的 `connections.json`，依赖目录与文件权限
保护，尚未迁移到操作系统 Keychain。备份或共享数据目录时必须将其视为敏感数据。

## 当前边界

- 内置 Language Provider 只有 OpenAI 兼容实现。
- 当前认证方式只有 API Key。
- OpenAI 兼容端点的扩展字段和能力可能不同，需要显式配置。
- Provider-native Compaction 接口已经定义，但内置 Provider 尚未实现。
- Connection 配置更新会重建 Client，不迁移进行中的模型请求。
