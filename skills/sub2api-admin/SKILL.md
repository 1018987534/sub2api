---
name: sub2api-admin
description: Manage Sub2API admin APIs, including 小红豆模型广场 model/family CRUD and display pricing (新增模型、调展示价、增加 Gemini 等系列), accounts, groups, redeem codes, proxies, imports, and settings. Prefer the CLI for administrator operations.
---

# Sub2API Admin

Use the bundled CLI instead of ad hoc `curl`. Run examples from this skill directory.

```bash
export SUB2API_BASE_URL='https://your-sub2api-host'
export SUB2API_ADMIN_API_KEY='<admin api key>'
# Or, when the deployment uses admin JWT login instead of an admin API key:
# export SUB2API_JWT='<admin access_token>'
node scripts/sub2api-admin.js accounts list
```

For all commands and payload examples, read [references/admin-cli.md](references/admin-cli.md).

## 模型广场（只影响展示）

小红豆的「模型广场定价」直接用 `node scripts/sub2api-admin.js plaza ...`，不要在后台点击，也不要改账单配置或直接写生产数据库。先锁定正式站点、模型 ID 及上下文档位，使用 `list` / `get` / `defaults` 得到现状，再执行 `create`、`update`、`price` 或 `delete`。写命令自带读回。新系列由首个模型的 `family` 自动建立，不需改代码、部署或先创建空系列。`null` 恢复默认，`0` 是明确的零价。

优先使用只授权 `model_plaza.read` 和所需 `model_plaza.write` / `model_plaza.delete` 的管理密钥，或现有管理员 JWT。凭据由 `pwd-xiaofanqie-login` 按目标 HTTPS 站点取得，仅传入进程环境。缺失或被拒绝时说明认证卡点，不生成主密钥、不绕过鉴权。接口与示例见 [模型广场 API](references/admin-cli.md#模型广场展示价)。

## Workflow

1. Reuse `SUB2API_BASE_URL` and either `SUB2API_ADMIN_API_KEY` or `SUB2API_JWT` from the environment.
2. Run read-only commands first: `accounts list`, `accounts get <id>`, `groups all`, or `proxies all`.
3. Before destructive or bulk writes, print the target account names and IDs.
4. Execute the write command only after the target set is clear.
5. Run a follow-up read command to verify the result.

## Site Logo And Branding

- For user requests like `站点 logo 换一下`, treat `site_logo` as a production system setting first. Do not start by changing `frontend/public/logo.*`, running local UI tests, or relying on the default fallback asset unless the user explicitly asks to change the default source asset.
- Update the live setting through the admin settings surface/API when possible. If you must use a database fallback, update only `settings.key='site_logo'`, back up the previous value, then verify `/api/v1/settings/public` and the rendered homepage favicon on `https://xiaohondou.com` and compatibility hosts.
- A DB-only setting change can leave server-rendered HTML cached with the previous favicon. If public settings show the new value but homepage HTML still injects the old data URL, restart only the control `sub2api` container and re-check readiness plus rendered favicon; do not touch gateway-only nodes.
- If the requested logo change truly requires code, finish the default
  topic-branch -> `main` merge and `cdy/main` push delivery lifecycle instead
  of leaving a local-only code diff. Production release remains a separate
  user-authorized step governed by `sub2api-vps-release`.

## Multi-node Responses Routing

- Read `GET /api/v1/admin/settings/gateway-routing` before changing node ratios. It returns both administrator-owned `settings.nodes[*].target_weight` and monitor-derived `runtime.nodes[*].effective_weight`.
- Update ratios with `PUT /api/v1/admin/settings/gateway-routing`. Weights are arbitrary ratios, so `5/1/3/1` means `50%/10%/30%/10%`; at least one target must remain positive.
- Traffic protection never rewrites the target. At the configured threshold it temporarily publishes `effective_weight=0`; monitor failure keeps the last valid effective result and marks it stale.
- Verify the control read-only endpoint `GET /api/v1/gateway-routing/runtime` with `X-Gateway-Routing-Token`, then verify fresh origin logs and `usage_logs.instance_id`. The dedicated token must remain only in the control environment and the Worker `ROUTING_CONFIG_TOKEN` secret. A successful settings response alone is not proof that the Worker has consumed the new weights.
- The Worker runtime cache is about 15 seconds and the current monitoring feed is sampled about once per minute. Do not expect byte-perfect distribution from request weights.

## Common Commands

```bash
node scripts/sub2api-admin.js accounts list --page-size 20
node scripts/sub2api-admin.js accounts get 40
node scripts/sub2api-admin.js accounts usage 40
node scripts/sub2api-admin.js accounts set-schedulable 40 true
node scripts/sub2api-admin.js accounts bulk-update --ids 40,39 --json '{"concurrency":10}'
node scripts/sub2api-admin.js redeem-codes list --page-size 20
node scripts/sub2api-admin.js redeem-codes generate --json '{"count":1,"type":"balance","value":10}' --idempotency-key redeem-$(date +%s)
node scripts/sub2api-admin.js redeem-codes create-and-redeem --json '{"code":"order_123","type":"balance","value":10,"user_id":123}' --idempotency-key order-123
node scripts/sub2api-admin.js error-rules list
node scripts/sub2api-admin.js tls-profiles list
node scripts/sub2api-admin.js api GET /admin/settings/gateway-routing
```

## Safety Notes

- Authentication uses `x-api-key` from `SUB2API_ADMIN_API_KEY` first, then falls back to `Authorization: Bearer <jwt>` from `SUB2API_JWT`.
- If the API returns `INVALID_ADMIN_KEY`, ask the user to regenerate the admin API key. If using JWT, log in as an admin user and copy the `access_token` from `POST /api/v1/auth/login`.
- `accounts export` includes credentials and tokens. Prefer `--file` and avoid printing exports in chat.
- Redeem code create/redeem commands should use `--idempotency-key` for payment or recharge workflows.
- For uncertain or newly added backend APIs, use `api <METHOD> <admin-path>` after a read-only check.
