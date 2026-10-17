# BWG → DMIT 迁移执行包

确认后的目标：DMIT 保持现有 2 核 / 2GB，不升级；作为唯一 control、PostgreSQL、Redis、Komari、WireGuard 中心。BWG **继续运行 `bwg-us-01` gateway**，与 YT 一样使用 DMIT 数据层。全程保持五个实例 ID、当前二进制、Worker 配置及流量权重。

| 节点 | 目标 WireGuard 地址 | 角色 / 路径 |
|---|---|---|
| DMIT | `10.20.0.1/32` | control + 数据中心 |
| BWG | `10.20.3.2/32` | gateway，直连 DMIT |
| VMISS-01 | `10.20.0.2/32` | gateway，经 YT 中转到 DMIT |
| YT | `10.20.1.2/32` | gateway，直连 DMIT；保留 VMISS UDP 中转 |
| VMISS-02 | `10.20.2.2/32` | gateway，经 YT 中转到 DMIT |

VMISS 现有直连 UDP 曾失败，因此本次沿用已经验证的 YT 中转，只把中转上游由 BWG 改为 DMIT。最终数据库/监控路径不依赖 BWG。BWG 到期停止服务时，其余四台不会因此失去数据库。

## 使用方式

在这台 Mac 执行，SSH 使用现有 `~/.ssh/id_ed25519`。`migration.env` 只放非密配置。脚本会从正在运行的容器读取真实 Compose 项目名、文件、挂载、凭据及镜像 ID，兼容 YT 的 Compose v1，其余使用 v2。凭据只进入 SSH 管道及远端权限 600 的运行文件，不写本地证据。

```bash
cd /Users/chendeyao/work/sub2api-upstream-brand-login
export MIGRATION_EVIDENCE_DIR="$HOME/Documents/Codex/bwg-dmit-migration/run-$(date +%Y%m%d-%H%M%S)"
S=ops/bwg-dmit-migration/bwg_to_dmit.sh
"$S" plan
"$S" preflight
```

预备阶段，无需停止现网：

```bash
"$S" prepare --execute
```

若中断，可单独重试尚未完成的 `"$S" stage --execute` 或 `"$S" seed --execute`；无需重跑已完成的全量恢复。

Docker/数据库命令失败时，退出码会打印，可能包含凭据或行数据的原始 stderr 只保留在出错机器 `/run/bwg-dmit-migration/last-error.json`（600 权限），不要把该文件原文贴到公开日志。

`stage` 固定源端 PostgreSQL/Redis/Komari 的实际镜像，流式传到 DMIT；创建 DMIT 独立数据目录和仅监听 `127.0.0.1:15432/16379` 的目标 PG/Redis。现有应用仍读写 BWG。`seed` 完整迁入角色、权限和 `sub2api` 全库，演练恢复并生成目标清单，不保留 dump 文件。源库在线变化，因此精确一致校验安排在维护窗口内。

预备状态在远端 `/run/bwg-dmit-migration`，包含权限 600 的原配置，仅供本次切换/回退；这不是历史归档。**预备后不要重启服务器**，`/run` 重启会消失。如果源/目标配置或二进制被其他操作改变，`cutover` 会在停应用之前拒绝执行。

维护窗口执行：

```bash
"$S" cutover --execute --confirm-cutover=I_UNDERSTAND
"$S" validate
```

`cutover` 的固定顺序：

1. 校验预备状态、配置/二进制漂移、目标健康；准备两个监控 vhost 及 TLS。
2. 对五台 Sub2API 容器逐台发送正常停止，最多等待 90 秒排空；超时强杀则中止。每台生成带本次 run ID 的 `apps-stopped` 标记。
3. 仅在五台标记全部匹配且容器确实停止后，切 WireGuard 中心。DMIT → BWG → YT → 更新 YT 中转 → VMISS 两台；验证私网 ping 和新握手。独立 `wireguard` 入口有同样门槛。
4. 要求源库无剩余客户端写入连接。`pg_dumpall --globals-only` 加 `pg_dump -Fc --create | pg_restore` 流式恢复所有 schema、表、历史数据、序列、扩展、large object、角色和 ACL。串行恢复，不跑并行任务。
5. 源/目标逐表对比精确行数与按行内容计算的哈希，同时比较序列值、schema 哈希、扩展、角色权限及 migration；任何差异立即中止。
6. 五台应用停写后，源 Redis `SAVE` 并正常停止，完整流式复制持久化目录（RDB 和 multipart AOF），逐文件比较 SHA256。目标先在独立临时目录验全流、RDB/AOF，确认无误才替换其专属数据目录；保留所有 Redis DB 和绝对过期时间。TTL 到期会使 DBSIZE 正常减少，不能据此判定丢数据。完整复制停止后的 Komari SQLite/主题/配置，逐文件 SHA 及 SQLite `quick_check`。
7. PG/Redis/Komari 只在 DMIT `10.20.0.1` 私网监听；防火墙只允许四个 gateway 私网地址与 DMIT control 的 Docker 网络。安装 WireGuard、防火墙及数据服务开机顺序，避免 Docker 早于私网地址就绪时绑定失败。
8. DMIT control 先启动，再串行启动 **BWG、VMISS-01、YT、VMISS-02 四个 gateway**；只重建应用容器，使用真实原 Compose 项目和 `--no-deps`。修改原 Compose 中 `sub2api.environment` 和 `.env` 的连接/连接池字段，确保后续普通容器重建也使用 DMIT；保留镜像、挂载和其他服务。
9. 校验五节点角色、DB/Redis ready、原二进制/挂载/安全标记、主站、运行时路由、Komari 五节点及近期 usage。不会生成付费请求或 Responses POST。零权重节点暂无新增 usage 时如实报告，不能拿健康检查替代业务验收。

