---
title: REST 与事件流
description: 了解客户端与 Foya 内核之间的 HTTP、SSE 和错误协议。
slug: docs/technical/transport
---

Foya 使用 HTTP 作为客户端与 Go 内核之间的统一应用协议：

- REST 负责命令和查询；
- Server-Sent Events（SSE）负责持续状态更新；
- 二进制内容使用独立 HTTP 响应。

协议与底层 Listener 分离。本地桌面端、SSH 隧道和 HTTPS 远程连接使用同一套
HTTP 语义。

## 请求方向

REST 请求用于发起明确操作，例如：

- 创建或更新 Session；
- 提交 Turn；
- 修改队列；
- 响应审批和用户问题；
- 上传或读取 Artifact；
- 管理 Connection、Project、Rule、Memory 和 Skill；
- 启动 Terminal、SubAgent、Automation 或 Canvas 生成。

写请求使用 JSON，并对关键结构拒绝未知字段。错误统一返回：

```json
{
  "code": "error_code",
  "message": "human-readable detail"
}
```

HTTP Status 表达错误类别，`code` 用于客户端稳定判断。

## 事件方向

Session SSE 端点持续发送：

```text
id: <event-seq>
event: <event-kind>
data: <event-json>
```

事件覆盖：

- 消息和推理增量；
- Tool 生命周期；
- 审批与提问；
- Session、Queue 和 Usage 更新；
- Compaction；
- SubAgent；
- Workflow；
- Browser Action；
- Turn 完成和错误。

客户端不应从事件到达时间推断顺序，应使用 Event Seq 和 Run ID。

## 快照与增量

客户端初始化通常遵循：

1. 通过 REST 获取 Session 列表；
2. 获取目标 Session 的持久 History 和当前 Queue；
3. 建立 SSE 订阅；
4. 把后续增量应用到本地视图。

SSE 适合实时增量，但不是唯一事实来源。网络中断或客户端处理过慢时，应重新读取
持久快照，而不是假设每个 Delta 都已收到。

## Replay

持久 Event 支持按序号读取。部分事件流端点接受最后已知序号或 `after` 参数，用于
补发后续事件。

高频 `message_delta` 和 `reasoning_delta` 只存在于内存 Broker，不进入持久日志。
因此重连只能恢复最终完成消息，不能逐 Token 重播原始生成动画。

## Event Broker

内核 Broker 按 Topic 管理订阅者，例如：

```text
session:<session-id>
canvas:<canvas-id>
terminal:<session-id>:<terminal-ref>
```

Broker 有两种投递策略：

| 策略 | 用途 |
|---|---|
| 普通 Publish | 高频、可由最终状态恢复的 Delta |
| Must Deliver | 完成、审批和关键状态事件 |

普通 Publish 在订阅者缓冲区已满时可以丢弃。Must Deliver 会进行有界等待，但不会
允许一个慢客户端无限阻塞整个内核。

## Terminal SSE

Terminal 使用独立事件流。每个 Terminal 资源拥有自己的递增 Seq，并保留有限数量
的进程内事件用于 Attach 和 Replay。

Terminal Seq 与数据库 Event Seq 不属于同一序列。客户端必须按资源分别跟踪。

## Canvas SSE

Canvas 使用 Revision 作为更新边界。客户端提交修改时携带 Expected Revision，
服务端检测并发冲突；成功后广播新的 Document。

Canvas Event 不进入普通 Session Message History。

## 二进制传输

图片和媒体不会编码进普通 Event JSON：

- 上传使用 Multipart；
- 下载使用原始 Media Type；
- 消息和 Canvas Document 只保存 Metadata 与引用；
- Tauri Host 使用字节请求接口传递内容。

这避免大型 Base64 Payload 挤占 SSE 和 Event Store。

## 本地传输

桌面模式下，Go Kernel 监听：

```text
<user-config-dir>/foya/kernel.sock
```

Socket 所在目录使用 `0700`，Socket 使用 `0600`。启动时：

1. 检查现有 Socket 是否仍有进程监听；
2. 活跃时拒绝启动第二个 Kernel；
3. 无监听时删除陈旧 Socket；
4. 创建新的 Unix Listener。

这同时防止两个内核并发修改同一数据目录。

## Tauri Bridge

Rust Host 使用 Hyper Unix Connector 发送 HTTP 请求。短 REST 响应会完整读取后
返回 WebView；SSE Body 则按行解析，将每个 `data:` 帧发送到 Tauri Channel。

Vue 代码只调用 Tauri Command，不直接连接 Unix Socket。这让本机路径和 Socket
实现停留在 Rust 边界。

## SSH 模式

家庭服务器推荐使用桌面端的 SSH 模式。Rust Host 使用系统 OpenSSH：

1. 探测远端 Linux 架构和 Bubblewrap；
2. 从同版本 Release 下载并校验匹配架构的内核，或复用本地缓存；
3. 上传匹配的静态内核二进制；
4. 在远端回环地址启动带 Bearer Token 鉴权的内核；
5. 创建本地端口转发，并在隧道退出后自动重连。

该模式不开放远端公网端口，也不要求域名和 TLS 证书。

## TCP、TLS 与鉴权

`foya serve --listen <host:port>` 启用 TCP 服务模式。TCP 模式要求：

- 配置一个至少 32 字符访问令牌的 SHA-256 摘要；
- 同时提供 TLS 证书和私钥；或者仅在可信反向代理网络内显式设置
  `--allow-plaintext`；
- 除 `GET /healthz` 外，所有请求都携带 `Authorization: Bearer <token>`。

Token 使用恒定时间摘要比较。`GET /readyz` 受鉴权保护，可用于验证完整访问链路。
公网部署、Caddy 和 systemd 示例参见[服务端部署](../server-deployment.md)。

## API 稳定性

当前 REST 与 SSE 主要服务同仓库桌面端和 CLI，不承诺独立公共 API 的长期版本
兼容性。

Wire DTO 与 HTTP Handler 位于 `internal/server`。当前尚未提供：

- OpenAPI 文档；
- API Version Prefix；
- 弃用周期；
- 独立 SDK；

外部集成应优先使用 CLI、MCP 或受支持 Channel，而不是直接绑定内部端点。

## 当前边界

- 本地主要使用 Unix Domain Socket。
- 家庭服务器可使用 SSH 自动部署和端口转发。
- 公网单租户服务可使用内置 TLS，或在可信反向代理后使用明文 TCP。
- SSE 不保证高频 Delta 永不丢失。
- 二进制资源不进入普通 SSE。
- 当前 API 面向单个 Kernel 实例，不提供多租户授权模型。
