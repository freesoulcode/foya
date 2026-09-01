# Hooks

Hooks 是由 Foya 内核触发的用户命令。命令从标准输入读取 JSON 请求，并可在标准输出返回 JSON 结果。

## 配置位置

全局配置：

```text
~/.foya/hooks.json
```

项目配置：

```text
<PROJECT_FOLDER>/.foya/hooks.json
```

触发时先执行全局 Hook，再执行项目 Hook。同一 `id` 不会发生静默覆盖。

## 配置格式

```json
[
  {
    "id": "check-bash",
    "name": "Check bash commands",
    "event": "PreToolUse",
    "matcher": "^bash$",
    "command": "foya-hook-check-bash",
    "timeout": 10,
    "enabled": true
  }
]
```

字段：

| 字段 | 含义 |
|---|---|
| `id` | 可选稳定标识，用于审计与界面展示 |
| `name` | 可选显示名称 |
| `event` | 触发事件 |
| `matcher` | 仅工具事件有效；Go 正则，匹配工具名称 |
| `command` | 通过 shell 执行的命令 |
| `timeout` | 超时秒数；省略时为 `30`，最大 `600` |
| `enabled` | 省略或 `true` 表示启用 |

支持的事件：

| 事件 | 触发时机 | 行为 |
|---|---|---|
| `SessionStart` | Session 创建后、首个回合前 | 可注入首轮上下文 |
| `UserPromptSubmit` | 用户输入提交后、模型请求前 | 可拦截请求或补充上下文 |
| `PreToolUse` | 工具实际执行前 | 可拦截或改写工具参数 |
| `PostToolUse` | 工具执行完成后 | 可检查结果并补充模型上下文 |
| `Stop` | 模型准备结束当前回合时 | 可阻断结束并让模型继续 |
| `Notification` | 审批等待或回合结束时 | 异步通知，不影响主流程 |

## 请求输入

Hook 命令通过标准输入收到类似以下 JSON：

```json
{
  "version": 1,
  "event": "PreToolUse",
  "session_id": "session-123",
  "run_id": "run-123",
  "cwd": "/workspace/project",
  "project_path": "/workspace/project",
  "model": "gpt-5",
  "tool_name": "bash",
  "tool_call_id": "call-123",
  "tool_input": {
    "command": "git status"
  }
}
```

字段会按事件省略。`UserPromptSubmit` 带 `user_prompt`，`PostToolUse` 带 `tool_output`，`Stop` 带 `assistant_message`，`Notification` 带 `notification`。

为避免凭证泄露，内核不会向 Hook 子进程传递名称包含 `API_KEY` 的环境变量。

## 命令输出

命令退出码为 `0` 时，可向标准输出写入：

```json
{
  "decision": "deny",
  "reason": "production environment requires approval",
  "context": "Use the staging environment instead.",
  "updated_input": {
    "command": "git status --short"
  }
}
```

| 字段 | 含义 |
|---|---|
| `decision` | `none`、`allow` 或 `deny` |
| `reason` | 拦截原因 |
| `context` | 字符串或字符串数组，附加给模型 |
| `updated_input` | 仅 `PreToolUse` 使用；JSON 对象，浅合并覆盖工具参数 |
| `halt` | `true` 时终止当前回合 |

`allow` 仅表达 Hook 允许操作，**不会绕过 Foya 的审批或沙箱**。

退出码也可表达控制结果：

| 退出码 | 含义 |
|---|---|
| `0` | 读取 JSON 输出；空输出等价于无意见 |
| `2` | 拦截当前动作，标准错误输出作为原因 |
| `49` | 终止整个回合，标准错误输出作为原因 |
| 其他非零值 | Hook 失败，记录审计事件但 fail-open |

超时、无效 JSON、未知输出字段和普通命令失败都会 fail-open；Hook 失败不会让 Agent 内核崩溃。

## 审计与多端同步

每个 Hook 的执行结果都会以 `hook_completed` 事件写入 Session 事件日志并经 SSE 广播。`Notification` 异步执行，其完成同样会产生审计事件，但不等待命令结束。

`PreToolUse` 的 `deny` 只阻止该工具调用；模型会收到结构化错误并可修正方案。`Stop` 的 `deny` 会写入系统反馈并发起下一轮模型请求。
