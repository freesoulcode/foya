---
title: 模型连接
description: 配置语言、图片和视频模型连接。
slug: docs/model-connections
---

Foya 使用 Connection 表示一个模型账号或兼容端点。Session 保存
`connection_id` 和 `model`，因此不同会话可以使用不同服务。

## 连接类型

| 类型 | 用途 |
|---|---|
| Language | 聊天、工具调用、标题生成和上下文压缩 |
| Image | 创作画布中的图片生成 |
| Video | 创作画布中的视频生成 |

语言模型连接通过 OpenAI 兼容接口工作。图片和视频连接使用各自的生成适配器，
不能混用于普通会话。

## 添加语言模型连接

在“设置 → 模型连接”中填写：

- 名称：仅用于界面识别。
- Base URL：模型服务的 OpenAI 兼容地址。
- API Key：该服务的访问凭证。
- 类型：普通会话请选择 Language。

保存后可以拉取模型列表。服务未提供完整模型元数据时，可手工补充模型能力。

## 模型能力

Foya 会按模型保存以下能力声明：

- 上下文窗口
- 最大输入和输出 Token
- 图片输入
- 图片、视频或音频生成
- Tool Calling
- 原生 Web Search
- 可用推理强度

这些配置会影响界面选项和请求校验。例如模型明确不支持图片输入时，带图片的
会话消息会在请求模型前被拒绝。

## 默认模型

默认模型按用途分别配置：

- Language：新会话默认模型。
- Fast：自动标题等短任务优先使用。
- Image：创作画布默认图片模型。
- Video：创作画布默认视频模型。

Session 创建后会保存具体连接和模型。修改全局默认值不会自动改写已有 Session。

Provider 接口、Route 隔离和 Usage 的技术说明参见
[Provider 与模型路由](./technical/providers.md)。

## 凭证边界

读取连接列表时，API 不返回明文密钥，只返回是否已配置密钥。但当前实现仍将
连接目录保存在用户私有数据目录的 `connections.json` 中，依赖目录和文件权限
保护；系统 Keychain 尚未接入。
