# Foya

一个开源、非商业、BYOK(用户自带 API Key)的个人 agent 系统。

核心是一个 Go 编写的**独立常驻内核**(个人 agent 后端),Tauri 桌面端与 CLI 是它的客户端;
用户可从多设备、多客户端、多会话连接同一内核。内核持有全部会话状态(日志即真相),
客户端无状态、从事件流投影。

## 文档

- [架构](./docs/架构.md) — 宏观四层分层
- [节点抽象设计](./docs/节点抽象设计.md) — 内核核心节点接口
- [前端架构](./docs/前端架构.md) — Vue 前端分层

## 目录结构

```
cmd/foya/          入口:内核 daemon + exec 子命令
internal/
  kernel/          内核组合根 (App)
  agent/           回合引擎 AgentLoop、Turn
  broker/          事件总线 Broker[T](两级投递)
  event/           Event 类型与单调序号
  session/         会话与多会话管理
  state/           日志即真相:Event Log + 投影
  tool/            工具接口、注册表、路由
  skill/           内置、全局和项目级 Skills 发现与启停
  mcpclient/       MCP tools/resources/prompts 与传输适配
  websearch/       原生搜索、Google CSE 与 DuckDuckGo 路由
  approval/        审批网关与策略
  sandbox/         工具执行隔离
  provider/        LLM provider 抽象与 OpenAI 兼容实现
  credential/      凭证存储(keychain / 加密文件降级)
  backend/         传输无关业务层:多连接、多会话、事件扇出
  server/          REST + SSE
  protocol/        线格式类型(Submission / Event DTO)
  config/          配置
```

## 开发

```
make build   # 编译内核二进制到 bin/foya
make run     # 启动内核
make test    # 运行测试
```

## 已实现

- Go 常驻内核、本地 Unix socket、REST + SSE。
- 多会话 Agent Loop、工具调用、审批、取消、队列与上下文压缩。
- OpenAI 兼容模型连接和 BYOK 配置。
- `bash`、`read`、`write`、`edit`、Skills、Web Search 与 WebFetch。
- MCP stdio、Streamable HTTP、legacy SSE，以及 tools/resources/prompts。
- `foya exec` 和 Skills、MCP、Web Search 管理命令。
- Tauri + Vue 桌面端及对应设置界面。

完整 MCP OAuth、客户端归属 MCP 和富媒体 artifact 仍在开发中。
