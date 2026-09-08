---
title: 消息渠道
description: 了解外部聊天平台如何连接 Foya，并复用同一个 Agent Runtime。
slug: docs/channels
---

Channel 将外部聊天平台接入 Foya。外部消息不会进入另一套 Bot Runtime，而是通过
Kernel Service 创建或继续 Session，再使用标准 Agent Loop、Tool 和权限系统。

当前内置消息渠道为飞书。

## 工作方式

```mermaid
flowchart LR
    USER["飞书用户或群聊"] --> CHANNEL["Feishu Channel"]
    CHANNEL --> MAP["Chat → Session 映射"]
    MAP --> BACKEND["Foya Kernel Service"]
    BACKEND --> AGENT["Agent Runtime"]
    AGENT --> EVENTS["Session Events"]
    EVENTS --> CHANNEL
    CHANNEL --> USER
```

Channel Adapter 负责平台协议，Kernel Service 负责 Agent 行为。桌面端和飞书因此可以
观察同一个 Session。

## 飞书连接

飞书 Channel 使用长连接接收事件，不要求部署公网 Callback URL。

配置需要：

- App ID；
- App Secret；
- 默认 Connection 和 Model；
- 可选 Project；
- Approval Mode（`auto` 或 `full_access`）；
- 允许的 User 和 Chat；
- 是否启用。

应用需要在飞书开发者后台启用机器人，并订阅消息事件。

## 访问控制

外部渠道直接连接可以读取项目并调用工具的 Agent，因此必须限制消息来源。

支持：

- User Allowlist；
- Chat Allowlist；
- 隔离测试环境下显式允许全部来源。

群聊默认只响应提及 Bot 的消息。公开或生产应用不应使用 Allow All。

Allowlist 只控制谁可以提交消息，不会替代 Tool Approval 和 Sandbox。

## Session 映射

Channel 为外部 Chat 保存对应 Foya Session ID。同一个 Chat 的后续消息会继续使用
原 Session，因此可以继承历史、Project、Model 和上下文。

映射持久化在 Channel 自己的数据文件中。发送 `/new` 可以为当前 Chat 建立新的
Session，发送 `/stop` 可以取消当前运行。

Channel Session 与桌面创建的 Session 使用相同数据模型，可以从桌面端查看。

## 消息输入

飞书 Channel 支持文本和图片消息。收到消息后：

1. 验证来源是否允许；
2. 解析平台消息；
3. 创建或查找 Chat 对应 Session；
4. 将图片存入该 Session 的 Artifact Store；
5. 通过 Kernel Service 提交标准 User Input。

附件在进入 Agent Runtime 前会执行与桌面输入相同的类型、大小和模型能力检查。

## 回复

Channel 订阅 Session Event，并在 Turn 完成后发送 Markdown 回复。

长回复会根据平台限制拆分。工具过程和推理增量不会原样转发为大量聊天消息，最终
结果仍保存在 Foya Session 历史中。

## 交互限制

飞书 Channel 面向无人值守或异步消息处理，只接受 `auto` 和 `full_access`，默认
使用 `auto`。它不接受 `manual`。

如果运行时仍产生未处理的 Approval Request，Channel 会拒绝该请求；如果 Agent
请求交互式提问，Channel 会取消提问。需要人工审批或补充输入时，应在桌面端新建
Manual Session 继续处理，而不是让飞书任务保持等待。

## 启动

可以通过桌面设置保存并启用飞书 Channel，也可以使用 CLI：

```bash
export FOYA_FEISHU_APP_ID=cli_xxx
export FOYA_FEISHU_APP_SECRET=xxx
export FOYA_FEISHU_ALLOWED_USERS=ou_xxx

foya bot
```

`foya bot` 会同时启动 Foya 内核服务和飞书长连接。不要再启动另一个使用相同数据
目录的 Foya 进程。

## 故障与重启

Channel 配置和 Chat 映射会持久化。内核重启后重新建立长连接。

重启不会恢复：

- 正在生成的 Provider Stream；
- 等待中的 Tool Approval；
- 内存消息队列；
- 尚未完成的外部平台发送操作。

已经写入 Foya Event Store 的完整消息仍可恢复。

## 安全建议

1. 使用专门的飞书应用。
2. 只开放必要权限。
3. 始终配置 User 或 Chat Allowlist。
4. 为 Channel 使用权限受限的 Project。
5. 避免默认启用 Full Access。
6. 不要在群聊回复中泄露本地路径、Secret 或完整 Tool Result。

## 当前边界

- 当前内置 Channel 只有飞书。
- 飞书使用长连接，不提供通用 Webhook Channel。
- Chat 与 Session 是一对一持久映射，除非用户显式新建会话。
- 平台消息投递可能重试，外部副作用任务仍需具备幂等性。
- Channel 不提供比桌面端更高的 Agent 权限。
