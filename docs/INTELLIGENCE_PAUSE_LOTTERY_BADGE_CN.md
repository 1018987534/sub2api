# 降智调度暂停与抽奖中奖标识

## 降智暂停及恢复

`ProtectDegradedIntelligenceAccount` 继续使用账号级降智记录和分组级调度隔离。
暂停只作用于符合保护条件且有替代账号的“不降智”分组，唯一可用账号的例外保持原有规则。

`firstTokenLatencyStatsCache.ResetForIntelligencePause` 保留现有接口名称，实际只写入
`intelligence_pause_until_ms` 标记。不得删除耗时样本、改写统计时间、重置快慢池、触发熔断，
也不得清除探测租约或人工探测队列。现有缓存过期时间和滚动窗口规则继续生效。

`RecoverIntelligenceAccount` 只解除降智隔离及对应的采样抑制，恢复时直接使用保留的池状态。
账号凭据、分组关系、倍率及其他冷却状态均不因降智暂停或恢复而重置。
内部恢复检测不写入正常耗时样本，真实请求恢复后继续累计原有样本。

管理面板 `AccountFirstTokenLatencyMetrics` 保留真实耗时、P50/P90、样本数和快慢池状态，
由每个分组的 `intelligence_blocked` 字段单独表达调度暂停。界面显示“降智暂停”和“已恢复调度”。
真实熔断及自然样本不足仍按原有规则显示，不能把它们与降智暂停混为一谈。
旧版本已经删除的 Redis 样本无法通过本次代码修改还原。

回归入口：`TestIntelligencePoolPausePreservesState`、
`TestIntelligencePausedAccountPreservesMetricsThroughRecovery`、
`TestIntelligenceSharedAccountIsolationAndRecovery`。

## 抽奖中奖标识

`AppSidebar.vue` 复用已有每 15 秒及 `lottery-availability-changed` 事件触发的
`GET /api/v1/lottery/current` 请求。只有 `my_recent_winners` 含个人中奖记录时，
个人 `/lottery` 菜单才显示金色“奖”徽标；英文为 `Win`。
管理员的个人区与普通用户菜单使用相同行为，侧栏收起时徽标仍可见。

该标识不依赖当前轮是否已开奖、是否已参与或是否可再次参与，
也不使用公开的 `recent_winners` 判断本人中奖。中奖徽标优先于“可参与”红点，
未中奖时继续使用原有红点规则。该标识表示存在个人中奖记录，不是未读计数。
不增加独立轮询，不修改领奖、余额发放或抽奖参与行为。

回归入口：`AppSidebar.lotteryWinner.spec.ts`、`AppSidebar.polling.spec.ts`。
