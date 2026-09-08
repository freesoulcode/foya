---
title: 媒体生成
description: 了解 Canvas 如何调用图片和视频模型并保存生成结果。
slug: docs/technical/media-generation
---

媒体生成是 Canvas 上的显式操作。它使用独立 Image 或 Video Connection，不经过
普通聊天 Agent Loop。

## 通用流程

1. 客户端提交 Canvas ID、Generation Node、Output Node 和 Expected Revision。
2. Kernel Service 校验 Document 没有并发变化。
3. 校验 Node 类型与 Generation Mode。
4. 解析 Connection 和 Model。
5. 收集相连 Text Prompt 和参考图片。
6. 调用对应生成 Adapter。
7. 把结果写为 Canvas Asset。
8. 更新 Node Status 和 Output Asset ID。
9. 广播新的 Canvas Revision。

失败时，Generation Node 和 Output Node 都会进入 Error，并保存可见错误说明。

## Connection 选择

优先级为：

1. API 请求显式 Connection；
2. Generation Node 中的 Connection；
3. 对应类型的全局 Default Model。

Model 优先使用 Generation Node 配置，缺失时使用对应默认模型。

图片生成只接受 Image Connection，视频生成只接受 Video Connection。模型存在显式
能力设置时，还必须声明对应 Generation Capability。

## Prompt 组装

Generation Node 自身的 Prompt 首先加入。

所有指向该 Node 的 Text Node 按 Edge 和 Node 顺序追加，以空行分隔。没有任何
Prompt 时请求失败。

图片生成可以读取多个相连 Image Node 作为 References。视频生成当前只使用第一张
相连参考图。

## 图片生成

Image Adapter 使用 OpenAI Images 接口：

- 没有 Reference 时调用 Generate；
- 存在 Reference 时调用 Edit；
- 每次请求生成一个结果；
- Aspect Ratio 映射到横向、纵向或方形尺寸；
- Quality 原样传给兼容端点。

Provider 可以返回 Base64 或下载 URL。URL 结果会由 Foya 下载，最大 100 MiB，并
验证响应是图片。

Canvas 图片生成总 Timeout 为 5 分钟。

## 视频生成

Video Adapter 当前支持：

| Protocol | 创建与查询协议 |
|---|---|
| `seedance` | `/api/v3/contents/generations/tasks` |
| `minimax_h3` | `/v2/video_generation` |

请求创建异步 Job 后，Adapter 每两秒轮询状态，直到：

- 成功并得到 Result URL；
- 失败、取消或过期；
- 调用 Context 取消；
- 超过 Canvas 的 30 分钟 Timeout。

Duration 接受 4 到 15 秒，其他值使用 5 秒默认值。生成视频最大下载 500 MiB。

下载 URL 与配置 Base URL 同源时携带 Authorization；外部结果 URL 不转发 API
Key。

## Asset 提交

生成结果不会直接嵌入 Canvas JSON。Foya 先：

1. 验证 Media Type；
2. 计算 SHA-256；
3. 写入 `canvas-assets/<canvas-id>/`；
4. 更新 Canvas Asset 列表；
5. 更新 Output Node。

Asset 写入或 Document 更新失败时，Generation 显示 Error。

## 并发

生成请求携带 Expected Revision。开始前若 Canvas 已变化，请求被拒绝。

生成期间 Canvas 仍可能被其他客户端修改。完成后 Kernel Service 重新读取当前 Document，
使用最新 Revision 更新相关 Node，避免用启动时旧 Document 覆盖其他修改。

## 与语言 Provider 的区别

Image/Video Adapter 不实现普通 Agent Provider：

- 不接收对话历史；
- 不接收 Tool Schema；
- 不参与 Context Compaction；
- 使用专门的生成请求和结果下载逻辑。

Connection Catalog 复用身份、Base URL、API Key 和 Model Settings，但运行协议不同。

## 当前边界

- 图片使用 OpenAI Images 兼容接口。
- 视频仅支持 Seedance 和 MiniMax H3 两种协议。
- 媒体生成不记录 Language Model Usage。
- 生成 Job 不跨 Kernel 重启恢复。
- 当前没有后台生成队列或并发配额。
