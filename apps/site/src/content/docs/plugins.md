---
title: 插件
description: 安装和管理符合 Agent Plugins 1.0.0 的可移植扩展包。
slug: docs/plugins
---

Foya 支持 [Agent Plugins 1.0.0](https://agent-plugins.org/specification)。
这是由多个厂商共同维护的开放打包规范，不是 Foya 私有格式。

## 包结构

插件是一个自包含目录：

```text
my-plugin/
├── plugin.json
├── skills/
│   └── summarize/
│       ├── SKILL.md
│       ├── scripts/
│       └── references/
└── mcp.json
```

根 `plugin.json` 必须声明规范版本和插件名称：

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "my-plugin",
  "version": "1.0.0",
  "description": "Example plugin"
}
```

Agent Plugins 1.0.0 的可移植核心仅包含：

- `skills/`：符合 Agent Skills 规范的 Skill；
- `mcp.json`：stdio、Streamable HTTP 或旧 SSE MCP Server。

Hooks、Agents 和 Commands 尚不是 1.0.0 的可移植组件。Foya 不会给
`plugin.json` 添加私有顶层字段。

## 安装与更新

Foya 不内置或维护官方插件市场。在主页侧边栏打开“插件”，通过“添加 → 添加插件
市场”注册 GitHub 仓库、HTTPS/Git SSH URL 或本机目录。可以指定 Git ref 和稀疏检出
路径。

Foya 会依次发现现有生态的市场清单：

- Codex：`.agents/plugins/marketplace.json`；
- Claude Code：`.claude-plugin/marketplace.json`；
- GitHub Copilot：`.github/plugin/marketplace.json`。

市场保存在本地快照中，可以独立刷新、停用或移除。Foya 会解析市场中的仓库、
子目录和 Git 版本信息，下载后仍按 Agent Plugins 1.0.0 校验；只包含其他客户端
私有组件的插件不会被允许安装。

点击插件名称或图标可以打开详情页。详情页展示版本、作者、许可证、来源和标签；
未安装插件会先预检远端包，展示 Foya 可用的 Skills、MCP Servers 和不受支持的
组件；安装后则展示实际加载状态与诊断信息。不兼容的插件不能从详情页安装。

“添加 → 从来源安装”用于安装未被市场收录的单个插件，支持：

- GitHub `owner/repo`；
- 不含凭证的 HTTPS Git URL；
- Git SSH URL；
- 本机目录。

Foya 将插件安装到：

```text
~/.foya/plugins/<plugin-name>
```

更新会原子替换包目录。插件的持久数据位于：

```text
~/.foya/plugin-data/<plugin-name>
```

停用或卸载插件不会删除该数据目录。

## Skills

Foya 只发现 `skills/` 的直接子目录，不递归搜索更深层的 `SKILL.md`。每个 Skill
独立校验；无效 Skill 被跳过，不影响同一插件中的其他 Skill 或 MCP Server。

插件 Skill 使用 `plugin:<plugin-name>:<skill-name>` 引用。发生同名冲突时，项目
Skill 和用户全局 Skill 优先于插件 Skill。

## MCP Server

`mcp.json` 使用规范定义的闭合 Schema：

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "tools": {
      "type": "stdio",
      "command": "./bin/server",
      "args": ["--data", "${PLUGIN_DATA}"]
    }
  }
}
```

Foya 为 stdio Server 提供：

- `PLUGIN_ROOT`：插件安装目录；
- `PLUGIN_DATA`：跨更新保留的可写数据目录。

变量只在 `args`、`env` 值和 `cwd` 中单次展开。插件不能覆盖两个保留变量。
一个 MCP Server 配置或连接失败时，其他 Server 和 Skills 仍继续加载。

## 安全边界

- 所有包内路径解析后必须留在插件根目录；
- 安装时拒绝符号链接和超出大小限制的包；
- 非本机 HTTP MCP endpoint 必须使用 HTTPS；
- 插件 MCP 配置不会写入用户的 `~/.foya/mcp.json`；
- 插件 MCP Tool 仍经过 Foya Approval Gateway；
- 项目目录不会被自动当作可信插件源。

Agent Plugins 规范不定义 Marketplace、安装来源、权限或更新界面。这些属于 Foya
客户端生命周期，但不会改变标准包格式。
