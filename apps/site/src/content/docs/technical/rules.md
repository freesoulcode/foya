---
title: 规则
description: 了解 Foya 如何定义、匹配和加载用户控制的 Agent 行为约束。
slug: docs/technical/rules
---

Rule 用于表达 Agent 应该怎样工作，例如编码规范、验证要求和项目约定。它是用户
管理的行为约束，不是历史记录，也不是从对话自动推断出的事实。

## Rule 适合表达什么

适合使用 Rule：

- 修改代码后必须运行哪些检查；
- 某类文件应遵守什么命名或格式规范；
- 提交内容不得包含哪些文件；
- 特定目录应该使用哪种工作流程；
- 用户希望 Agent 始终遵守的协作约定。

不适合使用 Rule：

- 当前任务已经完成到哪里；
- 某个错误的完整日志；
- 用户的一次性请求；
- 需要跨会话保存的事实或偏好；
- 需要执行的程序逻辑。

长期事实应使用 [Memory](./memory.md)，可执行的确定性逻辑应使用 Tool 或 Hook。

## 数据模型

一条 Rule 包含：

| 字段 | 作用 |
|---|---|
| ID | 稳定标识 |
| Name | 用于界面展示和手动引用 |
| Description | 帮助模型判断是否需要加载 |
| Scope | Global 或 Project |
| Trigger | 决定何时进入上下文 |
| Globs | `glob` Trigger 的匹配模式 |
| Path | Project Rule 的文件位置 |
| Content | 实际规则正文 |

每条 Rule 的正文最多 6000 个 Unicode 字符。无效配置或超限内容会在保存时被拒绝。

## 作用域

Rules 支持两个作用域：

| 作用域 | 生效范围 |
|---|---|
| Global | 所有 Session |
| Project | 绑定指定 Project 的 Session |

对于 Project Session，Foya 会同时读取 Global 与对应 Project Rules。Prompt 会
明确要求：当项目规则与全局规则冲突时，优先使用项目规则。

Rule 不能削弱系统安全、审批、工具访问和沙箱策略。即使用户 Rule 写了“无需审批”，
Approval Gateway 仍按 Session 的实际审批模式工作。

## Trigger

Rule 通过 Trigger 决定何时进入模型上下文。

### `always`

每个相关 Session 的模型 Step 都直接注入完整正文。

适合短小、稳定且每次都需要遵守的要求。过多 Always Rule 会持续占用上下文。

### `glob`

当当前活动文本匹配指定路径模式时注入正文。

活动文本由当前用户请求和本 Turn 已出现的工具名称、参数组成。例如，用户提到
`src/api/client.go`，或工具准备操作该路径，都可能激活匹配的 Rule。

支持的模式包括：

- `*`：匹配一个路径段内的字符；
- `?`：匹配一个字符；
- `**`：跨目录匹配。

Glob 匹配用于选择上下文，不是文件系统访问控制。

### `model_decision`

正文不会直接注入。Foya 只把 Rule 的 Name 和 Description 放入可用 Rule Index。

模型判断该 Rule 与当前任务相关时，调用 `rule_load` 获取正文。此模式适合内容
较长或只在少数任务中需要的规则。

`model_decision` 必须提供 Description，否则无法保存。

### `manual`

只有当前活动文本显式包含 `@RuleName` 或 `@RuleID` 时才注入正文。

适合由用户明确启用的可选工作方式。

## 每个 Step 的匹配

Rule 不是只在 Turn 开始时匹配一次。

Foya 会维护当前 Turn 的活动文本：

```text
用户原始请求
+ 已出现的工具名称
+ 工具调用参数
```

每个模型 Step 开始前重新选择 Active Rules。因此，后续工具调用暴露的新路径可以
激活对应的 Glob Rule。

这项机制只影响下一次上下文组装，不会修改已经发出的 Provider 请求。

## Prompt 表示

直接生效的 Rule 会加入 `<foya_rules>` 区块。可按需加载的 Rule 只在
`<foya_available_rules>` 中显示 Name 和 Description。

Foya 会：

- 清理控制字符；
- 转义会破坏上下文标签的字符；
- 限制直接 Rule 的合计字符数；
- 限制 Rule Index 的合计字符数；
- 对超出预算的内容添加省略标记。

当前直接 Rule 总上限为 12000 字符，Rule Index 上限为 3000 字符。这个限制只影响
单次 Prompt，不会修改磁盘文件。

## `rule_load`

`rule_load` 只能读取当前 Project 有效范围内、Trigger 为 `model_decision` 的 Rule。
调用参数是准确的 Name 或 ID。

工具返回带 Rule Name 和 Scope 的正文区块。读取 Rule 不会：

- 修改 Rule；
- 把它永久改成 Always；
- 扩大当前工具权限；
- 读取其他 Project 的 Rule。

## 文件布局

Global Rules 集中保存在：

```text
~/.foya/rules/user_rules.md
```

Project Rules 保存在项目目录：

```text
<project>/.foya/rules/**/*.md
```

每个 Project Rule 文件包含一条 Rule，使用 YAML Frontmatter 描述 Name、
Description、Trigger 和 Globs。目录最多支持三层。

Project Rule 位于仓库内部，可以由用户选择纳入版本控制。Global Rule 位于用户
配置目录，不随项目共享。

## 与项目说明文件的区别

Foya 还会读取 `AGENTS.md`、`CLAUDE.md` 和 `GEMINI.md`。这些文件与 Rules 的区别
是：

| 项目说明文件 | Foya Rule |
|---|---|
| 兼容其他 Agent 生态 | Foya 管理的数据模型 |
| 按文件位置整体加载 | 支持 Scope 和 Trigger |
| 没有独立 Rule ID | 有稳定 ID、Name 和 Description |
| 主要随仓库维护 | 可由设置界面管理 |

两者都是用户可控内容，都不能覆盖更高优先级的系统安全和权限边界。

## 更新与同步

创建、更新或删除 Rule 时，Foya 会：

1. 校验 Scope、Trigger 和正文；
2. 原子写入对应 Markdown 文件；
3. 追加 Context 变更事件；
4. 向设置界面订阅者广播更新。

Rule 文件是持久内容，Context Event 用于客户端同步，不替代文件本身。

下一次模型 Step 会重新读取有效 Rule，因此通常不需要重启内核或重新创建 Session。

## 选择 Trigger

| 情况 | 建议 |
|---|---|
| 每次任务都必须遵守 | `always` |
| 只针对目录或文件类型 | `glob` |
| 内容较长，由模型判断相关性 | `model_decision` |
| 只在用户明确点名时启用 | `manual` |

Rule 应尽量具体、可验证。相比“写出高质量代码”，
“修改 Go 文件后运行对应 package test”更容易执行和检查。

## 当前边界

- Rule 只支持 Global 和 Project 两级作用域。
- Glob 根据活动文本匹配，不跟踪模型尚未提及的所有文件。
- `model_decision` 依赖模型主动调用 `rule_load`。
- Rule 没有数值化优先级；Project 与 Global 的冲突关系通过 Prompt 明确。
- Rule 不是强制执行引擎。真正的权限限制必须由 Approval、Sandbox 或 Tool 实现。
