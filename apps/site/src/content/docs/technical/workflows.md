---
title: Workflows
description: 了解 Plan、Spec 和 Goal 命令如何建立持久工作流状态。
slug: docs/technical/workflows
---

Workflow 为需要“先产出文档，再进入下一阶段”的任务提供持久状态。它由 Built-in
Command 启动，与普通 Prompt Command 分离。

当前定义三种 Kind：

| Kind | 目标 |
|---|---|
| `plan` | 生成可审查的实施计划 |
| `spec` | 生成技术规格 |
| `goal` | 定义可持续追踪的目标 |

## Workflow Record

每条 Record 包含：

- ID；
- Session ID；
- Kind；
- Status；
- Goal；
- 可选 Title；
- 最终 Content；
- Artifact Path，或 Spec 的 Artifact Root 与三份文档路径；
- Revision；
- 创建和更新时间。

同一 Session 启动新 Workflow 时，已有 Active、Ready 或 Approved Record 会先标记为
Closed。

## 状态

定义的状态包括：

```text
active → ready → approved → completed
   └────────────→ closed
```

- `active`：Agent 正在生成 Artifact。
- `ready`：Artifact 已完成，等待用户确认。
- `approved`：用户确认进入后续阶段。
- `completed`：已批准的 Spec 任务全部完成。
- `closed`：被新 Workflow 替代，或由用户主动退出。

Revision 每次状态或内容更新时递增。

## Plan

Plan 是当前实现最完整的 Workflow：

1. `/plan <goal>` 创建 Active Record。
2. Session 进入 Plan Mode。
3. 每个模型 Step 应用只读 Workflow Policy。
4. Agent 输出最终计划。
5. 最终内容保存到 Markdown Artifact。
6. Record 进入 Ready，Session 进入 `plan_ready`。
7. 用户批准后进入 Approved。
8. Session 回到 Execute Mode，并自动提交实施请求。

Active 或 Ready 阶段都可以从活动栏退出。退出会停止仍在生成的回合，并恢复进入
Plan 前的 Session Mode；已生成的 Artifact 保留。

## Plan Tool Policy

Plan Mode 只允许：

```text
read
web_search
web_fetch
skill_search
skill_load
skill_read_resource
rule_load
ask_user
```

它不允许文件修改、Shell 或 SubAgent。这个限制由 Runtime 每个 Step 重新解析，
不是只写在 Prompt 中。

批准后的实施 Turn 使用普通 Execute Mode，不再创建第二份 Plan。

## Spec

Spec 面向范围较大、需要先对齐需求、任务和验收标准的工作：

1. `/spec <需求>` 创建 Active Record，并进入只读生成阶段。
2. Agent 可以读取项目、搜索资料和向用户提问，但不能修改源码或执行 Shell。
3. Agent 通过内核提供的 `workflow_submit_spec` 工具一次提交三份完整文档：
   `spec.md`、`tasks.md` 和 `checklist.md`。
4. Kernel Service 写入文档后将 Record 切换为 Ready，等待用户确认。
5. Ready 阶段仍保持只读；用户可以直接编辑文档，或通过对话要求 Agent 修订。
6. 用户点击“确认并执行”后，Kernel Service 从 `tasks.md` 初始化会话任务并恢复
   普通执行权限。
7. Agent 执行时通过 `update_tasks` 更新任务状态，Kernel Service 同步更新
   `tasks.md` 的复选框。

Active 或 Ready 阶段都可以从活动栏退出。退出后只读约束立即结束，已经生成的文档保留。

Spec Artifact 位于：

```text
<project>/.foya/specs/<task-name>/
├── spec.md
├── tasks.md
└── checklist.md
```

`/spec` 只对已绑定项目的 Session 开放。

## Goal

`/goal` 创建持久目标并提交专用 User Prompt。目前 Goal 仍使用单文件 Artifact，
不提供 Spec 的三文档审阅界面。

## Artifact

Workflow Artifact 根据 Kind 保存：

```text
<data-dir>/plans/<workflow-id>.md
<data-dir>/goals/<workflow-id>.md
```

Project Session 则保存在：

```text
<project>/.foya/plans/
<project>/.foya/specs/<task-name>/
<project>/.foya/goals/
```

文件包含 Kind、Status、Goal Frontmatter 和正文。写入使用临时文件后 Rename。

Record 索引保存在：

```text
<data-dir>/workflows.json
```

## 事件

Workflow 创建、完成和批准时会产生 `workflow_updated` 事件。客户端以 Record
Revision 更新界面。

Workflow Event 属于 Session 事件流，但 Artifact 文件是面向用户的持久产物。

## 与 Command 的关系

Workflow 不能直接由模型自行创建。入口是 Built-in Command：

```text
/plan
/spec
/goal
```

Command 负责解析用户参数和建立 Record，Workflow Manager 负责状态与 Artifact，
Agent Engine 负责执行受约束 Turn。

## 与 Session Mode 的关系

Session Mode 只在 Plan 流程中改变：

- Execute → Plan；
- Plan → Plan Ready；
- 批准或退出后 Plan Ready → Execute。

Workflow Status 是持久业务状态，Session Mode 是当前运行策略。两者会通过 Kernel Service
一起更新和广播。

## 当前边界

- 一个 Session 启动新 Workflow 时，会关闭此前仍处于 Active、Ready 或 Approved
  状态的 Workflow。
- Spec 执行进度以 `tasks.md` 和会话任务列表为准；验收项由 Agent 在实际验证后更新。
- Workflow Artifact 和 JSON 索引不在同一事务中。
- Workflow Policy 不能扩大系统权限，只能缩小 Tool 集合。
