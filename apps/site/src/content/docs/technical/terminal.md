---
title: Terminal
description: 了解交互式终端与 Agent 后台命令的资源生命周期。
slug: docs/technical/terminal
---

Foya 包含两类命令执行资源：

- **Interactive Terminal**：由用户通过 Workbar 操作的 PTY。
- **Background Command**：由 Agent `bash` Tool 或用户转入后台的进程。

两者都归 Session 所有，但使用不同接口和状态模型。

## Interactive Terminal

创建 Terminal 时需要 Session、工作目录和初始行列数。Unix 平台会：

1. 选择 `$SHELL`，缺省使用 `/bin/sh`；
2. 将工作目录设为 Session Project；
3. 设置 `TERM=xterm-256color`；
4. 创建指定尺寸的 PTY；
5. 启动 Shell 并异步读取输出。

Terminal Ref 是资源身份，所有 Attach、Input、Resize、Stop 请求都必须同时匹配
Session ID。

## 输出与 Replay

Terminal 为每个资源维护独立 Seq：

- 每次输出或退出产生 Data Event；
- 保留最近 1 MiB 输出 Buffer；
- 保留最近 2048 个 Event；
- Attach 返回当前 Snapshot；
- 订阅可以从指定 Seq 回放后续事件。

Terminal Event 通过独立 SSE 发送，不写入普通 Session Event Log。

进程退出后，Snapshot 记录 Running、Exit Code、Buffer 和最后 Seq。

## 生命周期

Terminal 资源由 Kernel 持有，不依赖某个 WebView 持续打开。关闭面板后可以再次
Attach。

删除 Session 时，Terminal Manager 会停止并移除该 Session 的全部 Terminal。
Kernel 重启不会恢复 PTY。

Windows 当前返回 Terminal Unavailable。

## Agent Background Command

`bash` Tool 支持：

- 直接以后台方式启动；
- 把正在运行的前台 Tool Call 转为后台；
- 查看状态；
- 停止进程。

每个 Background Command 保存：

- Command ID；
- Session ID；
- 命令文本；
- PID；
- 运行与退出状态；
- 有界 Stdout 和 Stderr；
- 启动时间；
- 由 Agent 或用户转入后台的来源。

## 前台转后台

普通 `bash` 默认等待进程完成。用户可以在运行期间请求 Background：

1. Runtime 找到 Session 与 Tool Call 对应的活跃命令；
2. 标记为用户转入后台；
3. 前台 Tool Call 立即返回后台状态；
4. 进程继续由 Kernel 管理；
5. 后续通过 Command ID 查询或停止。

Reveal 允许界面接管仍在运行的前台命令状态，但不会改变其 Sandbox Profile。

## 输出限制

底层进程只保留最近 1 MiB Stdout 和 Stderr。返回给 Tool 或 API 时还会应用更小的
行数与字节限制，并优先保留尾部。

因此 Background Command 不是完整日志归档。需要长期日志时，命令应自行写入项目
文件。

## Timeout 与取消

前台 `bash` 默认 Timeout 为 120 秒，调用方可以设置最长一天。后台命令默认无
Timeout，也可以显式设置。

Turn 取消会停止仍属于前台调用的进程树。已经明确转入后台的命令拥有独立 Context，
直到完成、超时、用户停止或 Session 删除。

## 权限

Agent `bash` 在启动前必须经过 Approval Gateway，并使用当前 Sandbox Profile。

Interactive Terminal 是用户直接操作的资源，不通过模型 Tool Approval。它以宿主
Shell 运行，因此用户输入的命令由用户自行负责。

## 当前边界

- Interactive Terminal 仅在非 Windows 实现。
- Terminal 状态只在内存中，重启后不可恢复。
- 输出 Buffer 有界，不保证保存完整日志。
- 后台命令不跨 Kernel 重启恢复。
- Terminal 与 Agent `bash` 是不同资源，不能相互 Attach。