control 连接池上限 16、四个 gateway 各 12，总和 64；PG `max_connections=100`、`shared_buffers=128MB`、`work_mem=2MB`。目标 PG/Redis/Komari 容器内存上限分别为 768/384/192MiB，属于上限而非预留，DMIT 保持现有规格。

此方案包含完整数据库重传和逐表哈希，维护窗口时长以 `seed` 实际恢复耗时为依据，不能保证秒级切换。准备期间的演练成功也不代表生产迁移完成。准备时源库持续写入，目标不是持续复制的备用库；正式切换必须重新全量同步。

## 监控域名与收尾

两个监控域名目前在 BWG；切换数据库后再迁 DNS：

```bash
"$S" publish --execute --confirm-publish=I_UNDERSTAND
"$S" finalize --execute --confirm-finalize=I_UNDERSTAND
```

只改 `check.nideyiyi.com`、`check.xiaofanqie.org` 的 A 记录指向 DMIT，保留代理/TTL/备注。Cloudflare token 使用已声明的 `~/.codex/secrets/cloudflare_nideyiyi_api_token` 或环境变量，不读取浏览器凭据。BWG gateway origin 不改。TLS 现有证书直接迁入，DNS 生效后注册 DMIT Certbot webroot 续期。

`finalize` 重新验收后，只停止 BWG 旧 PostgreSQL、Redis、两个 relay、Komari，并关闭这些旧容器自动重启；**不停止 BWG gateway**。源在线数据暂时保留供回退；不恢复或复制此前删除的历史备份。DMIT 的 `/opt/sub2api-migration-staging` 已成为正式数据目录，名称带 staging 也不能删除。

## 回退

切换中断、DMIT 尚未启动应用时：

```bash
"$S" rollback --execute --confirm-rollback=I_UNDERSTAND
```

只要启动过任何 DMIT 应用，启动任务也可能写库；此后普通回退会被拒绝，必须：

```bash
"$S" rollback --execute --confirm-rollback=I_UNDERSTAND --reverse-sync
```

先停五台应用，把 DMIT 最新 PG 全库/角色、Redis、Komari 反向同步回 BWG并校验，再恢复 DNS、WireGuard、中转、原环境和容器角色。不能直接切回已经过时的旧 BWG 库。目标阶段目录保留便于排错，不执行 `down -v`、全局 prune 或删除其他业务。

`/run` 原配置仅维持本次回退窗口；窗口结束后执行 `"$S" close --execute --confirm-close=I_UNDERSTAND` 清理五台本次临时状态，之后不再支持自动回退。已删除的历史备份不可恢复；目录外的既有清理 `ROLLBACK.sh` 只能恢复清理脚本自身。

## 验证边界

`python3 selftest.py` 检查拒绝门槛和配置；`python3 integration_rehearsal.py` 在本机 arm64 Docker 中创建隔离夹具，实际验证 PG 全库/角色/ACL/序列/large object 的正反向恢复，以及 Redis RDB+AOF、跨库、会话、绝对 TTL、重启和新增写入反向恢复。夹具 PostgreSQL 18/Redis 8 镜像来自 public.ecr.aws，生产则始终使用从 BWG 复制的原镜像 ID。

五台现状、DNS 和新 Compose 渲染由只读 `preflight` / `compose-probe` 核验；`seed` 才是 DMIT 独立目标的实际恢复。WireGuard、生产切换、DNS 发布、生产反向回退须在维护窗口逐步验收，不能由本地夹具替代。DMIT 现有 Money、QQ、XFQ、FRP 等服务不属于迁移对象。
