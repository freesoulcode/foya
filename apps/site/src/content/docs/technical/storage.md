---
title: 数据存储
description: 了解 Foya 的本地数据目录、SQLite Schema、文件存储和恢复边界。
slug: docs/technical/storage
---

Foya 默认将运行数据保存在当前操作系统用户配置目录下的 `foya` 子目录。数据不会
因为使用桌面界面而交给单独的云端服务；模型请求只发送到用户配置的 Provider。

本文说明持久化布局和一致性边界。事件语义参见[事件存储](./event-store.md)。

## 数据目录

默认数据目录由操作系统的用户配置目录决定：

```text
<user-config-dir>/foya/
```

桌面内核的 Unix Domain Socket 也位于该目录。目录创建权限为仅当前用户可访问，
SQLite 和敏感配置文件使用用户私有权限。

另有一组用户可编辑配置位于：

```text
~/.foya/
```

Project 自身还可以包含：

```text
<project>/.foya/
```

三者职责不同：

| 位置 | 主要内容 |
|---|---|
| Foya 数据目录 | 数据库、Connection、Artifact 和运行状态 |
| `~/.foya` | 全局 Rules、Memory、Skills、Hooks、Commands、MCP |
| `<project>/.foya` | 项目级 Rules、Skills、Hooks 和 Commands |

## SQLite

共享数据库文件名为：

```text
foya.db
```

数据库打开时使用：

- Foreign Keys；
- WAL Journal；
- `synchronous=FULL`；
- 5 秒 Busy Timeout；
- Incremental Auto Vacuum；
- 单个活动数据库连接。

单连接配置牺牲部分并行 SQL 吞吐，换取桌面场景中更明确的事务顺序。

## 表结构

### Session 与 Event

| 表 | 内容 |
|---|---|
| `sessions` | Session 元数据、模型绑定、Project、状态和 Child 信息 |
| `events` | 持久化会话事件 |
| `message_projection` | 当前有效消息索引 |
| `stream_snapshots` | 可替换的流状态快照 |
| `deleted_sessions` | 已删除 Session 防迟到写入标记 |
| `cleanup_jobs` | 延迟清理任务 |

`events.seq` 是数据库级自增序号。它在所有 Session 间单调增加，不是每个 Session
独立从 1 开始。

### 上下文

| 表 | 内容 |
|---|---|
| `compaction_checkpoints` | 每个 Session 当前有效 Checkpoint 投影 |
| `context_accepted_boundaries` | 每个 Session 和 Route 最近成功请求边界 |

Checkpoint 的完成事件仍保存在 `events`。专用表是读取投影，可以在损坏时从事件
恢复。

### 文件变化

| 表 | 内容 |
|---|---|
| `file_blobs` | 按 SHA-256 去重并使用 Gzip 保存的文件内容 |
| `file_changes` | Tool 修改前后的 Blob 引用和审查状态 |
| `file_rewind_journals` | 文件回退事务状态 |
| `file_rewind_journal_files` | 每次回退涉及的文件快照 |

事件 Payload 只保留 Blob Hash，不重复保存完整文件内容。

### Usage

| 表 | 内容 |
|---|---|
| `usage_records` | Session 级模型请求明细 |
| `usage_daily_ledger` | 按日期和模型聚合的 Token |
| `usage_message_daily_ledger` | 按日期聚合的消息数 |
| `usage_session_days` | 活跃 Session 日期集合 |

删除 Session 时，明细可以删除，已经形成的日 Ledger 仍用于总体统计。

## 事件与投影事务

Conversation Store 会在一个事务中追加 Event 并更新对应 Projection。例如：

- `message_end` 与消息有效索引；
- `usage_updated` 与 Usage 明细、Ledger；
- `compaction_completed` 与 Checkpoint 投影；
- `context_request_accepted` 与 Accepted Boundary；
- 文件 Tool Message 与 File Change、Blob。

事务提交失败时，两部分都不会生效。

跨 SQLite 与外部文件系统的操作无法共享同一个数据库事务，因此文件回退使用
持久 Journal 和状态令牌额外保护。

## 文件 Blob

文件工具修改文本文件时，会计算修改前后内容的 SHA-256。内容以 Hash 作为键保存，
相同内容只需存储一次。

Blob 使用 Gzip 压缩。File Change 记录：

- 绝对路径；
- 修改前是否存在；
- 修改前后文件模式；
- 修改前后 Blob Hash；
- 审查状态。

保存 Event 前，Conversation Store 会验证必要 Blob 确实存在。原始内容随后从 Event
Payload 移除，避免同一数据同时出现在事件 JSON 和 Blob 表中。

## File Change 保留

文件恢复能力不是永久版本控制。

未置顶 Session 的 File Change 会受到两项保留限制：

- 最近 100 条 User 消息对应的范围；
- 最长 30 天。

