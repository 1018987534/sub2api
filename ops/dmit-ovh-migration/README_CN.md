# 小红豆：DMIT → OVH 整站迁移执行包

目标是 OVH 保留一套 Sub2API（含前端）、PostgreSQL、Redis。Cloudflare 对 `xiaohondou.com/*` 使用统一选路；CN 和地区未知按有效权重走 VMISS-01、VMISS-02、DMIT，再由节点转发到 OVH。海外保留独立 OVH 比例。保留节点 ID、套量保护、登录、主站域名和同域回调。Komari、Vaultwarden 和 QQ 机器人等独立服务不在停服清单内。

本执行包不授权生产发布。`wrangler.toml`、现网五条路由和四节点日常发布脚本均未改动。候选 Worker 位于 `deploy/multi-node/worker/wrangler.whole-site.toml`，不带活动路由。数据迁移是独立操作；日常应用发布仍不自动创建数据库 dump。

## 1. 准备阶段可直接执行

在仓库根目录执行。`plan` 不访问网络，`preflight` 只读。四台服务器使用固定身份；OVH 通过本机 Vaultwarden SSH helper 获取精确账号和已固定的主机密钥，其他三台使用已授权的本机 SSH 配置。

```bash
python3 -B ops/dmit-ovh-migration/migrate.py plan
python3 -B ops/dmit-ovh-migration/migrate.py preflight
python3 -B -m unittest discover -s ops/dmit-ovh-migration -v
node --test deploy/multi-node/worker/{responses-dispatcher,whole-site-dispatcher}.test.mjs
PG_BIN="$(pg_config --bindir)" bash ops/dmit-ovh-migration/rehearse-local.sh
```

`preflight --check-edge` 另外读取 Cloudflare zone、部署版本、五条旧路由和主站／控制源站的实际 DNS。要使用 Vaultwarden 中与该账户和 zone 精确匹配的 Cloudflare token，经授权环境注入 `CLOUDFLARE_API_TOKEN`。脚本不启动登录、不输出 token。发现同站点或源站有重叠规则、同名多记录、AAAA/CNAME、未知 IP 或版本流量拆分时，先停下来核对，不能靠改脚本跳过。

## 2. 正式窗口前必须准备的站点配置

执行器负责编排、复制和主写门禁、目标提升、Cloudflare 切换、日志与回滚。不会根据历史值猜测生产 WireGuard、防火墙、Compose、证书或维护模式。把以下内容做成**经过审查和演练的站点 hook**，用 `profile.example.json` 的字段登记。配置和 hook 路径须在 Git 仓库之外，由操作者持有，且不可被组或其他账号写入。应用候选、脚本及 Worker 必须来自同一个经过测试的 `cdy/main` 完整 SHA。

| 站点项目 | 放行条件 |
| --- | --- |
| OVH 应用候选 | `linux/amd64` 的 embedded control；校验版本、二进制和 resources 哈希。保留现有签名／加密材料、应用数据、Logo、插件和初始化锁。候选使用受信跳板身份保持入口维度的并发记录及用量归因，主控识别共享 OVH 故障，不把它记成三个独立冷却。上述 Go 应用适配不由本执行包实现，必须先完成和验证。 |
| 三条转发路径 | 只给 VMISS 两台和 DMIT 开到 OVH 私有 HTTP 监听的访问。为新链路使用独立 WireGuard 地址／端口，验证 MTU 和持续传输；保留旧 DMIT 数据通路到数据切换结束。在现有 TLS vhost 基础上按 `nginx/relay.location.conf.example` 合并整站反代，使用站点专属认证、可信 Cloudflare 来源和独立健康 ACL，不能将模板直接覆盖线上文件。 |
| OVH 旧入口兼容 | 在专属源站到 OVH 私有反代中保留 `$host` 的正确校验、业务 Host `xiaohondou.com`、请求方法／原始 URI、Cookie、Authorization、真实 IP、SSE、WebSocket 和下载。旧 Worker 在过渡及边缘回滚期仍必须能访问原五类接口；对旧 Worker 的兼容仅限原受信接入面和原路径，不能免认证开放整站。 |
| PostgreSQL 副本 | 用 DMIT 当前 `sub2api` 物理集群和同版本镜像直接向 OVH 执行 `pg_basebackup` 并持续回放 WAL，不能使用旧归档初始化。OVH 容器名 `sub2api-ovh-postgres`。源端只为认证过的复制账号和私网 peer 开放；DMIT 盘没有额外全库 dump 余量，复制节流、磁盘报警，并给本次复制槽限制 WAL 保留。验证扩展、版本、系统 ID、追平和恢复演练。 |
| Redis 副本 | OVH 容器名 `sub2api-ovh-redis`，与 DMIT 同镜像、启用 AOF，通过认证私网复制，初始只读。验证复制 ID、偏移、持久化和 TTL；提前演练提升后持久化运行配置，确保重启不会重新跟随 DMIT。核对暂停、余额保留、幂等、限流和会话中的关键键。 |
| Cloudflare 候选 | 已获准 `stage` 后，使用 `versions upload` 上传候选并记录 UUID，不要用 `wrangler deploy` 或改活动 secrets。以项目 Vault 按作用域注入 `ROUTING_CONFIG_TOKEN`、`ORIGIN_AUTH_TOKEN`，分别匹配 OVH control 和私有源站入口；`--secrets-file` 仅使用 Vault 注入的临时受保护文件，退出即清除。不要提交 secrets 或打印 Wrangler 原始输出。校验两个 DNS A 记录、运行态来源、源站 WAF/TLS/健康和权限，并保留回滚前的 Worker 版本。 |

