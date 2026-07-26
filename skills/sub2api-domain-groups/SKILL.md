---
name: sub2api-domain-groups
description: Maintain the Sub2API multi-domain portal customization for 多域名, 域名分组, domain_brand_config, xiaohondou.com, xiaofanqie.org, and legacy nideyiyi.com API compatibility. Use when changing per-domain Logo/title/subtitle/contact/API endpoint, assigning groups to a domain, filtering portal pricing, debugging Host routing or cross-domain group leaks, merging upstream changes, or deploying this customized feature.
---

# Sub2API Domain Groups

This project uses one backend, database, user set, balance, and API gateway for multiple portal domains. The customization is intentionally smaller than multi-tenancy: a configured Host may override five public settings and gets an independent group allowlist.

## Invariants

- Unconfigured hosts inherit global Settings and retain the historical full group list.
- Configured hosts use `allowed_group_ids` even when the list is empty.
- A group ID may belong to only one configured domain.
- Users, balances, API keys, orders, upstream accounts, and administrators are shared.
- Self-service registrations persist an immutable `registration_site_name` snapshot from the effective portal title. It is an attribution label only; it does not change authentication, entitlements, balances, or group permissions.
- `/v1`, `/responses`, image/video, and other gateway paths are Host-neutral.
- Registration follows upstream behavior and does not auto-create a default API key.
- Domain scoping is a portal pricing/display boundary, not a commercial entitlement boundary.
- `xiaohondou.com` is the primary C-end domain. `nideyiyi.com` and `api.nideyiyi.com` are legacy compatibility ingress domains and must not own duplicate group configuration.
- `nideyiyi.com` redirects only browser document navigations (`GET`/`HEAD`, HTML `Accept`, non-API path) to `xiaohondou.com` with temporary `302`.
- `api.nideyiyi.com`, API/gateway paths, non-HTML requests, POST requests, SSE, and WebSocket traffic remain on the original Host and proxy to the shared backend. Never permanently redirect API or gateway requests across domains.
- The shared API uses strict CORS. Keep every browser portal origin in `/www/sub2api/data/config.yaml` under `cors.allowed_origins`; an absent or empty list rejects cross-origin preflights with `403`.

## Stored Config

The `settings` table key is `domain_brand_config`. The value shape is:

```json
{
  "domains": [
    {
      "domain": "xiaohondou.com",
      "api_base_url": "https://xiaohondou.com/v1",
      "allowed_group_ids": [5, 79]
    },
    {
      "domain": "xiaofanqie.org",
      "site_name": "小番茄",
      "site_logo": "",
      "contact_info": "B2B support contact",
      "api_base_url": "https://xiaofanqie.org/v1",
      "allowed_group_ids": []
    }
  ]
}
```

Public field semantics:

- Missing or `null`: inherit the global Setting.
- `site_logo: ""`: use the frontend packaged `/logo.svg` fallback.
- Non-empty value: override the global Setting for that Host.
- The configurable public fields are `site_name`, `site_logo`, `site_subtitle`, `contact_info`, and `api_base_url`.

Host normalization lowercases the value and removes a port, trailing dot, and IPv6 brackets.

## Registration Source

`users.registration_site_name` records the effective `site_name` when an end
user is first created through email or OAuth registration. A configured domain
uses its `site_name`; an omitted domain title inherits the global title. The
field is intentionally not changed by profile edits or later brand renames.

- Migration `191_add_user_registration_site_name.sql` backfills existing users
  from the then-current global `site_name` so the administration list has no
  blank historical source labels.
- Domain-brand context applies to `/api/v1/auth/*` as well as portal display,
  group, and payment routes; do not remove it from auth registration paths.
- Admin-created users are not self-service registrations and may have an empty
  source label.

## Code Map

- Config, validation, context: `backend/internal/service/domain_brand.go`
- Setting key: `backend/internal/service/domain_constants.go`
- Host middleware: `backend/internal/server/middleware/domain_brand.go`
- Router ordering: `backend/internal/server/router.go`
- Public display overlay: `backend/internal/service/setting_public.go`
- API key group list/create/update: `backend/internal/service/api_key_service.go`
- Payment list and checkout filtering: `backend/internal/handler/payment_handler.go`, `backend/internal/service/payment_config_plans.go`
- Forged plan rejection: `backend/internal/service/payment_order.go`
- Host-sharded HTML/ETag cache: `backend/internal/web/html_cache.go`, `backend/internal/web/embed_on.go`
- Admin API: `GET/PUT /api/v1/admin/settings/domain-brand-config`
- Admin UI: `frontend/src/views/admin/settings/DomainBrandConfigPanel.vue`
- Registration snapshot: `backend/internal/service/auth_service.go` and `backend/internal/service/domain_brand.go`
- Admin user label: `frontend/src/views/admin/UsersView.vue`
- Design references: `MULTI_BRAND_MINIMAL_PLAN_CN.md`, `MULTI_BRAND_ARCHITECTURE_CN.md`

## Admin Save Contract

`域名品牌与分组` is an independently submitted settings panel. Its `保存域名配置`
action calls `PUT /api/v1/admin/settings/domain-brand-config`; the regular System
Settings save calls `/api/v1/admin/settings` and does not submit this panel's
values.

After editing a domain brand:

1. Use the panel's own `保存域名配置` action.
2. Confirm the domain-brand `PUT` request succeeded.
3. Probe `/api/v1/settings/public` with the target Host and verify the returned
   title, subtitle, contact, API endpoint, and Logo semantics.
4. Reload the target-domain page and verify both the document title and visible
   brand heading.

## Adding A Group

