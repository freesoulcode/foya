---
title: Artifact
description: 了解 Foya 如何保存、校验和传递 Session 二进制附件。
slug: docs/technical/artifacts
---

Artifact 是 Session 拥有的二进制资源。它用于用户图片输入，以及 Tool 生成的图片、
视频和文件。Message 只保存引用，原始字节位于独立文件存储中。

## 为什么独立存储

二进制内容不适合直接写入 Event JSON 或 SSE：

- Base64 会增加体积；
- 每次读取历史都会重复加载；
- 大对象会放大数据库和事件传输；
- Provider 只在具体请求中才需要字节。

因此 Event Store 保存 Attachment Ref，Artifact Store 保存实际文件。

## Attachment Ref

引用包含：

- ID；
- 文件名；
- Kind；
- Media Type；
- 字节数；
- 宽度和高度；
- SHA-256。

ID 和 Session ID 都经过安全字符校验，不能用路径分隔符访问其他目录。

## 写入流程

图片写入时：

1. 限制输入读取大小；
2. 根据文件签名确认 PNG、JPEG、GIF 或 WebP；
3. 解码尺寸并限制像素数；
4. 必要时将最长边缩放到 2048；
5. 计算规范化字节的 SHA-256；
6. 原子写入 Binary 与 Metadata；
7. 用户消息提交成功后标记为 Committed。

普通文件限制为 20 MiB。视频使用独立的写入路径，验证 `video/*` Media Type，并在
流式写盘时计算 SHA-256，避免为大视频额外分配一份完整内存副本。视频上限与生成
Adapter 一致，为 500 MiB。

限制为：

| 限制 | 数值 |
|---|---|
| 单张输入图片 | 20 MiB |
| 单个普通文件 | 20 MiB |
| 单个视频 | 500 MiB |
| 单 Turn 附件总量 | 50 MiB |
| 单 Turn 附件数量 | 8 |
| 最大像素数 | 4000 万 |
| 规范化最长边 | 2048 |

## 两阶段提交

上传 Artifact 与提交消息是两个步骤。未提交 Artifact 可以删除；已经绑定消息的
Artifact 不能通过普通删除接口单独移除。

这防止历史 Message 引用已经不存在的二进制。

## 读取校验

每次读取都会：

- 确认 Metadata 中的 ID 与请求一致；
- 读取当前 Session 目录；
- 重新计算 SHA-256；
- 拒绝 Hash 不匹配的内容。

Provider 请求需要图片时才读取字节。Event、REST 列表和客户端状态只传引用。

桌面端预览同时检查 Artifact Kind、Media Type 和文件扩展名。因此，即使普通文件的
Media Type 是 `application/octet-stream`，常见图片和视频扩展名仍会使用对应媒体
组件预览。

## Tool 产物

Tool Result 可以返回内存图片，或返回已经写入 Store 的 `artifact_ref`。Agent Engine
会持久化并提交引用，再把 Attachment Ref 放入 Tool Message。普通对话中的
`generate_image` 和 `generate_video` 使用后一种方式。

如果持久化失败，模型不会收到一个虚假的可用引用。

## Session 分支与删除

分支 Session 会复制历史中引用的 Artifact，生成新 Session 自己的引用。源 Session
删除后不会破坏分支。

删除 Session 时会删除：

```text
<data-dir>/artifacts/<session-id>/
```

## 与 Canvas Asset 的区别

Session Artifact 和 Canvas Asset 使用不同存储：

| Session Artifact | Canvas Asset |
|---|---|
| 属于消息历史 | 属于 Canvas Document |
| 支持规范化图片、视频和普通文件 | 支持图片和视频 |
| 图片/文件 20 MiB，视频 500 MiB | 单 Asset 500 MiB |
| 随 Session 删除 | 随 Canvas 删除 |

二者不能只通过 ID 互换。

## 当前边界

- 用户上传入口当前只接受图片；视频由生成 Tool 写入。
- Artifact 保存在本地文件系统，不进入 SQLite Blob。
- 没有跨 Session 内容去重。
- 已提交 Artifact 的生命周期与 Session 绑定。
