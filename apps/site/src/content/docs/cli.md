---
title: CLI 参考
description: Foya 命令行入口和常用子命令。
slug: docs/cli
---

`foya` 不带子命令时启动常驻 Go 内核。其他子命令用于无头执行任务或管理本地
配置。

以下示例假设 `foya` 已加入 `PATH`。从源码构建但未安装时，请使用
`./bin/foya`。当前 CLI 使用 Go 标准 Flag 解析，Flag 必须写在位置参数之前。

## 单实例约束

所有子命令都会打开 Foya 数据目录并获取 `kernel.lock`，包括只读取配置的
`projects`、`skills`、`mcp` 等管理命令。桌面端、daemon 或其他 CLI 已经占用同一
数据目录时，新命令会返回 `Foya data directory is already in use`。

运行管理命令前应先关闭使用该数据目录的 Foya 进程。也可以通过 `FOYA_DATA_DIR`
显式选择另一个独立实例，但该命令只会读取和修改新目录中的数据。

## 启动内核

```bash
foya
```

默认使用 Unix Domain Socket：

```text
<UserConfigDir>/foya/kernel.sock
```

目录权限为 `0700`，Socket 权限为 `0600`。

### 服务模式

```bash
foya serve [flags]
```

| 参数 | 说明 |
|---|---|
| `--listen` | TCP 监听地址；设置后进入持久服务模式 |
| `--socket` | Unix Socket 路径 |
| `--data-dir` | 持久数据目录 |
| `--auth-token-hash-file` | Bearer Token 的 SHA-256 摘要文件 |
| `--tls-cert`、`--tls-key` | TLS 证书链和私钥 |
| `--allow-plaintext` | 仅允许可信反向代理网络中的明文 TCP |

TCP 模式必须配置 Token 摘要，并且必须启用 TLS 或显式允许明文代理链路。完整示例
参见[服务端部署](./server-deployment.md)。

## 执行一次任务

```bash
foya exec [flags] <prompt>
```

常用参数：

| 参数 | 说明 |
|---|---|
| `--project` | Project ID |
| `--connection` | 模型连接 ID |
| `--model` | 模型 ID |
| `--approval` | `manual`、`auto` 或 `full_access` |
| `--image` | 图片路径，可重复提供 |

没有命令行 Prompt 时，`exec` 会从标准输入读取文本。以下两种调用等价：

```bash
foya exec "总结当前项目"
printf '%s\n' "总结当前项目" | foya exec
```

附带多张图片时重复使用 `--image`：

```bash
foya exec --image before.png --image after.png "比较这两张截图"
```

## 项目

```bash
foya projects list
foya projects add <path>
foya projects delete <id>
```

删除 Project 只删除 Foya 管理的数据和关联 Session，不删除项目目录。

## Agent 与 Skills

```bash
foya agents [--project <id>]
foya skills list [--project <id>]
foya skills enable <ref>
foya skills disable <ref>
```

## Rules 与 Memory

```bash
foya rules list [--project <id>]
foya rules add [--project <id>] <text>
foya rules edit <id> <text>
foya rules delete <id>

foya memory status
foya memory enable
foya memory disable
foya memory show [--project <id>]
foya memory set [--project <id>] <text>
foya memory clear [--project <id>]
```

## 外部能力

```bash
foya mcp list
foya mcp apply <config.json>

foya web-search show
foya web-search test <query>
foya web-search set-google <id> <cx> <api-key>

foya bot [flags]
```

`web-search set-google` 的 API Key 会出现在 Shell History 中。长期配置更适合通过
桌面设置完成。

`bot` 支持：

| 参数 | 说明 |
|---|---|
| `--app-id`、`--app-secret` | 飞书应用凭证 |
| `--connection`、`--model` | Language Connection 与 Model |
| `--project` | 绑定的 Project ID |
| `--approval` | `auto` 或 `full_access`，默认 `auto` |
| `--allow-user` | 允许的用户 Open ID，可重复提供 |
| `--allow-chat` | 允许的 Chat ID，可重复提供 |
| `--allow-all` | 允许所有来源，仅用于隔离测试 |

对应环境变量为 `FOYA_FEISHU_APP_ID`、`FOYA_FEISHU_APP_SECRET`、
`FOYA_FEISHU_CONNECTION_ID`、`FOYA_FEISHU_MODEL`、
`FOYA_FEISHU_PROJECT_ID`、`FOYA_FEISHU_APPROVAL_MODE`、
`FOYA_FEISHU_ALLOWED_USERS` 和 `FOYA_FEISHU_ALLOWED_CHATS`。两个 Allowlist
变量使用逗号分隔。

短生命周期的管理命令会禁用飞书、Cron 等长生命周期外部集成，避免只查询配置时
意外建立连接；这不会绕过上述数据目录单实例锁。
