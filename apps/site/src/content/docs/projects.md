---
title: Project
description: 管理 Foya 中的本地项目目录以及与 Session 的绑定关系。
slug: docs/projects
---

Project 是 Foya 对本地工作目录的稳定引用。它让不同 Session 可以使用同一个项目
目录和项目级配置，同时避免把绝对路径散落在会话元数据中。

## Project 内容

一个 Project 保存：

- 稳定 ID；
- 显示名称；
- 本地目录的规范绝对路径；
- 置顶状态；
- 创建和更新时间。

Foya 不复制项目文件。Project 始终指向用户设备上的现有目录。

## 注册 Project

注册时必须提供已经存在的目录。Foya 会：

1. 转换为绝对路径；
2. 解析符号链接；
3. 确认目标是目录；
4. 将规范路径写入 Project Catalog。

目录之后被移动或删除时，Project 记录仍然存在，但会显示为不可用。

## Session 绑定

Session 可以在创建时绑定 Project。绑定后：

- 文件和 Shell 工具默认在该目录工作；
- 加载项目级 Rules、Skills、Hooks 和 Commands；
- Project Memory 对该 Session 生效；
- 审批和沙箱可以使用稳定的工作区范围。

非空 Project 绑定建立后不能切换或清除。需要操作其他目录时，应创建新的 Session。
这可以防止长期历史中的路径和规则突然改变含义。

## 项目级配置

Foya 会识别 Project 中的以下内容：

```text
<project>/
├── AGENTS.md
├── CLAUDE.md
├── GEMINI.md
├── .agents/
│   ├── agents/
│   └── skills/
└── .foya/
    ├── commands/
    ├── hooks.json
    ├── rules/
    └── skills/
```

这些文件由项目所有者控制，可以根据团队需要纳入版本控制。

Project Memory 默认保存在用户目录，而不是项目仓库中。

## 置顶与排序

置顶只影响 Project 在客户端中的展示顺序，不影响权限、Session 或上下文。

未置顶 Project 通常按最近更新时间排序。

## 删除 Project

删除 Project 会：

- 从 Foya Catalog 中移除 Project；
- 删除绑定该 Project 的 Session 及相关 Foya 数据；
- 更新客户端中的 Project 列表。

删除操作不会删除实际项目目录，也不会修改其中的源代码或 `.git`。

执行删除前，应确认不再需要关联 Session 历史和 Artifact。

## 与工作目录的区别

Project 是持久身份，工作目录是一次工具执行使用的路径。

普通 Project Session 的工作目录来自 Project。无 Project Session 可以使用内核
提供的默认目录，但不会获得项目级 Rules、Memory 和 Skills。

## 当前边界

- 一个 Session 只能绑定一个 Project。
- 已绑定 Project 的 Session 不能切换目录身份。
- Foya 不负责移动、同步或备份项目目录。
- 删除 Project 会删除关联的 Foya Session 数据，但不删除磁盘项目。
