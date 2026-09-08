---
title: Foya 文档
description: Foya 的使用指南、核心机制与技术设计文档。
slug: docs
---

Foya 是一个开源、本地优先、支持自带模型服务的个人 Agent 系统。它能够在用户授权
的边界内读取项目、调用工具、修改文件并执行长任务。

文档分为使用指南和技术文档。使用指南说明怎样操作 Foya；技术文档说明系统为什么
这样设计、运行时发生了什么，以及当前实现边界。

## 开始使用

第一次运行 Foya，建议按以下顺序阅读：

1. [快速开始](./quick-start.md)：准备开发环境并启动桌面端。
2. [模型连接](./model-connections.md)：连接自己的语言模型服务。
3. [Project](./projects.md)：理解工作目录与项目级配置。
4. [Session](./technical/session.md)：了解会话、队列和历史。
5. [远程部署](./server-deployment.md)：通过 SSH 或 HTTPS 使用远程内核。

## 日常使用

- [文件审查](./file-review.md)：检查、保留或撤销 Agent 修改。
- [子 Agent](./subagents.md)：把独立任务委派给 Child Session。
- [自动化](./automation.md)：按计划运行 Agent 任务。
- [消息渠道](./channels.md)：通过飞书连接同一个 Foya 内核。

## 扩展能力

- [插件](./plugins.md)：安装符合 Agent Plugins 1.0.0 的 Skills 与 MCP 扩展包。
- [Skills](./skills.md)：提供可复用任务流程和配套资源。
- [MCP](./mcp.md)：接入外部 Tool、Resource 和 Prompt。
- [规则](./technical/rules.md)：定义 Agent 应遵守的行为约束。
- [记忆](./technical/memory.md)：保存跨会话事实和用户偏好。

## 技术文档

从[技术文档总览](./technical/index.md)开始，可以按系统运行顺序阅读，也可以按
子系统查找。

当前技术文档覆盖：

- Kernel、Kernel Service、桌面运行时和 REST/SSE；
- Agent Loop、消息、Session、SubAgent 和 Project；
- Context、Compaction、Memory 和 Rules；
- Tools、Permissions、Hooks、Commands 和 Workflows；
- Terminal、Browser 和 Web Search；
- Provider、Skills、MCP、Automation 和 Channel；
- Artifact、Canvas 和媒体生成；
- Event Store、SQLite、Usage、配置、安全与生命周期。

每篇技术文档聚焦一个主题，并统一说明：

- 该机制解决的问题；
- Foya 当前采用的设计；
- 关键数据流和生命周期；
- 失败与恢复行为；
- 当前已经实现的边界。

## 参考

- [CLI 参考](./cli.md)
- [GitHub 仓库](https://github.com/freesoulcode/foya)

## 当前产品边界

- 桌面端以 Tauri、Vue 和 Go Sidecar 组成。
- 本地桌面通信主要使用 Unix Domain Socket。
- 语言模型通过 OpenAI 兼容接口接入。
- macOS 和 Linux 具备主要受限执行能力。
- Windows 桌面传输和受限执行仍未形成完整支持。
- SSH 自动部署和单租户 HTTPS 远程访问已经可用。
- 多租户授权和系统 Keychain 凭证存储尚未完成。

文档只描述当前可验证实现。规划中的能力会明确标记，不作为现有功能介绍。
