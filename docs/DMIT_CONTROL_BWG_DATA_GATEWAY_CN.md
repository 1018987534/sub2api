# DMIT 主控和数据层：四节点生产拓扑

## 当前状态

本文保留原文件名供历史链接使用。`bwg-us-01` 于 2026-10-29 退出生产，
`yt-us-01`（`154.23.243.26`）于 2026-11-25 退出生产。
退役节点不应出现在动态调度、Worker 静态 origin、活动监控或 DMIT 的 WireGuard peer 中。
不能只设为零权重：仍在配置中的零权重节点可能被紧急兜底选中。

| 实例 | 主机 | 职责 | 数据访问 | 网关 origin |
|---|---|---|---|---|
| `dmit-us-01` | `179.255.105.184` | 唯一 control、嵌入前端、PostgreSQL、Redis、Komari | 本机 `10.20.0.1` | `gateway3-origin.xiaohondou.com` |
| `vmiss-us-01` | `38.47.117.85` | gateway | WireGuard 直连 DMIT | `gateway-origin.xiaohondou.com` |
| `vmiss-us-02` | `38.47.113.166` | gateway | WireGuard 直连 DMIT | `gateway2-origin.xiaohondou.com` |
| `ovh-us-01` | `147.135.37.51` | 海外专用 gateway | 本机 WireGuard → TLS WebSocket → DMIT | `gateway-ovh-origin.xiaohondou.com` |

权威 PostgreSQL 容器是 DMIT 的 `sub2api-migration-postgres`，Redis 容器是
`sub2api-migration-redis`。BWG 的旧数据容器自 2026-10-23 起已停止；其旧卷不能重新作为主库启动。
所有运行节点使用 DMIT 的 `10.20.0.1:5432` 和 `10.20.0.1:6379`，数据端口不对公网开放。

## 边缘路由

主站 `xiaohondou.com` 的网页、管理和普通 API 进入 DMIT。
`control-origin.xiaohondou.com` 指向 DMIT，提供带专用 token 的运行时配置。
Responses 的三类路径及两类 Chat Completions 路径由现有 Worker 分流。
退役的 `nideyiyi.com` 和 `api.nideyiyi.com` 应用 DNS 和 Worker 路由不得恢复。

权威目标来自 DMIT `settings.gateway_routing_settings`；
有效权重来自 `GET /api/v1/gateway-routing/runtime`。2026-11-25 摘除 YT 后普通池目标为：

| 实例 | 目标权重 |
|---|---|
| `vmiss-us-01` | 61% |
| `vmiss-us-02` | 18% |
| `dmit-us-01` | 21% |

以上将普通池存活节点的 `30:9:10` 按最大余数法分配至 100%。OVH 的海外比例独立配置，
不参与普通池的权重合计。后续以管理员配置为准；Worker 静态变量只包含普通池节点。

配置运行时地址时，Worker 冷启动及缓存过期均等待权威配置，获取失败返回 503；
不使用静态或过期权重。缓存 TTL 为 1 秒。已经发出的流不能迁移，应先摘流并确认请求排空再停服务。
不要因连接失败、5xx 或模型错误重放 Responses POST。
仅 GET/HEAD 可在现有规则允许时故障切换。

现有流量阈值、健康和丢包保护保持启用。零权重备用和全节点故障时的紧急兜底仅从配置中的节点选择，
因此退役节点必须从 `nodes` 删除。BWG、YT 的历史 `usage_logs` 保留。

### 暂停与自动恢复的实际健康探测

主控每轮对全部已配置节点（包括暂停和零权重节点）请求 `/health/ready`，失败后立即复核。
探测必须得到 HTTP 200、`status=ready`、`checks.database=ok` 和 `checks.redis=ok`。
Nginx 的 403、空响应、重定向或仅网页可访问不能证明数据库和 Redis 正常，不计为恢复成功。

