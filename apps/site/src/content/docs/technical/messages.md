---
title: 消息模型
description: 了解 User、Assistant、Tool 消息以及附件和展示字段的边界。
slug: docs/technical/messages
---

Message 是 Foya 会话历史的基本内容单位。它同时服务于持久化历史、客户端展示和
Provider 请求，但不同字段并不都会发送给模型。

## 消息角色

| Role | 含义 |
|---|---|
| `user` | 用户提交的任务或补充信息 |
| `assistant` | 模型文本、推理展示和 Tool Call |
| `tool` | 一次 Tool Call 的执行结果 |
| `system` | 内核生成的持久上下文，例如 Hook Context |

运行时临时系统提示词不会作为普通 Message 写入 Session History。

## User Input

标准 User Input 可以包含：

- Text；
- Command 标识；
- Attachment 引用；
- Browser Element。

Kernel Service 会在创建 Turn 或 Queue Item 前校验结构化输入。空文本只有在同时存在附件
或浏览器元素时才允许提交。

### Attachment

Attachment 保存 ID、名称、类型、Media Type、大小、尺寸和 SHA-256。二进制内容
位于 Artifact Store，不进入 Message JSON。

### Browser Element

Browser Element 保存页面 URL、标题、Tag、Selector、文本和有限 HTML。Provider
视图会把它标记为用户选择的不可信页面数据。

## Assistant Message

Assistant Message 可以同时包含：

- 面向用户的 Text；
- 仅供展示的 Reasoning；
- 一个或多个 Tool Call；
- Turn 开始和完成时间；
- Turn Status 与 Reason。

如果响应包含 Tool Call，它不是 Turn 的最终回答。工具执行完成后，Runtime 会继续
使用同一个 Turn 进行下一次模型请求。

## Tool Call

Tool Call 包含：

```text
ID + Name + JSON Input
```

ID 用于将 Assistant 请求与后续 Tool Message 关联。参数在 Provider 流中可能分片
到达，Engine 会按 Tool Index 组装，完成后再执行。

## Tool Message

Tool Message 使用 `tool_call_id` 指向对应调用，并保存：

- 模型可见结果文本；
- 可选 Attachment；
- 可选 File Change；
- 仅供 UI 展示的 Diff。

工具失败也会形成 Tool Message。模型可以读取错误并尝试修正。

## Provider 视图

持久 Message 在发送前转换为 Provider Input Message。

以下内容会发送：

- Role；
- Model-visible Content；
- Tool Calls；
- Tool Call ID；
- 请求时加载的图片 Part。

以下内容不会作为普通文本发送：

- Event Seq；
- 普通完成回合的 Reasoning；
- UI Diff；
- Turn 时间；
- 文件恢复 Metadata；
- Artifact 存储路径。

这样可以让持久模型保留审查和恢复信息，同时控制模型上下文大小。

## 取消消息

如果 Assistant 在取消前已经产生 Text 或 Reasoning，Foya 会保存部分消息。

普通已完成 Reasoning 不会再次回灌模型；取消消息中的 Reasoning 会加上明确的中断
标记，作为后续用户纠正的上下文。这个例外避免丢失已经执行到一半的重要分析。

没有内容、没有 Tool Call 且对模型无有效信息的空 Assistant Message 会从 Provider
视图中跳过。

## Event Seq

Message 从 Event Store 投影出来时携带 `event_seq`。它用于：

- 稳定标识消息；
- 选择历史回退边界；
- 选择 SubAgent 上下文；
- 引用大型 Tool Result。

Event Seq 不发送给普通 Provider Message，除非 Foya 显式构造工具结果引用。

## UI Segment

流式界面需要保留一次 Assistant 回合内的交错顺序，例如：

```text
Reasoning
Text
Tool Call
Reasoning
Text
```

Vue 客户端将这些内容组织成 UI Segment。Segment 是展示投影，不改变 Kernel 中
Assistant Message 与 Tool Message 的规范结构。

## 消息与事件

完整消息通过 `message_end` 持久化。从分支导入的消息使用
`message_imported`。

`message_delta` 和 `reasoning_delta` 只用于实时流。客户端不能把 Delta 当成最终
消息写回内核。

历史回退通过 Projection 将旧消息标记为非活动，而不改写原 Message Payload。

## 大型结果

Tool Message 的完整文本保存在 Canonical History。进入模型上下文时，超大结果
可以被转换为头尾摘录和可恢复引用。

这个转换只影响 Provider Projection，不修改 Message 本身。详情参见
[上下文压缩](./compaction.md)。

## 当前边界

- 普通对话附件当前主要支持图片。
- Browser Element 数量和总大小受到限制。
- UI Segment 不是公开持久协议。
- Reasoning 主要用于展示，不作为常规后续模型输入。
- Message Schema 与 Provider SDK 类型保持解耦。
