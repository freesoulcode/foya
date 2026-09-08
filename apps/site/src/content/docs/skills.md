---
title: Skills
description: 使用可复用的指令包为 Agent 提供专业流程和配套资源。
slug: docs/skills
---

Skill 是一组可复用的任务说明。它告诉 Agent 在特定场景中应该采用什么流程，并可
携带参考文档、脚本、模板和其他资源。

Skill 不提供新的系统权限。它只能使用当前 Foya 实例已经存在并允许的 Tool。

## Skill 目录

Foya 从以下位置发现 Skill：

```text
~/.agents/skills/
~/.foya/skills/
<project>/.agents/skills/
<project>/.foya/skills/
```

内置 Skill 也会加入统一目录。存在同名 Skill 时，Project Scope 高于 Global
Scope，Foya 原生目录高于兼容目录。

每个 Skill 是独立目录，入口文件为 `SKILL.md`。

## `SKILL.md`

入口文件包含 YAML Frontmatter 和 Markdown 正文。元数据可以声明：

- Name；
- Description；
- 允许使用的 Tool；
- 必需 Tool；
- 必需 Capability；
- 是否具有附加 Resource。

Description 用于发现和触发，不等于完整执行说明。Agent 必须加载正文后才能按
Skill 工作。

## 渐进加载

为了避免所有 Skill 正文长期占用上下文，Foya 使用渐进加载：

1. 初始 Prompt 只包含可用 Skill 的元数据。
2. Agent 根据当前任务选择匹配 Skill。
3. `skill_load` 加载完整 `SKILL.md`。
4. 如正文引用其他文件，使用 `skill_read_resource` 按需读取。

当 Skill 数量超过 Prompt Catalog 预算时，Agent 可以使用 `skill_search` 搜索未
显示的 Skill。

## 显式选择

在聊天输入框键入 `/` 可以同时搜索命令和当前项目可用的 Skill。选择 Skill 后，
输入框会显示 Skill 标签；发送消息时 Foya 会校验对应的稳定引用，并只为当前回合
加载其完整指令。项目 Skill 和插件 Skill 都使用相同流程。

## Resource

Skill 目录可以包含：

```text
references/
scripts/
assets/
templates/
```

Foya 会建立资源清单。文本资源通过 `skill_read_resource` 读取，并验证规范路径仍
位于 Skill 根目录内，防止相对路径逃逸。

Skill 中的脚本不会因为存在于 `scripts/` 就自动执行。执行仍需调用对应 Tool，并
经过当前权限策略。

## Requirements

Skill 可以声明 Required Tools 和 Required Capabilities。Foya 在暴露或加载 Skill
前检查运行环境。

缺少依赖时：

- Skill 仍可出现在诊断信息中；
- 不会作为可正常调用的 Skill 进入模型流程；
- 加载请求会返回缺失项。

Requirements 只做可用性校验，不会注册新 Tool 或授予权限。

## Enabled 与 Pinned

用户可以单独启用或停用 Skill。

Pinned Skill 的正文会随 Catalog 直接进入系统提示词，适合非常短且每次都需要的
流程。普通 Skill 只显示元数据并按需加载。

固定过多 Skill 会持续增加上下文，应优先使用渐进加载。

## Project Scope

Project Skill 只对绑定该 Project 的 Session 可见，适合随仓库共享：

- 发布流程；
- 项目测试命令；
- 特定框架约定；
- 代码生成模板。

Global Skill 适合跨项目通用能力。

## 安全边界

Skill 内容属于用户可控指令，不能：

- 改变 Session Approval Mode；
- 绕过 Sandbox；
- 注册未授权 Tool；
- 读取其他 Skill 包外的任意文件；
- 覆盖系统安全约束；
- 把 Required Tool 当作自动授权。

模型加载 Skill 后，实际动作仍通过 Tool Runtime。

## 与其他扩展的区别

| 机制 | 主要用途 |
|---|---|
| Skill | 描述可复用工作流程 |
| Rule | 约束 Agent 必须怎样工作 |
| Memory | 保存跨会话事实 |
| Tool | 执行具体操作 |
| MCP | 从外部进程或服务提供 Tool、Resource 和 Prompt |
| Hook | 在生命周期节点运行确定性命令 |

## 当前边界

- Skill 发现基于本地目录。
- Skill 正文和 Resource 有大小与路径限制。
- Skill 匹配依赖 Name、Description 和模型判断。
- Pinned Skill 会直接占用每次请求的上下文。
- Skill 不能安装缺失的系统依赖或自行扩大权限。
