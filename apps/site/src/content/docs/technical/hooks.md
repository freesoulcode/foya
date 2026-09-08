---
title: Hooks
description: 了解 Foya 如何在 Agent 生命周期节点运行确定性外部命令。
slug: docs/technical/hooks
---

Hook 是由用户配置的本地命令，在 Agent 生命周期的特定节点运行。它适合执行格式
检查、策略验证、通知和上下文补充等确定性操作。

Hook 不等同于 Tool。Tool 由模型主动调用，Hook 由 Runtime 在固定事件上自动触发。

## 支持的事件

| Event | 时机 |
|---|---|
| `SessionStart` | Session 创建并获得稳定 ID 后 |
| `SessionEnd` | Session 删除前 |
| `UserPromptSubmit` | User Input 写入 Agent Loop 前 |
| `PreToolUse` | Tool 执行前 |
| `PostToolUse` | Tool 返回后 |
| `PermissionRequest` | Tool 需要审批时 |
| `SubagentStart` | Child Session 建立后、开始执行前 |
| `SubagentStop` | 子 Agent 准备返回结果时 |
| `PreCompact` | Context Compaction 执行前 |
| `PostCompact` | Context Compaction 完成或失败后 |
| `Stop` | 模型准备结束 Turn 时 |
| `TurnComplete` | Turn 成功、失败或取消后 |
| `Notification` | 发生通知类状态时 |

`SessionEnd`、`PostCompact`、`TurnComplete` 和 `Notification` 是异步观察
事件，结果只用于审计。其他 Hook 默认同步执行，可以影响当前生命周期。任意同步
事件也可以配置 `async: true` 转为后台观察模式，此时输出不影响 Agent。

## 配置作用域

Global Hook：

```text
~/.foya/hooks.json
```

Project Hook：

```text
<project>/.foya/hooks.json
```

执行时先加载 Global，再加载 Project。两个作用域都会生效；相同 ID 的 Project
Hook 不会静默覆盖 Global Hook。

每项配置包含 Event、Command、可选 Name、Matcher、Timeout、Async 和 Enabled。

## 输入协议

Hook Command 从标准输入读取 Version 1 JSON。根据事件不同，内容可以包含：

- Session ID 和 Run ID；
- Event ID、发生时间和 Project ID；
- 工作目录和 Project 路径；
- Model；
- User Prompt；
- Tool Name、Tool Call ID 和 JSON Input；
- Tool Output；
- Tool 状态、错误和耗时；
- Assistant Message；
- 子 Agent、压缩和回合完成状态；
- Notification 类型。

无关字段会省略。Hook 应根据 `event` 判断哪些字段可用。

## 输出协议

正常退出时，Hook 可以在标准输出返回：

```json
{
  "decision": "allow",
  "halt": false,
  "reason": "",
  "context": ["additional context"],
  "updated_input": {}
}
```

所有字段可省略。空输出表示不作决定。

`updated_input` 必须是 JSON Object，只在允许继续时应用。

## 决策合并

多个匹配 Hook 按配置顺序执行：

- Deny 优先于 Allow；
- Halt 一旦出现就保持；
- Reason 按顺序合并；
- Context 按顺序追加；
- Updated Input 对当前 Tool Input 做浅层 Object Merge；
- 后一个有效 Updated Input 可以继续修改前一个结果。

配置或执行错误会记录，但默认 fail-open，不自动阻断 Agent。

## Tool 生命周期

PreToolUse 可以：

- 拒绝调用；
- 修改顶层 Tool Input；
- 附加返回给模型的 Context；
- 要求当前批次结束。

PostToolUse 在 Tool 已产生结果后运行，并携带成功状态、错误和执行耗时。它可以把
结果标记为错误、附加 Context 或要求停止，但不能撤销已经发生的外部副作用。

需要阻止操作时应使用 PreToolUse。

PermissionRequest 的 Allow 可以批准当前请求，Deny 可以拒绝请求；不返回决定时
继续 Foya 的内建审批流程。其他 Hook 的 Allow 不会绕过 Approval 或 Sandbox。

## Exit Code

除 JSON 输出外，Hook 还定义特殊退出码：

| Exit Code | 含义 |
|---|---|
| `0` | 正常解析标准输出 |
| `2` | Deny，Reason 取标准错误 |
| `49` | Deny 并 Halt |

其他非零退出码记录为 Hook Error，并按 fail-open 处理。

## 运行限制

- 默认 Timeout 为 30 秒；
- 配置最大 Timeout 为 600 秒；
- `async: true` 的 Handler 后台执行，控制输出会被忽略；
- 标准输出和错误输出各限制为 64 KiB；
- 超过限制的输出视为错误；
- Unix 使用 `/bin/sh -c`，Windows 使用 `cmd.exe /C`；
- 工作目录是当前 Session 的 CWD。

Hook 会继承大部分 Kernel 环境变量，但名称包含 `API_KEY`、`SECRET`、`TOKEN`、
`PASSWORD` 的变量以及 Langfuse Auth、OTLP Header 会被过滤。

## 审计

每个已执行 Hook 都产生 `hook_completed` 事件，记录：

- 配置 ID 和 Name；
- Event；
- Decision 和 Halt；
- Reason；
- 耗时；
- Error；
- 是否修改了 Input。

审计事件不保存完整 Hook 标准输出或修改后的 Secret。

## 安全边界

Hook Command 由本机 Shell 直接执行，不经过 Agent Tool Approval 或 Sandbox。

因此 Hook 配置本身属于受信任代码。不要运行来自不可信仓库的 Project Hook，也
不要把用户输入未经校验地拼接进 Shell Command。

Hook 无法扩大 Agent Tool 权限，但 Hook 进程本身拥有启动 Kernel 的宿主用户权限。

## 当前边界

- Hook 只支持本地 Shell Command。
- 没有插件进程隔离或独立凭证沙箱。
- Updated Input 只做浅层 Object Merge。
- PostToolUse 不能回滚 Tool 副作用。
- Hook 错误默认不阻断 Agent，只通过审计事件暴露。
