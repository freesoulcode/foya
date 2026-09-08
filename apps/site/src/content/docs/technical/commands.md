---
title: Commands
description: 了解 Foya 如何发现、解析和执行用户显式调用的命令。
slug: docs/technical/commands
---

Command 是由用户显式触发的入口。它可以把一段可复用 Prompt 提交为普通 Turn，
也可以启动内核定义的 Workflow。

Command 不由模型自动选择，因此适合用户需要明确控制入口的工作方式。

## Command 类型

| Kind | 行为 |
|---|---|
| `prompt` | 展开 Markdown 正文并提交普通 Turn |
| `workflow` | 创建持久 Workflow，再启动受约束 Turn |

当前内置 Workflow Commands 为：

```text
/plan
/spec
/goal
```

内置名称保留，用户 Command 不能覆盖。

## 作用域

Global Command 位于：

```text
~/.foya/commands/
```

Project Command 位于：

```text
<project>/.foya/commands/
```

解析同名 Command 时：

```text
Built-in > Project > Global
```

Project Command 只对绑定该 Project 的 Session 可见。

## 文件格式

每个自定义 Command 是 Markdown 文件：

```markdown
---
name: test-api
description: 运行并分析 API 测试
---

运行 API 测试，定位失败原因，并给出可以验证的修复。

用户参数：$ARGUMENTS
```

未在 Frontmatter 提供 Name 时，Foya 根据相对路径生成名称。目录分隔符转换为
冒号，例如：

```text
quality/test.md → quality:test
```

Command 文件最大 256 KiB，目录最多三层，不跟随符号链接。

## 参数展开

正文支持三个参数标记：

```text
$ARGUMENTS
$@
{{args}}
```

执行时，所有标记都会替换为用户提供的参数。

如果正文没有标记但用户提供了参数，Foya 会在末尾添加明确的“用户补充参数”
区块，避免静默丢失。

## 执行 Prompt Command

执行流程：

1. 根据 Session Project 计算有效 Command 集合；
2. 去掉输入名称开头的 `/`；
3. 解析 Built-in、Project 或 Global Command；
4. 展开参数；
5. 作为普通 User Input 提交 Kernel Service；
6. Session 忙时进入同一 FIFO Queue。

Command 名称会保存在 User Message 中，便于界面区分普通输入和命令触发。

## 管理操作

用户可以：

- 按作用域列出 Command；
- 创建空模板；
- 修改 Name、Description 和 Body；
- 移动由 Name 决定的文件路径；
- 删除 Command。

Name 只允许字母、数字、冒号、下划线和连字符，长度最多 64。重命名时会检查目标
作用域中是否存在冲突。

## 与 Skill 的区别

| Command | Skill |
|---|---|
| 用户显式触发 | Agent 根据任务选择 |
| 展开为一次输入 | 提供完整工作流程 |
| 适合固定快捷操作 | 适合领域能力和资源包 |
| 单个 Markdown 文件 | 包含 `SKILL.md` 的目录 |

Command 可以要求 Agent 加载某个 Skill，但不会自动赋予 Skill 需要的 Tool。

## 与 Workflow 的关系

`plan`、`spec` 和 `goal` 是 Built-in Command。它们不从用户 Markdown 加载，而是由
Kernel Service 创建持久 Workflow State，并根据 Kind 调整 Agent Policy。

自定义 Prompt Command 不能伪装成 Built-in Workflow，也不能使用保留名称。

## 当前边界

- Command 必须由用户或客户端显式执行。
- 自定义 Command 只有 Prompt 类型。
- Built-in Workflow 名称不可覆盖。
- 参数替换是文本替换，不是模板语言。
- Command 不绕过 Session Queue、权限或 Tool 限制。
