---
title: 技术文档总览
description: Foya 技术架构、运行时、扩展能力和数据系统的完整阅读地图。
slug: docs/technical
---

本节说明 Foya 当前已经实现的技术系统。文档面向希望理解、评估或参与 Foya 的
读者，不要求预先阅读源码。

除本页作为导航外，每篇文档只聚焦一个主题。

## 建议阅读顺序

第一次了解 Foya 的实现，建议依次阅读：

1. [系统架构](./architecture.md)
2. [Kernel 与 Service](./kernel-service.md)
3. [Agent Runtime](./agent-runtime.md)
4. [消息模型](./messages.md)
5. [Session](./session.md)
6. [事件存储](./event-store.md)
7. [上下文](./context.md)
8. [上下文压缩](./compaction.md)
9. [工具系统](./tools.md)
10. [权限系统](./permissions.md)
11. [数据存储](./storage.md)

## 系统边界

| 主题 | 文档 |
|---|---|
| 整体分层和数据流 | [系统架构](./architecture.md) |
| 服务装配与业务协调 | [Kernel 与 Service](./kernel-service.md) |
| REST、SSE、Unix Socket 与远程连接 | [REST 与事件流](./transport.md) |
| SSH、TLS 与服务端运维 | [远程内核](../server-deployment.md) |
| Vue、Tauri 与 Sidecar | [桌面运行时](./desktop.md) |
| 启动、关闭和中断恢复 | [进程生命周期与恢复](./lifecycle.md) |
| 配置来源和覆盖关系 | [配置系统](./configuration.md) |
| 信任和风险边界 | [安全模型](./security.md) |

## Agent Runtime

| 主题 | 文档 |
|---|---|
| 模型与工具循环 | [Agent Runtime](./agent-runtime.md) |
| User、Assistant 和 Tool 数据 | [消息模型](./messages.md) |
| 会话、队列、分支和回退 | [Session](./session.md) |
| Child Session 与任务树 | [子 Agent](../subagents.md) |
| Project 与工作目录 | [Project](../projects.md) |
| 模型连接和能力配置 | [模型连接](../model-connections.md) |

## 上下文与知识

| 主题 | 文档 |
|---|---|
| 单次模型请求输入 | [上下文](./context.md) |
| 长历史压缩与恢复 | [上下文压缩](./compaction.md) |
| 跨会话事实 | [记忆](./memory.md) |
| 行为约束 | [规则](./rules.md) |

Context、Memory 和 Rule 是三个独立机制，不应互相替代。

## 执行系统

| 主题 | 文档 |
|---|---|
| Tool 注册、发现和执行 | [工具系统](./tools.md) |
| Approval 与 Sandbox | [权限系统](./permissions.md) |
| Agent 生命周期回调 | [Hooks](./hooks.md) |
| 用户显式 Prompt 入口 | [Commands](./commands.md) |
| Plan、Spec 和 Goal 状态 | [Workflows](./workflows.md) |
| 文件变更与撤销 | [文件审查](../file-review.md) |
| PTY 与后台进程 | [Terminal](./terminal.md) |
| WebView 自动化 | [Browser Runtime](./browser.md) |
| 搜索路由与网页抓取 | [Web Search](./web-search.md) |

## 扩展与集成

| 主题 | 文档 |
|---|---|
| 可复用指令包 | [Skills](../skills.md) |
| 外部 Tool、Resource 和 Prompt | [MCP](../mcp.md) |
| 模型协议与 Route | [Provider 与模型路由](./providers.md) |
| 定时 Agent 任务 | [自动化](../automation.md) |
| 外部聊天平台 | [消息渠道](../channels.md) |

## 视觉与媒体

| 主题 | 文档 |
|---|---|
| Session 二进制附件 | [Artifact](./artifacts.md) |
| 持久节点式视觉工作区 | [Canvas](./canvas.md) |
| 图片和视频生成 | [媒体生成](./media-generation.md) |

## 数据与观测

| 主题 | 文档 |
|---|---|
| Canonical Event 与 Projection | [事件存储](./event-store.md) |
| SQLite 与文件布局 | [数据存储](./storage.md) |
| Token 与活跃度统计 | [Usage 统计](./usage.md) |
| Trace、Metrics 与 OTLP 导出 | [OpenTelemetry](./telemetry.md) |

## 文档约定

技术文档统一使用以下术语：

- **Session**：长期会话身份。
- **Turn**：一次用户输入触发的完整运行。
- **Step**：Turn 中的一次模型请求。
- **Event**：已经发生并可持久化的事实。
- **Projection**：从事实派生的读取视图。
- **Provider**：模型协议适配器。
- **Tool**：模型可以请求的执行单元。
- **Route**：Connection 与 Model 的稳定运行身份。

“当前边界”描述现有实现限制，不代表长期 Roadmap。
