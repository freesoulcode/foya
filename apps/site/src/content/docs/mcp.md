---
title: MCP
description: 连接 MCP Server，并将外部 Tool、Resource 和 Prompt 接入 Foya。
slug: docs/mcp
---

Model Context Protocol（MCP）允许外部进程或服务向 Foya 提供 Tool、Resource 和
Prompt。Foya 作为 MCP Client 管理连接，并把远端 Tool 接入统一 Tool Registry。

## 支持的传输

当前支持：

| Transport | 使用场景 |
|---|---|
| `stdio` | 启动本机 MCP Server 子进程 |
| `streamable_http` | 连接支持 Streamable HTTP 的远端服务 |
| `sse` | 连接使用旧 SSE Transport 的服务 |

stdio 配置需要 Command，可选 Args、Environment 和 Working Directory。远端配置
需要 URL，可选 Header 和 Bearer Token。

## 生命周期

启用 MCP Server 后，Manager 会：

1. 建立 Client Session；
2. 初始化协议能力；
3. 获取 Tool 列表；
4. 将 Tool 适配为 Foya Tool；
5. 注册到统一 Tool Registry；
6. 查询 Resource 和 Prompt 数量；
7. 持续维护连接状态。

配置被停用、删除或替换时，旧 Session 会关闭，对应动态 Tool 会从 Registry
移除。

## MCP Tool

MCP Tool 对模型表现为普通 Function Tool：

- 使用 Server 提供的 Name、Description 和 Input Schema；
- 通过 Foya Tool Registry 进入 Provider 请求；
- 调用结果转换成统一 Tool Result；
- 调用过程进入当前 Session 的事件流。

MCP Tool 不会绕过 Foya 的 Agent Runtime。模型仍只能调用当前 Step 实际暴露的
Schema。

## Resource

MCP Resource 是可读取内容，不会自动伪装成 Tool。Foya 通过专门接口：

- 列出 Server Resource；
- 按 URI 读取 Resource；
- 将结果作为明确来源的数据交给调用方。

Resource 内容属于外部数据，不能覆盖系统权限或安全规则。

## Prompt

MCP Prompt 由 Server 提供名称、参数和生成结果。Foya 可以列出 Prompt，并使用
参数请求具体内容。

Prompt 是可复用输入模板，不是系统级指令。使用 Prompt 不会改变 Session 的工具
白名单、Approval Mode 或 Sandbox Profile。

## 配置与凭证

MCP 配置保存在：

```text
~/.foya/mcp.json
```

Bearer Token 单独保存在：

```text
~/.foya/mcp-credentials.json
```

客户端读取公共配置时只返回 `has_token`，不会返回明文 Token。

配置文件使用用户私有权限，但当前凭证尚未接入操作系统 Keychain。备份和共享
`~/.foya` 时应按敏感数据处理。

## 环境变量

stdio Environment 和 HTTP Header 支持运行时环境变量展开。不要把 Secret 直接
写入项目仓库中的共享配置。

stdio Server 以本机进程运行，其进程本身可能拥有 Foya Sandbox 之外的权限。只应
连接可信程序，并审查 Command、Args 和 Environment。

## 与 Tool 权限的关系

MCP 描述远端能力，但不授予更高权限：

- Tool 是否进入请求由 Registry 和 Session 工具策略决定；
- 调用是否需要审批由 Foya Approval Gateway 决定；
- MCP Server 自己的网络和文件权限仍由其部署环境决定；
- Server 返回的文字视为外部 Tool Result。

对于远端服务，Foya 无法在本地 Sandbox 中限制服务端内部副作用。审批请求应准确
描述工具及目标资源。

## 与 Skill 的区别

| Skill | MCP |
|---|---|
| 提供任务流程和说明 | 提供协议化外部能力 |
| 由本地 Markdown 和资源组成 | 由进程或网络服务提供 |
| 加载后仍需调用已有 Tool | 可以注册新的 Tool |
| 不能执行代码本身 | Server 可以执行实际操作 |

一个 Skill 可以说明如何正确使用某个 MCP Tool，但 Skill 本身不会建立 MCP
连接。

## 故障行为

Server 连接失败时：

- 该 Server 状态会显示错误；
- 它的 Tool 不应继续作为可调用能力保留；
- 其他内置 Tool 和 MCP Server 可以继续工作；
- Foya 不会把连接错误自动解释为 Agent 任务失败，除非当前任务调用了该能力。

内核关闭时会关闭 MCP Session 和本地子进程。

## 当前边界

- 当前支持 stdio、Streamable HTTP 和旧 SSE Transport。
- 完整 MCP OAuth 尚未实现。
- Bearer Token 仍由本地文件权限保护。
- MCP Tool 的远端副作用无法由本地 Sandbox 完整隔离。
- Resource 与 Prompt 需要显式读取，不会自动注入所有模型请求。
