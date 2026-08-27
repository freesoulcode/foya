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
  approval/        审批网关与策略
  sandbox/         工具执行隔离
  provider/        LLM provider 抽象(先 mock)
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

## 状态

脚手架阶段:仅搭建工程结构与核心接口骨架,业务逻辑尚未实现,模型接入先用 mock/echo。
