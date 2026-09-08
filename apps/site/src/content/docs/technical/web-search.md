---
title: Web Search
description: 了解 Foya 的搜索路由、网页抓取和网络安全边界。
slug: docs/technical/web-search
---

Foya 将 Web Search 与 Web Fetch 分成两个 Tool：

- `web_search` 根据查询返回有界结果列表；
- `web_fetch` 读取一个已知 URL 的主要文本。

两者都需要 Network Approval，但使用不同的 Provider 和输入约束。

## Search Route

搜索按以下顺序尝试：

1. 当前语言 Provider 的 Native Web Search；
2. 用户配置的 Default Search Provider；
3. DuckDuckGo Lite Fallback。

Native Search 只有在 Provider 明确实现该能力时使用。调用失败或没有结果时，Router
继续尝试外部 Provider。

## 外部 Provider

当前支持：

| Kind | 要求 |
|---|---|
| Google Custom Search | API Key 与 Search Engine ID |
| Bing Web Search | API Key，可选 Endpoint |
| Baidu | 无 API Key，解析搜索 HTML |
| DuckDuckGo Lite | 内置无配置 Fallback |

Search Limit 默认为 5，最大为 10。返回结果统一为 Title、URL、Snippet、Source
和 Rank。

## 配置

Web Search 设置包含：

- 总开关；
- 默认 Provider ID；
- 多个 Provider 配置；
- 每项 Enabled；
- 对应凭证和 Endpoint。

配置保存在：

```text
<data-dir>/web-search.json
```

读取公共配置时会移除明文 API Key，只返回 `has_api_key`。

## `web_search`

工具会：

1. 校验 Query；
2. 请求 Network Approval；
3. 读取当前 Session 的 Model 与 Provider；
4. 执行 Search Route；
5. 标准化并限制结果数；
6. 以 JSON 返回 Provider、Query 和 Results。

Search Tool 可与其他声明为并行安全的读取 Tool 同时执行。

## `web_fetch`

Web Fetch 只接受 HTTP 和 HTTPS，并拒绝：

- 缺少 Host；
- 带用户信息的 URL；
- 解析到 Loopback；
- Private Address；
- Link-local；
- Multicast；
- Unspecified Address。

每次 Redirect 都重新验证目标，最多跟随 10 次。

## 内容处理

Web Fetch：

- 请求 Timeout 为 30 秒；
- 响应上限为 5 MiB；
- 只接受 Text、JSON、XML 或 HTML；
- 将 HTML 转换成有界可读文本；
- 保留绝对链接；
- 移除 Script、Style 和 Noscript；
- 最终文本限制为 200000 Bytes 和 2000 行。

网页内容作为 Tool Result 回灌模型，不具有指令权限。

## SSRF 边界

Web Fetch 在建立连接前解析 DNS，并拒绝私有或保留地址。Redirect 目标也会校验。

这减少了 Agent 读取本机服务和内网 Metadata Endpoint 的风险，但不能替代完整网络
隔离：

- 公共域名的 DNS 记录可能变化；
- 外部代理可能执行额外访问；
- Provider Native Search 在 Provider 服务端运行；
- MCP Server 的网络行为不受 Web Fetch 校验控制。

## 与 Browser 的区别

Web Fetch 不执行 JavaScript、不保持 Cookie 会话，也不提供点击和输入。需要动态
交互时应使用 [Browser Runtime](./browser.md)。

## 当前边界

- Search Provider 结果依赖第三方服务质量。
- Baidu 和 DuckDuckGo 使用 HTML 解析，页面变化可能导致结果缺失。
- Web Fetch 不支持登录态页面。
- 当前没有通用 Robots 或站点抓取调度器。
- 凭证仍保存在本地配置文件中。
