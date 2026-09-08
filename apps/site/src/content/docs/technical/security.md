---
title: 安全模型
description: 了解 Foya 的信任边界、权限层次、输入处理和已知风险。
slug: docs/technical/security
---

Foya 是能够读取文件、运行命令和访问网络的本地 Agent。安全模型的目标不是证明
模型永远不会犯错，而是限制模型错误判断能够直接产生的副作用。

## 信任对象

Foya 默认信任：

- 启动 Kernel 的本地用户；
- 用户显式保存的 Connection 和扩展配置；
- 用户明确选择的 Full Access 环境。

Foya 不默认信任：

- 模型输出；
- 网页内容；
- Tool Result；
- 项目仓库文件；
- MCP Server 返回内容；
- Skill、Rule 和 Agent Definition 中的外部数据；
- 消息渠道中的远端发送者。

## 权威层次

上下文内容不能自行授予能力：

```text
系统安全与实际执行边界
  > Session 权限和 Tool 白名单
  > 用户 Rules
  > Project Guidance / Skill / Memory
  >网页和 Tool Result
```

即使较低层内容要求跳过审批，Approval Gateway 和 Sandbox 仍按真实配置执行。

## 模型与副作用

模型只能返回文本和 Tool Call。产生副作用必须经过：

1. Tool 是否注册并对当前 Agent 可见；
2. Tool 参数校验；
3. Hook PreToolUse；
4. Approval Gateway；
5. Sandbox 或 Tool 自身边界；
6. 结果记录与审查。

Tool Description 和 JSON Schema 只是模型接口，不是授权凭证。

## Prompt Injection

项目说明、网页、Browser Snapshot 和 Tool Result 都可能包含诱导模型越权的文字。

Foya 使用以下防护：

- 在 Prompt 中标记用户可控和不可信来源；
- 对项目说明检测覆盖式措辞、敏感值和控制字符；
- 转义上下文标签；
- 不允许这些内容修改实际权限；
- Browser Result 明确声明页面内容不是指令；
- Memory 自动提取明确忽略证据中的指令。

这些措施降低风险，但不能保证模型完全不受恶意文本影响。最终安全依赖工具权限和
执行边界。

## 文件系统

非 Full Access 的 Agent 进程使用 Workspace Write Profile：

- 项目目录、临时目录和缓存可写；
- 项目内 `.git`、`.agents` 和 `.foya` 只读；
- 其他位置只读；
- 网络默认关闭。

Read Tool 当前允许读取宿主可见文件，因此敏感文件保护不能只依赖“读取无需审批”。
用户应避免在 Agent 可访问账户中存放无保护 Secret。

## 网络

Web Search 和 Fetch 需要 Network Approval。

Web Fetch 拒绝私有、Loopback、Link-local、Multicast 和 Unspecified Address，并
重新验证 Redirect，降低 SSRF 风险。

Provider、MCP Server、Hook 和用户直接运行的 Terminal 有各自网络边界，不一定受
Web Fetch 规则限制。

远程 TCP 服务要求 Bearer Token 摘要，并默认要求 TLS。只有部署在同机或隔离容器
网络中的可信反向代理后面，才应显式启用明文 TCP。SSH 模式只让内核监听远端
Loopback，并通过 OpenSSH 本地端口转发访问。

## 凭证

公共 API 返回 Connection、MCP 和 Search 配置时会隐藏明文 Secret。

当前凭证仍保存在用户私有文件中，尚未统一接入 OS Keychain。日志和事件不应写入
API Key，但用户自定义 Hook 或外部程序仍可能主动读取环境。

Hook 环境会过滤名称包含 `API_KEY` 的变量，但这不是完整 Secret Manager。

## Artifact 与路径

Artifact ID 和 Session ID 只允许安全字符，读取路径固定在所属 Session 目录。
二进制读取会重新校验 SHA-256。

Project 路径注册时解析绝对路径和符号链接。Skill Resource 读取验证最终路径仍在
Skill Package 根目录。

## 外部扩展

### MCP

stdio Server 是本地高权限进程；远端 MCP 的服务端副作用无法由本地 Sandbox
控制。只能连接可信 Server。

### Hooks

Hook 直接通过本机 Shell 运行，不经过 Tool Approval 或 Sandbox。Hook 配置应视为
本地代码。

### Channels

消息渠道必须使用 User 或 Chat Allowlist。Allowlist 只控制输入来源，不能替代
Agent 权限。

## Full Access

Full Access：

- 自动批准 Tool 操作；
- 使用完整文件系统 Profile；
- 允许进程网络。

这会显著扩大模型错误和 Prompt Injection 的影响范围。应只在额外隔离、可恢复且
不含敏感凭证的环境中使用。

## 恢复不是安全控制

文件审查和历史回退可以恢复部分文本文件，但不能撤销：

- 网络请求；
- 数据库外部写入；
- Shell 修改但未记录的文件；
- 已发送消息；
- 远端 MCP 副作用；
- 删除或覆盖后没有快照的数据。

审批应在执行前控制风险，不能依赖事后撤销。

## 当前边界

- 系统面向可信本地单用户，不是多租户隔离环境。
- 没有统一 OS Keychain 集成。
- Auto Approval 依赖模型判断。
- Hook 和第三方 MCP 可能拥有宿主权限。
- 一个远程访问令牌可以访问该实例的全部会话；不要向互不信任的用户共享。
- 安全边界仍需要操作系统账户、容器或虚拟机提供外层隔离。
