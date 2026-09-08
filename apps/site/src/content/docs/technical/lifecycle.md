---
title: 进程生命周期与恢复
description: 了解 Foya Kernel 的启动、关闭、中断恢复和不重放原则。
slug: docs/technical/lifecycle
---

Foya Kernel 持有 Session Runtime、工具进程和本地数据。进程重启后，持久事实可以
恢复，但进行中的模型调用和外部副作用不会自动续跑。

## 启动

Daemon 启动顺序为：

1. 加载默认配置和持久 Agent Limits；
2. 打开 SQLite 并建立 Schema；
3. 创建 Conversation Manager 和 Store；
4. 恢复文件回退 Journal；
5. 清理过期 File Change；
6. 装配 Tool、Model Adapter 和 Kernel Service；
7. 加载 MCP、Automation、Channel 和 Memory Maintenance；
8. 创建 HTTP Server；
9. 监听 Unix Socket 或配置的 TCP Address。

任何必要组件初始化失败都会终止启动。

## 单实例边界

Unix Socket 启动前会检查旧路径：

- 仍有进程监听时拒绝启动；
- 无法连接时视为陈旧 Socket 并删除；
- 新 Socket 使用 `0600`；
- 父目录使用 `0700`。

这避免两个 Kernel 同时拥有同一桌面数据目录。

## 桌面父进程

Tauri 启动 Sidecar 时传入父进程 ID。Kernel 监听父进程退出，并使用有限时间关闭
HTTP Server。

正常退出会关闭：

- Automation Scheduler；
- Channel；
- 根 Context；
- Browser Controller；
- MCP Session 与子进程；
- SQLite。

## 恢复的状态

启动时可恢复：

- Session 元数据和完成消息；
- Event Projection；
- Compaction Checkpoint；
- Workflow Record；
- Automation 配置；
- Channel 配置与 Chat 映射；
- SubAgent Snapshot；
- Rules、Memory、Skills、Hooks 和 Commands；
- Canvas Document 与 Asset。

## 状态收敛

部分持久状态在重启后不能继续运行，会转换为终止状态：

| 状态 | 重启行为 |
|---|---|
| Session `turn` / `compaction` | 重置为 `idle` |
| Queued / Running SubAgent | 标记 `interrupted` |
| Running Canvas Generation | 标记 `error` |
| Prepared File Rewind | 尝试恢复 |

这些更新让界面看到明确终止结果，而不是永久显示“运行中”。

## 不恢复的运行态

以下对象只存在于进程内：

- Provider Stream；
- Turn Cancel Context；
- Session Message Queue；
- 等待中的 Approval；
- 等待中的 Question；
- Browser Action；
- Interactive Terminal；
- Background Command；
- Session 临时 Grant；
- Deferred Tool 激活集合。

重启不会重新创建它们。

## 不重放原则

Foya 无法仅根据“没有收到完成事件”判断外部操作是否执行。例如命令可能已经修改
文件，但进程在记录结果前退出。

因此恢复策略是：

- 恢复可验证的持久状态；
- 标记未完成任务；
- 不自动重发模型请求；
- 不自动重放 Tool Call；
- 由用户检查工作区后决定下一步。

## 文件回退恢复

文件回退在修改文件前写入 Journal。启动时若发现未完成 Journal，会根据保存的
Before/After Blob 修复中断状态。

这是少数能够自动恢复的副作用，因为 Foya 保存了完整内容、Hash 和预期文件状态。

## 删除期间的迟到事件

删除 Session 时，Kernel Service 先停止运行并等待收尾。Conversation Store 再写入
Deleted Session 标记并删除数据。

如果旧 goroutine 仍尝试追加事件，Store 会检测删除标记并拒绝重新建立 Session
历史。

## 当前边界

- 没有跨进程任务接管。
- 内存 Queue 不恢复。
- 工具副作用不自动重放。
- 异常退出时依赖 SQLite WAL 与各文件的原子写入能力。
- 远程服务与本地模式使用相同的恢复流程。
- 远程高可用和多实例协调尚未实现。
