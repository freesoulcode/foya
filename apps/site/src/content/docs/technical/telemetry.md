---
title: OpenTelemetry
description: 配置 Foya 的 Trace、Metrics 和 OTLP 导出。
slug: docs/technical/telemetry
---

Foya 使用 OpenTelemetry 记录 Agent 运行时行为。遥测默认关闭，启用后通过
OTLP/HTTP 批量导出，不经过生命周期 Hook。

## Trace 模型

每个 Turn 是一个根 Span：

```text
foya.turn
├── foya.llm.request
├── foya.tool
│   ├── foya.approval.wait
│   └── foya.hook
├── foya.compaction
└── foya.subagent
```

Agent Loop 中每次流式 Provider 请求都会产生独立 `foya.llm.request`，包括模型、
完成原因、Token Usage、耗时和首 Token 延迟。工具、审批、Hook、压缩和子 Agent
Span 会继承当前 Trace Context。标题生成和后台 Memory 维护等旁路模型请求暂不单独
生成 LLM Span。

模型 Span 使用 `gen_ai.*` OpenTelemetry 属性，并保留 `session.id`、
`foya.run.id`、`foya.request.id` 等 Foya 关联字段。

## 启用

Foya 使用标准 `OTEL_EXPORTER_OTLP_*` 环境变量配置传输：

```bash
export FOYA_OTEL_ENABLED=true
export FOYA_OTEL_TRACES_ENABLED=true
export OTEL_SERVICE_NAME=foya
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
```

`FOYA_OTEL_ENABLED` 是总开关，默认关闭。Trace 子开关默认开启，但只有总开关为
`true` 时才会创建和导出 Span。

Metrics 默认关闭。需要发送到支持 OTLP Metrics 的 Collector 时启用：

```bash
export FOYA_OTEL_METRICS_ENABLED=true
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://localhost:4318/v1/metrics
```

配置只在 Kernel 启动时读取，修改后需要重启 Foya。

当前桌面设置页没有 Telemetry 表单。开发模式可在设置环境变量后运行
`make desktop-dev`；从 macOS 图形界面启动的应用通常不会继承 Shell Profile
中的变量，发布版应通过 LaunchAgent、系统环境或本机 Collector 注入配置。

## Langfuse

Langfuse 接收 OTLP/HTTP Trace：

```bash
export LANGFUSE_PUBLIC_KEY=pk-lf-...
export LANGFUSE_SECRET_KEY=sk-lf-...
export LANGFUSE_AUTH="$(
  printf '%s:%s' "$LANGFUSE_PUBLIC_KEY" "$LANGFUSE_SECRET_KEY" |
  base64 |
  tr -d '\n'
)"

export FOYA_OTEL_ENABLED=true
export FOYA_OTEL_TRACES_ENABLED=true
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic ${LANGFUSE_AUTH},x-langfuse-ingestion-version=4"
```

美国区使用 `https://us.cloud.langfuse.com/api/public/otel`。自托管实例使用对应
Host 下的 `/api/public/otel`。

Langfuse 不接收 Foya Metrics，因此直连 Langfuse 时保持
`FOYA_OTEL_METRICS_ENABLED=false`。如需 Metrics，应发送到独立的
OpenTelemetry Collector 或指标后端。

## 内容与隐私

默认只发送模型、状态、耗时、Token 和关联 ID，不发送 Prompt、模型回答、推理、
工具参数或工具结果。

显式启用内容采集：

```bash
export FOYA_OTEL_CAPTURE_CONTENT=true
```

内容可能包含源码、文件路径、命令输出和敏感信息。只有在目标平台、数据区域和保留
策略均满足要求时才应开启。

## Metrics

启用 Metrics 后，Foya 导出：

- `foya.turn.count`、`foya.turn.duration`
- `foya.llm.count`、`foya.llm.duration`、`foya.llm.ttft`
- `foya.llm.token.usage`、`foya.llm.error`
- `foya.tool.count`、`foya.tool.duration`
- `foya.approval.wait_duration`
- `foya.hook.duration`、`foya.hook.error`
- `foya.compaction.count`、`foya.compaction.duration`
- `foya.queue.wait_duration`

Metrics 标签只使用模型、Provider、操作类型和状态等低基数字段，不包含 Session、
Run、Request 或 Tool Call ID。
