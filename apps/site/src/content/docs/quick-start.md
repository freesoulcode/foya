---
title: 快速开始
description: 从源码构建并运行 Foya 桌面端。
slug: docs/quick-start
---

本指南提供两条路径：直接运行 Go CLI，或启动完整桌面端。桌面开发模式会先构建
Go 内核 Sidecar，再启动 Tauri 和 Vue 开发服务器。

## 环境要求

运行内核或 CLI 只需要 Go 1.26 或更高版本。

开发桌面端还需要：

- Node.js 22.12 或更高版本
- pnpm
- Rust 工具链
- [当前平台所需的 Tauri 系统依赖](https://v2.tauri.app/start/prerequisites/)

当前桌面传输主要支持 macOS 和 Linux。Windows 虽然存在构建目标，但多数
桌面命令仍未实现。

## 获取代码

```bash
git clone https://github.com/freesoulcode/foya.git
cd foya
```

## 验证 Go 内核

```bash
make test
make build
```

`make build` 会生成 `bin/foya`。可以直接启动本地内核：

```bash
./bin/foya
```

默认监听用户配置目录中的 `foya/kernel.sock`。

## 启动桌面端

首次运行先安装 Vue/Tauri 依赖：

```bash
make fe-install
```

启动完整桌面开发环境：

```bash
make desktop-dev
```

该命令会：

1. 编译当前平台的 Go sidecar。
2. 构建供 SSH 部署使用的 Linux amd64/arm64 内核。
3. 将二进制放入 `apps/desktop/src-tauri/binaries/`。
4. 启动 Vite 开发服务器。
5. 启动 Tauri 窗口并拉起 sidecar。

## 提交第一项任务

1. 打开“设置 → 模型连接”，添加语言模型连接。
2. 导入或填写模型名称。
3. 返回聊天页，创建项目或使用无项目会话。
4. 选择模型和审批模式。
5. 输入一项可以验证结果的任务。

首次发送消息时，草稿才会被转换为持久 Session。之后的消息、工具调用和
回合状态由内核事件流驱动界面更新。

Session 的运行和持久化规则参见 [Session](./technical/session.md)。

## 使用 CLI

不启动桌面端也可以执行一次性任务。首次运行且尚未保存模型连接时，通过环境变量
提供一个 OpenAI 兼容端点：

```bash
export FOYA_PROVIDER_BASE_URL="https://your-provider.example/v1"
export FOYA_PROVIDER_API_KEY="your-api-key"
export FOYA_PROVIDER_MODEL="your-model"

./bin/foya exec "分析当前项目并列出主要模块"
```

已有模型连接和 Project 时，可以显式选择对应 ID：

```bash
./bin/foya exec \
  --connection <connection-id> \
  --model <model-id> \
  --project <project-id> \
  "分析当前项目并列出主要模块"
```

CLI 的模型连接、项目和会话数据与桌面端使用同一个 Foya 数据目录。不要同时
启动多个会写入同一数据目录的内核进程。

`exec` 使用 Go 标准参数解析，所有 Flag 必须写在 Prompt 前。完整语法参见
[CLI 参考](./cli.md)。

## 使用远程内核

家庭服务器可以在“设置 → 内核”中添加 SSH 连接。桌面端会探测 Linux 架构、
首次从同版本 GitHub Release 下载对应内核，校验后缓存、上传，并建立可自动恢复的
SSH 隧道。首次部署需要桌面端能够访问 GitHub，远端服务器不需要访问公网，也不要求
域名或证书。

公网服务器应使用带 Bearer Token 的 HTTPS。完整要求和部署步骤参见
[服务端部署](./server-deployment.md)。
