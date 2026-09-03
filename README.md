# Foya

一个开源、非商业、BYOK(用户自带 API Key)的个人 agent 系统。

核心是一个 Go 编写的**独立常驻内核**(个人 agent 后端),Tauri 桌面端与 CLI 是它的客户端;
用户可从多设备、多客户端、多会话连接同一内核。内核持有全部会话状态(日志即真相),
客户端无状态、从事件流投影。

## 文档

- [架构](./docs/架构.md) — 宏观四层分层
- [节点抽象设计](./docs/节点抽象设计.md) — 内核核心节点接口
- [前端架构](./docs/前端架构.md) — Vue 前端分层
- [自定义智能体](./docs/自定义智能体.md) — 用户级/项目级 Agent 定义与派工

## 目录结构

```
cmd/foya/          入口:内核 daemon + exec 子命令
internal/
  kernel/          内核组合根 (App)
  agent/           回合引擎 AgentLoop、Turn
  agentdef/        用户级/项目级 Agent 定义发现
  subagent/        Child Session、并发调度与结果回传
  broker/          事件总线 Broker[T](两级投递)
  event/           Event 类型与单调序号
  session/         会话与多会话管理
  state/           日志即真相:Event Log + 投影
  tool/            工具接口、注册表、路由
  skill/           内置、全局和项目级 Skills 发现与启停
  mcpclient/       MCP tools/resources/prompts 与传输适配
  channel/         外部消息渠道公共契约与平台适配器
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

## 飞书 Bot

`foya bot` 通过飞书长连接接收消息，无需公网回调地址。它会同时启动
Foya 内核和本地 HTTP/socket 服务，因此桌面端可以连接同一个内核；不要再
单独启动第二个 `foya` 进程。

1. 在飞书开发者后台创建企业自建应用并开启机器人能力。
2. 开通应用身份权限：
   `im:message.p2p_msg:readonly`、`im:message.group_at_msg:readonly`、
   `im:message:send_as_bot`、`im:message.reactions:write_only`；需要处理图片时
   再开通 `im:resource`。
3. 在桌面端“设置 → 消息渠道 → 飞书”填写凭证、模型和访问白名单，启用后保存。
   也可以用 CLI 启动：

```bash
export FOYA_FEISHU_APP_ID=cli_xxx
export FOYA_FEISHU_APP_SECRET=xxx
export FOYA_FEISHU_ALLOWED_USERS=ou_xxx,ou_yyy
export FOYA_FEISHU_ALLOWED_CHATS=oc_xxx

make build
./bin/foya bot
```

4. 保持进程运行，在“事件与回调”中选择长连接，订阅
   `im.message.receive_v1`，然后发布应用。

也可以重复传入 `--allow-user`、`--allow-chat`。只有明确用于隔离测试的应用才
应使用 `--allow-all`，因为 Foya Agent 可以读取本机文件并执行工具。群聊默认
只响应 @bot 的消息；发送 `/new` 可开启新会话，发送 `/stop` 可中止当前任务。
文本和图片消息会进入 Foya，回复以完整 Markdown 富文本发送，长回复自动拆分。

模型默认使用桌面端中配置的默认语言模型，也可以通过
`--connection`、`--model`、`--project` 指定。Bot 默认采用 `auto` 审批；
如需完全放开工具权限，必须显式传入 `--approval full_access`。

## 已实现

- Go 常驻内核、本地 Unix socket、REST + SSE。
- 飞书 Bot 长连接、访问白名单、会话续接、流式回复与图片输入。
- 多会话 Agent Loop、并发子 Agent、工具调用、审批、取消、队列与上下文压缩。
- OpenAI 兼容模型连接和 BYOK 配置。
- `bash`、`read`、`write`、`edit`、Skills、Web Search 与 WebFetch。
- MCP stdio、Streamable HTTP、legacy SSE，以及 tools/resources/prompts。
- `foya exec` 和 Agents、Skills、MCP、Web Search 管理命令。
- Tauri + Vue 桌面端及对应设置界面。
- 图片 Artifact、用户图片输入、工具图片回灌与 OpenAI 多模态请求。
- 创作画布中的 OpenAI 兼容图片与视频生成，支持文字提示和参考图。

完整 MCP OAuth、客户端归属 MCP 和更多富媒体类型仍在开发中。
