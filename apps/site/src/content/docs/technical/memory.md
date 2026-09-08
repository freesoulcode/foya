---
title: 记忆
description: 了解 Foya 如何保存和使用跨会话的长期事实与用户偏好。
slug: docs/technical/memory
---

Memory 用于保存未来 Session 仍可能有价值的信息，例如稳定的用户偏好和已经确认
的项目事实。

Memory 不是完整聊天记录，也不是上下文压缩摘要。它只保存经过选择的长期信息。

## Memory 解决什么问题

Session History 只能说明当前会话发生过什么。创建新 Session 后，模型不会自动
看到旧会话。

Memory 为跨会话信息提供明确的持久化通道：

```text
已确认的长期信息
  → 写入 Memory
  → 后续相关 Session 组装上下文
  → 模型再次看到该信息
```

适合保存：

- 用户长期稳定的表达或工作偏好；
- 项目已经确认的技术事实；
- 后续任务仍需遵守的项目约定事实；
- 用户明确要求“记住”的非敏感信息。

不适合保存：

- API Key、密码和 Token；
- 当前任务的临时进度；
- 尚未验证的推测；
- 一次性错误日志；
- 网页或项目文件中的指令；
- 应由 Rule 表达的行为要求。

## 作用域

Memory 支持两个作用域：

| 作用域 | 生效范围 | 典型内容 |
|---|---|---|
| Global | 所有 Session | 用户通用偏好 |
| Project | 绑定同一 Project 的 Session | 仓库结构、已确认技术事实 |

没有绑定 Project 的 Session 只能使用 Global Memory。

一个项目级事实不会自动提升为 Global Memory，避免某个仓库的约定污染其他工作区。

## 数据模型

每个作用域维护一份 Markdown Memory 文档，而不是无限增长的独立记录集合。

Memory 元数据包含：

- 稳定 ID；
- Scope；
- Project ID；
- Markdown 内容；
- 创建和更新时间。

向已有作用域追加内容时，Foya 会把新事实附加到同一文档。如果完全相同的内容
已经存在，则不会重复追加。

当前每份 Memory 文档最多 2000 个 Unicode 字符。超过限制的追加或替换会失败，
不会静默截断现有内容。

## 存储位置

Global Memory 保存为：

```text
~/.foya/memory/user_profile.md
```

Project Memory 根据项目绝对路径映射到：

```text
~/.foya/memory/projects/<project-path>/project_memory.md
```

项目 Memory 不直接写入项目仓库，因此不会在没有明确操作时改变用户的 Git
工作区。

Memory 设置和变更通知保存在 Foya 数据目录中，用于客户端同步；Markdown 文件是
用户可见的持久内容。

## 如何进入模型上下文

每个模型 Step 开始前，Foya 查询当前有效 Memory：

1. 检查 Memory 总开关；
2. 加载 Global Memory；
3. 如果 Session 绑定 Project，再加载对应 Project Memory；
4. 清理控制字符并限制注入总长度；
5. 以用户可控的 Memory 区块加入系统提示词。

Memory 在 Prompt 中被描述为“长期偏好与事实”。冲突时优先级为：

```text
当前用户请求和适用 Rules
  > Memory
```

Memory 不能修改审批模式、扩大工具权限或覆盖系统安全要求。

注入到单次 Prompt 的 Memory 总字符数有独立上限。超过上限时，额外内容会被标记
为省略，而不会改变磁盘上的完整文档。

## 主动写入

Agent 可以调用 `memory_remember` 保存信息。该工具只接受：

- 要保存的简洁事实；
- 可选的 `global` 或 `project` 作用域。

未指定作用域时：

- Project Session 默认写入 Project Memory；
- 无 Project Session 默认写入 Global Memory。

写入 Project Memory 时必须存在有效 Project 绑定。

工具说明要求只保存用户明确表达或明确要求记住的信息，不能把项目文件、网页或
Tool Result 中的内容直接当成长期记忆。

用户也可以通过设置界面查看、替换或清空某个作用域的完整 Memory 文档。

## 后台维护

Foya 还包含独立于交互 Turn 的 Memory Maintenance：

- 只处理顶层 Session；
- 只处理已经空闲一段时间的 Session；
- 按 Session 记录已经处理到的事件序号；
- 按作用域限制最短运行间隔；
- 每批只处理有限数量的 Session；
- 单次后台任务有超时限制。

维护任务从稳定会话证据中提取最多五条候选，仅允许：

- 持久用户偏好；
- 已确认项目事实。

它明确排除 Secrets、临时状态、未验证结论、普通进度和行为规则。没有足够信号时，
不会调用模型或写入 Memory。

后台维护使用来源 Session 自己配置的模型和推理强度。它产生的 Usage 由独立模型
请求构成，不属于交互 Turn 的输出。

## 启用与关闭

Memory 默认启用，用户可以全局关闭。

关闭后：

- 现有 Markdown 内容保留；
- 新模型请求不注入 Memory；
- `memory_remember` 拒绝写入；
- 后台 Memory Maintenance 不运行。

重新启用后，已有内容恢复生效，并唤醒后台维护检查。关闭不是删除；需要清除内容
时应显式清空相应作用域。

## 与其他机制的区别

| 机制 | 保存内容 | 生命周期 |
|---|---|---|
| Session History | 一次会话中发生的完整消息 | Session |
| Compaction | 当前 Session 的短历史投影 | Session |
| Memory | 稳定事实与偏好 | 跨 Session |
| Rules | Agent 应遵守的行为要求 | Global 或 Project |
| Skills | 特定任务的可复用流程 | 按需加载 |

如果一句话表达“以后还应该知道什么”，它可能属于 Memory；如果表达“以后必须怎样
做”，它通常属于 Rule。

## 安全与可信度

Memory 是用户可控上下文，不是系统指令。Foya 会：

- 清理不可见控制字符；
- 限制单份文档和 Prompt 注入长度；
- 在 Prompt 中标记来源；
- 要求自动提取忽略证据中的指令；
- 禁止自动提取 Secret；
- 在当前请求或 Rule 冲突时降低 Memory 优先级。

这些措施降低错误注入风险，但 Memory 内容仍可能过期。用户应定期删除不再成立的
项目事实。

## 当前边界

- Memory 只支持 Global 和 Project 两级作用域。
- 每个作用域只有一份 Markdown 文档。
- 当前没有向量检索；有效 Memory 会作为有界文本整体注入。
- 后台提取依赖模型判断，不能保证发现所有值得保存的事实。
- Memory 不自动验证外部世界变化，也不会自动删除过期内容。