站点 hook 的契约如下。标准输出只返回 `{ "ok": true, "phase": "本次动作", "run_id": "本次 run ID" }`；`stage` 还需要 `worker_version` 并核对为候选 UUID。上下文由 `SUB2API_MIGRATION_RUN_ID`、`SUB2API_MIGRATION_COMMIT`、`SUB2API_MIGRATION_STATE` 提供。另从授权 Vault 取站点凭据，不把日志、密码、token 或业务行写到回执。每个动作须能按 run ID 确认前次执行结果，禁止看到超时就重复有副作用的请求。

| hook 动作 | 本机执行器期望的现场工作 |
| --- | --- |
| `stage` | 在隔离监听上准备 OVH 应用、两套只读副本和三条候选入口；上传但不部署 Worker。验证切换所需归因、连接、容量、数据和撤回路径。绝不改主站活动路由、重启旧应用或让候选运行后台写。 |
| `switch-origins` | 保存旧配置，短窗口交接唯一 control；OVH 使用 DMIT 数据，旧主站 vhost 临时指向同一 OVH control。逐台迁移并排空旧网关，再停非 OVH 旧 `sub2api`；实际维护开关和单写状态要读回。 |
| `verify-origins`, `verify-dns-origins`, `verify-legacy-origins` | 使用受信探针验证四个源站、公开接口鉴权、控制源站与 OVH 私有链路。分别证明整站候选、已改 DNS 后的主站默认／控制回源、可用于边缘回滚的旧入口。 |
| `verify-public`, `verify-legacy-public` | 对照 Worker、跳板、OVH 和 Komari 的现场证据。覆盖主站、登录会话、API、流式和回调。真实写入测试必须另获授权且只发一次；缺少证据不返回成功。 |
| `freeze`, `verify-maintenance` | 所有入口维护，排空长流、计费队列和订单任务，暂停对本数据写入的独立进程与回调入口，停止 `sub2api`。通过网络和服务管理策略封住所有其他写入入口。恢复用 `verify-maintenance` 证明这个边界仍成立。 |
| `attach-local-data`, `attach-source-data` | 基于当前唯一数据主写更新 OVH 应用连接；前者还须核对 Redis 提升状态已持久化。保持入口维护，仅供本机验证。 |
| `verify-data`, `resume`, `finalize` | 逐项校验 schema、账单、用量、订单、状态、Redis TTL 和 runtime 后解除维护。观察一个业务峰值后清理仅为旧数据通路配置的专用资源。保留 DMIT 停用数据和回滚档案，不自动删除卷。 |

## 3. 执行顺序与回退

正式运行时，从干净并已推送的 `main` 上执行。`SUB2API_MIGRATION_PROFILE` 和 `SUB2API_MIGRATION_STATE` 指向私有路径；后者为 0700 的持久目录，同一个迁移全程使用同一路径。只有收到正式生产和维护窗授权后才执行写阶段；复制及存储切换也要在已确认的授权范围内。**不要把下面的阶段拼成无人值守的整段自动执行。** 使用 `status` 和上线探针确认当前阶段成功，再开始下一段。

```bash
s=ops/dmit-ovh-migration/migrate.py
python3 -B "$s" preflight --check-edge
python3 -B "$s" stage --execute --confirm stage
python3 -B "$s" switch-origins --execute --confirm switch-origins
python3 -B "$s" publish-edge --execute --confirm publish-edge
python3 -B "$s" verify
# 独立数据维护窗
python3 -B "$s" cutover-data --execute --confirm cutover-data
python3 -B "$s" verify
# 观察一个业务峰值及确认存储／流量回收边界后
python3 -B "$s" finalize --execute --confirm finalize
```

整站 Worker 只对匹配本次随机 nonce 的接入前拒绝改投，不自动重试不明确的断线／5xx，包括可能写订单的 GET 回调。为避免缓存大正文，未声明长度或大于 1 MiB 的有正文请求只转发一次。普通池全不可用返回 503，OVH 是唯一应用和数据故障域。

- `status` 先核对 `pending` 和 `target_writes_possible`。日志中的冻结点、容器 ID、Worker 版本以及 DNS 均为非密证据。不能删除旧状态或改 run ID 来越过门禁。
- `rollback-edge --execute --confirm rollback-edge` 恢复旧五条路由和旧 Worker 版本，保留当前数据主写和已迁移的主站／控制源站 DNS。不会把业务拉回旧库。
- `rollback-before-promote --execute --confirm rollback-before-promote` 只在提升从未尝试且目标仍是只读时恢复 DMIT；核对原容器 ID 后才能恢复源端进程。已经尝试过提升，就只允许向前恢复。
- `resume-after-promote --execute --confirm resume-after-promote` 先确认维护和 DMIT 围栏还在，再读回 OVH 角色与冻结点。只提升仍是已追平副本的组件，成功后完成接管。若需要从 OVH 回到 DMIT，另行冻结、反向同步和核账，不能启动陈旧卷。

本轮交付的是准备工具和隔离候选。沙箱测试通过不能代替线上 Cloudflare 权限读回、OVH 站点 hook 联调和正式迁移演练。
