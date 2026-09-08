---
title: Usage 统计
description: 了解模型 Token、消息活跃度和子 Agent 预算如何记录与聚合。
slug: docs/technical/usage
---

Foya 记录 Provider 返回的 Usage，用于展示成本规模、辅助上下文预算，并限制
SubAgent 任务树。

## 单次请求

Usage 包含 Model、Input Tokens、Output Tokens、Total Tokens 和 Cached Tokens。
Cached Tokens 是 Input Tokens 的子集。

Provider 在流中返回 Usage 后，Agent Engine 会：

1. 写入 `usage_updated` Event；
2. 保存 Session 请求明细；
3. 更新日期与 Model 聚合；
4. 通知 SubAgent Budget；
5. 保存为下一次 Context 估算基线。

文本 Compaction 的模型调用也会记录 Usage。

## Session Usage

Session Usage 返回最近一次已记录模型请求。它适合显示当前上下文规模，不代表该
Session 的历史累计费用。

Provider 未返回 Usage 时，该次请求不会产生准确统计。

## 全局统计

统计页支持最近 7 天或 30 天汇总：

- Input、Output、Total 和 Cached Tokens；
- Session 数；
- User 与 Assistant Message 的合计数量；
- 活跃天数；
- 连续活跃天数；
- 按 Model 的 Token 和 Request 排名；
- 最近 365 天活动序列。

日期按运行 Kernel 的本地时区计算。

当前 API 只返回一个聚合 `message_count`，不分别返回 User 和 Assistant 数量。

## Ledger

Usage 明细与长期 Ledger 分离：

- `usage_records` 保存 Session 明细；
- `usage_daily_ledger` 保存日期与 Model 聚合；
- `usage_message_daily_ledger` 保存消息数量；
- `usage_session_days` 保存 Root Session 活跃日。

删除 Session 会删除明细，但不会回滚已经累计的长期 Ledger。

## SubAgent Budget

Child Session 的每次 Usage 会累计到 Root Run。配置 `max_tree_tokens` 后，达到上限
会取消该树中排队和运行的 Child。

主 Session 普通请求不计入 Child Tree Budget。

## 准确性

Usage 依赖 Provider 报告。Foya 不使用本地估算伪造账单数据。本地 Token 估算只
服务 Context Compiler，不写入实际 Usage Ledger。

不同 Provider 的计费规则可能与 Total Tokens 不完全一致，Foya 当前不计算货币
费用。

## 当前边界

- 没有按 API Key 或组织维度统计。
- 没有价格表和费用换算。
- 缺少 Provider Usage 时统计会少计。
- 日期统计使用 Kernel 本地时区。
- 删除 Session 不删除历史聚合。
