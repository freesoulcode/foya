---
title: Canvas
description: 了解 Foya 的持久视觉工作区、节点图、Revision 和 Asset 模型。
slug: docs/technical/canvas
---

Canvas 是 Foya Studio 中的持久视觉工作区。它保存文本、媒体和生成配置节点，并用
Edge 表达引用关系。

Canvas 不属于聊天消息投影。它有独立 Document、Asset Store、Revision 和事件流。

## Document

Canvas Document 包含：

- ID 与 Title；
- 可选 Session ID 和 Project ID；
- Revision；
- Nodes；
- Edges；
- Assets；
- Viewport；
- Background；
- 创建和更新时间。

Document Metadata 保存在 `canvases.json`，原始媒体位于 `canvas-assets/`。

## Node

支持四类 Node：

| Type | 用途 |
|---|---|
| `text` | Prompt 或说明文本 |
| `image` | 展示图片 Asset |
| `video` | 展示视频 Asset |
| `generation` | 保存图片或视频生成参数 |

Node 还包含位置、尺寸、旋转和 Z Index。Geometry 必须是有限数值，尺寸范围为
24 到 100000。

Generation Node 可以配置 Mode、Connection、Model、Aspect Ratio、Quality、
Count 和 Duration。

## Edge

Edge 连接两个已存在 Node，可以标记：

- `reference`；
- `variation`；
- `output`。

不能连接 Node 自己，Edge ID 不能重复。

生成时，指向 Generation Node 的 Text Node 会加入 Prompt；Image Node 可以作为
参考图。

## Revision

所有更新都要求客户端提交 `expected_revision`。

```text
读取 Revision 8
  → 提交 Expected Revision 8
  → 成功写入 Revision 9
```

如果其他客户端已经写入 Revision 9，旧请求返回 Revision Conflict，不会覆盖新
内容。

这是一种乐观并发控制，适合桌面编辑与 Agent Tool 同时修改同一 Canvas。

## Asset

Canvas Asset 支持图片和视频，记录 Name、Kind、Media Type、Bytes、尺寸、SHA-256
和创建时间。

上传时：

- 最多读取 500 MiB；
- 优先检测真实 Content Type；
- 只接受 `image/*` 和 `video/*`；
- 图片尝试读取尺寸；
- 计算 SHA-256；
- 先写二进制，再原子更新 Document。

读取时重新校验 SHA-256。

## Agent Tool

Canvas Tool 使用 Deferred Exposure：

| Tool | 作用 |
|---|---|
| `canvas_inspect` | 读取当前 Document |
| `canvas_add_text` | 添加 Text Node |
| `canvas_add_generation` | 添加 Generation Node |
| `canvas_connect` | 创建 Edge |
| `canvas_move` | 批量移动 Node |

修改 Tool 必须提供最新 Revision。Agent 不直接写 `canvases.json`。

## 事件

Canvas 使用独立 Topic：

```text
canvas:<canvas-id>
```

创建、更新和删除会广播完整 Document。事件中的 Seq 使用 Canvas Revision，而不是
SQLite Event Seq。

客户端可通过独立 SSE 订阅 Canvas 更新。

## 生成状态

图片或视频生成会更新相关 Generation Node 与 Output Node：

```text
idle → running → success
               ↘ error
```

生成成功后，结果写入 Canvas Asset，并将 Asset ID 绑定 Output Node。

内核启动时，如果持久 Document 中仍有 `running` Node，会改为 `error`，说明上次
生成因重启中断。任务不会自动重放。

## 删除

删除 Canvas 会同时删除：

- `canvases.json` 中的 Document；
- 对应 `canvas-assets/<canvas-id>/`；
- 客户端中的 Canvas 视图。

Project 或 Session 关联用于筛选和默认配置，不代表 Canvas Asset 与 Session
Artifact 共用存储。

## 当前边界

- Canvas Metadata 使用 JSON 文件，不在 SQLite。
- 每次更新提交完整 Nodes 或 Edges 集合。
- 不支持多人协同 CRDT，冲突通过 Revision 拒绝。
- 单个 Asset 最大 500 MiB。
- 生成任务不会跨 Kernel 重启恢复。