1. Create or activate the group through the normal admin Groups page.
2. Open System Settings, then `域名品牌与分组`.
3. Select the group under exactly one domain and save.
4. If the group has subscription plans, verify both `/payment/plans` and `/payment/checkout-info` on that Host.
5. Verify the other configured domain cannot bind the group or create an order with its plan ID.

Do not add a brand column to `groups` for this first version. Storage remains one validated JSON setting.

## Mandatory Change Surface

When extending this feature, keep all of these aligned:

1. Admin config validation and active-group validation.
2. Middleware placement before embedded frontend and portal handlers.
3. Public Settings response and HTML injection.
4. HTML cache key and ETag isolation by normalized Host.
5. Available groups plus API key create/update enforcement.
6. Subscription plan/checkout filtering plus server-side order validation.
7. Admin UI group picker and API types.
8. Unit tests for fallback, public-setting overlays, empty allowlists, explicit empty Logo, duplicate groups, and forged IDs.

Do not scope existing API key authentication, scheduling snapshots, billing, or gateway forwarding by Host.

## Verification

Backend:

```bash
cd backend
go test -tags unit ./internal/service ./internal/handler/admin ./internal/handler ./internal/server
go test -tags embed ./internal/web -run TestHTMLCache -count=1
```

Frontend:

```bash
cd frontend
pnpm typecheck
pnpm vitest run src/views/admin/__tests__/SettingsView.spec.ts src/views/admin/settings/__tests__/DomainBrandConfigPanel.spec.ts
pnpm build
```

Production probes should preserve the Host header and avoid downloading the full HTML when checking headers:

```bash
curl -fsSI https://DOMAIN/
curl -fsS https://DOMAIN/api/v1/settings/public
curl -fsS -H 'Host: DOMAIN' http://127.0.0.1:8080/api/v1/settings/public
curl -sS -D - -o /dev/null -X OPTIONS \
  -H 'Origin: https://PORTAL_DOMAIN' \
  -H 'Access-Control-Request-Method: GET' \
  -H 'Access-Control-Request-Headers: authorization,content-type' \
  https://API_DOMAIN/api/v1/auth/me
```

Expect the preflight to return `204`, echo the exact portal origin in `Access-Control-Allow-Origin`, and include `Access-Control-Allow-Credentials: true`.

Use an authenticated user session to verify available groups, plans, checkout, API key create/update, and forged cross-domain plan rejection.

## Release Guardrails

- Follow the `sub2api-vps-release` binary deployment skill; do not rebuild Compose on the small VPS.
- Preserve `Host` in Nginx with `proxy_set_header Host $host`.
- Read and back up the live vhost before edits, run `nginx -t`, then reload Nginx narrowly.
- Preserve the live `cors.allowed_origins` block across upstream upgrades. The production list must include `https://xiaohondou.com`, `https://xiaofanqie.org`, `https://nideyiyi.com`, and `https://api.nideyiyi.com`, with `allow_credentials: true`.
- Keep legacy HTTPS hosts as direct compatibility proxies. Use same-Host HTTP `308` only for the HTTP-to-HTTPS upgrade so methods, bodies, paths, and query strings are preserved.
- Do not use cross-domain `301` or `308` for legacy API hosts. The active website migration uses `302` only when Host is `nideyiyi.com`, method is `GET`/`HEAD`, `Accept` contains `text/html`, and the path is not an API/gateway/health path.
- Keep `api.nideyiyi.com` redirect-free. Test both advertised Cloudflare edge IPs with HTML, JSON, SSE, POST, and HTTP-to-HTTPS probes after every Nginx change.
- DNS must resolve before requesting TLS. If it does not, verify app behavior with an explicit Host header and report DNS as the remaining external blocker.
- Commit exactly the tested and shipped code, docs, and this skill before declaring the release complete.

## Pitfalls

- A single global embedded-HTML cache leaks branding across domains.
- Filtering only the frontend group selector is insufficient; create/update and payment order validation must reject forged IDs.
- Treating an empty allowlist as “not configured” exposes every group. Use `Configured` separately from list length.
- `site_logo` needs pointer semantics so missing and explicit empty values remain distinct.
- `contact_info` and `api_base_url` use pointer semantics too: missing inherits global settings; a configured value overrides per Host.
- Resolving domain config on gateway paths adds database work to the inference hot path. Keep those paths bypassed.
- Direct SQL edits do not invoke HTML cache invalidation. Prefer the admin endpoint; otherwise restart the app or invalidate/reload deliberately.
- Missing fingerprinted frontend assets must return `404`; never serve `index.html` for a stale `assets/*.js` request. A browser holding an old lazy-loaded chunk can request it after deployment, and an SPA fallback would render and cache HTML from an asset-bypassed request context, poisoning that Host's injected brand until cache invalidation or restart.
- Values typed into the domain-brand panel remain local form state until its own `保存域名配置` action succeeds. Saving the surrounding System Settings form does not persist them.
- Cross-domain permanent redirects can be cached by browsers, SDKs, and CDN edges. They can keep API clients on an unreachable destination even after the origin config is rolled back.
- A healthy `/health` or server-side `/v1/models` probe does not prove browser API compatibility. Strict CORS can leave those probes green while browser `OPTIONS` requests fail with `403`; always run an explicit preflight probe after an upgrade or domain change.
- Cloudflare origin health does not prove every Anycast route is usable. Pin and probe every advertised edge IP from the actual client network before redirecting legacy traffic to a new zone.
- Do not infer a user's registration brand from the current Host at list time. It must be written during user creation as `registration_site_name`, otherwise historical attribution changes when an administrator renames a brand.