过期记录清理后，不再被引用的 Blob 会被垃圾回收。置顶 Session 不执行这项自动
裁剪。

因此，Foya 的文件恢复不能替代 Git 或正式备份。

## Artifact

图片等二进制 Artifact 保存在：

```text
<data-dir>/artifacts/<session-id>/
```

每个 Artifact 包含：

- 规范化后的二进制文件；
- 独立 JSON Metadata；
- SHA-256；
- Media Type、尺寸和字节数；
- 是否已提交到消息的状态。

Artifact Store 会验证 ID、文件类型、尺寸、像素数和 Hash。事件日志只保存
Attachment 引用。

Session 分支会把引用到的 Artifact 复制到新 Session，避免新会话依赖源目录。
删除 Session 时，其 Artifact 目录一并清理。

## 其他持久文件

主要文件包括：

| 文件或目录 | 内容 |
|---|---|
| `connections.json` | Provider Connection 与 API Key |
| `default-models.json` | 各用途默认模型 |
| `agent-limits.json` | SubAgent 调度限制 |
| `projects.json` | Project 目录索引 |
| `skills.json` | Skill 启用和固定状态 |
| `automations.json` | 自动化任务 |
| `workflows.json` | Plan、Spec 和 Goal 工作流 |
| `agent-runs.json` | SubAgent Run 快照 |
| `canvases.json` | Canvas 文档 |
| `canvas-assets/` | Canvas 资源 |
| `web-search.json` | Web Search 配置 |
| `memory/maintenance.json` | Memory 后台维护水位 |

MCP 配置与凭证位于 `~/.foya/mcp.json` 和
`~/.foya/mcp-credentials.json`。

多数 JSON 和 Markdown 写入使用临时文件后 Rename，降低进程中断留下半个文件的
风险。但这些文件之间、以及它们与 SQLite 之间没有统一事务。

## Rules 与 Memory

Rules 和 Memory 使用人类可读 Markdown：

```text
~/.foya/rules/user_rules.md
~/.foya/memory/user_profile.md
~/.foya/memory/projects/.../project_memory.md
<project>/.foya/rules/**/*.md
```

`<data-dir>/context/events.jsonl` 保存它们的变更通知，用于客户端回放和同步。Markdown
文件仍是实际内容来源。

## 重启恢复

内核启动时执行以下恢复：

- 打开 SQLite 并确保当前 Schema 存在；
- 将 Session Phase 重置为 `idle`；
- 修复中断的文件回退 Journal；
- 清理过期 File Change；
- 将未完成的 SubAgent Run 标记为 `interrupted`；
- 将运行中的 Canvas 生成节点标记为错误；
- 重新加载 Automation、Channel、MCP 和配置文件；
- 启动 Memory Maintenance。

以下状态不会恢复：

- 运行中的 Provider Stream；
- 内存消息队列；
- 等待中的审批 Channel；
- Session 临时 Grant；
- 已启动但未完成的工具调用。

Foya 不自动重放这些操作，因为它们可能已经在外部系统产生副作用。

## 删除

永久删除 Session 时：

1. Kernel Service 先停止运行态和所有 Child Session；
2. Session ID 写入删除标记；
3. SQLite 中的事件、投影和 Session 明细被删除；
4. 不再引用的 File Blob 被回收；
5. Session Artifact 被删除；
6. 在线客户端收到删除事件。

删除 Project 只移除 Foya 的 Project 记录和关联 Session 数据，不删除实际项目
目录。

## 备份

一致备份的建议流程：

1. 先关闭 Foya 内核；
2. 备份完整 Foya 数据目录；
3. 同时备份 `~/.foya`；
4. 根据需要备份各 Project 的 `.foya` 目录；
5. 将备份视为敏感数据。

只复制 `foya.db` 会遗漏 Artifact、Connection、MCP、Rules、Memory 和其他配置。

运行中直接复制 WAL 数据库可能得到不一致快照。需要在线备份时应使用 SQLite
Backup API，而不是只复制主数据库文件。

## 凭证

以下文件可能包含敏感信息：

- `connections.json` 中的 API Key；
- `~/.foya/mcp-credentials.json` 中的 Bearer Token；
- 用户自行配置到 Hook、Command 或外部服务中的环境变量。

当前实现依赖用户目录和文件权限保护这些内容，尚未统一接入操作系统 Keychain。
不要把完整数据目录提交到 Git 或直接公开分享。

## 当前边界

- Schema 通过幂等 `CREATE TABLE IF NOT EXISTS` 建立，没有独立版本化迁移框架。
- SQLite 和外部 JSON/Markdown 文件之间不具备跨存储事务。
- 文件恢复数据有保留期限，不能替代版本控制。
- Memory Maintenance 的唤醒信号、审批等待和 Provider Stream 都是进程内状态。
- 数据目录格式面向单用户实例；本机、SSH 和 HTTPS 模式都不提供多租户数据库隔离。