四个 gateway origin 的 Nginx 精确 `location = /health/ready` 必须允许主控公网地址
`179.255.105.184`；复制该 server 原有 allow/deny 规则后，在最后的 `deny all` 前追加此地址。
只对健康路径增加主控权限，模型接口继续使用原有 Cloudflare 访问限制。每次修改先备份、
`nginx -t` 后 reload，并从 DMIT 请求四个 origin 确认返回真实 readiness JSON。

暂停期间继续探测，但必须完整等待配置的暂停时长，且确认网络恢复后才恢复原目标比例。
ICMP 导致的暂停必须有正常 ICMP 恢复信号；readiness 失败导致的暂停必须有连续三轮
readiness 成功，且不存在严重 ICMP 丢包。未满暂停时长时，正常探测不能提前恢复分流。
持续严重丢包、数据库/Redis 不可用以及流量阈值保护仍保持摘流；重启主控后继续读取
持久化暂停状态。

2026-11-15 曾因丢包临时经 YT 中继 WireGuard。2026-11-25 退役前，两台 VMISS 逐台
切换到 DMIT `179.255.105.184:51820/UDP`，确认双向握手、PostgreSQL、Redis 就绪后才停
YT 中继。OVH 原链路不变：本机 `127.0.0.1:51823/UDP` 经 TLS WebSocket 直达 DMIT
`51824/TCP`，再到 DMIT 本机 WireGuard `51820/UDP`。不得因 YT 退役而停止 DMIT 的
`sub2api-wg-wss-server.service`。本轮私有回滚配置保留在相关服务器的
`/root/sub2api-yt-retirement-20261125/`，不得复制到仓库。

## 发布与监控

DMIT control 使用 `-tags embed` 构建；三台 gateway 使用无前端产物。
常规应用发布工具仍使用兼容文件名 `release_five_node.sh`；执行前核对工具的发布名单
与运行时、Komari 一致。仍含 YT 或缺少 OVH 的旧版脚本不得直接运行。
发布前从 DMIT 的 `sub2api-migration-postgres` 检查 schema：

- 无待执行 migration：逐台发布 `vmiss-us-01`、`vmiss-us-02`、`ovh-us-01`、`dmit-us-01`。
- 存在兼容的待执行 migration：先以 DMIT control 执行官方 migration，确认 schema 已更新，
  再发布三台 gateway。不得手工写 migration 表。

2026-11-15 起，按用户要求，应用发布流程不自动创建、复制、下载或上传数据库备份，
包括存在待执行 migration 的发布。仅保留只读 schema 检查、兼容性检查和官方 migration
入口；确需数据库备份时必须作为用户单独明确要求的操作执行。程序二进制与 runtime
resources 的节点本地回滚归档继续保留，完成全节点验证后才清理历史程序归档。

发布保留既有 main 提交、远端同步、干净构建、逐节点检查和失败回滚门禁。
数据库、Redis 和其他业务不随应用发布重建或重启。

Komari 位于 DMIT；公开页面为四节点监控。BWG、YT 记录隐藏以保留历史指标，
首页 ping 和任务绑定不包括退役节点。

## 退役与恢复边界

退役前先核对共享数据、中继和其他业务依赖。若有依赖，逐台验证替代路径；
从运行时调度与 Worker 静态配置摘除节点，等原请求排空，再停专属服务，
最后移除旧 origin DNS、监控任务绑定和专属 peer。保留旧容器数据和历史记录。

BWG 原始配置与旧数据归档于 DMIT 的 `/root/bwg-retirement-20261029/`。
该目录包含敏感配置和旧数据库，权限保持 root 私有，不提交仓库或公开。
备份核验与机器关机/供应商释放是不同状态；供应商账单取消须以供应商实际回执为准。

恢复现行服务应使用 DMIT 的当前数据和当前四节点备份。禁止从 BWG 陈旧 PostgreSQL 数据回滚现行业务。
确需恢复 BWG gateway 时，必须先恢复其到 DMIT 的专属 peer、配置、源站 TLS/DNS 和监控，
验证直接健康及鉴权后，再显式把节点加回管理员配置；不启动旧 control 或旧数据容器。
