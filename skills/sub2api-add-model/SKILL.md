---
name: sub2api-add-model
description: 为 Sub2API 补加 OpenAI 文本模型、更新官方定价，并用模型清单和生成脚本同步默认列表、计费兜底及客户端配置。用户说新增模型、补模型、按官网更新模型价格时使用。
---

# Sub2API 补加模型

先确认用户给出的精确模型 ID 和价格例外。源码仓库为
`/Users/chendeyao/work/sub2api-upstream-brand-login`；该目录可能停留在旧分支。
使用 `git worktree list --porcelain` 定位当前 `main`，读取状态、fetch `cdy main`，
从最新主线建立隔离的 `cdy/` 工作分支，不覆盖其他任务工作区。
下文 `REPO` 指本次隔离工作区的绝对路径。

## 核实官网

读取 OpenAI Docs 技能并实际获取官方定价页及精确模型页：
`https://developers.openai.com/api/docs/pricing/`、
`https://developers.openai.com/api/docs/models/MODEL_ID`。
记录核实日期、USD/百万 token 的输入/缓存读取/输出、Fast/Flex 倍率、
长上下文阈值与倍率、context/output 上限、支持的 reasoning levels。
促销说明必须归属到精确型号，不能把 GPT-5.6 Sol 的优惠期当成 GPT-6 Sol。
不从旧型号名称推断新型号价格、OAuth 账号权限或 Ultra/Responses Lite 支持。

2026-09-23 用户明确要求：GPT-6 Sol/Luna **缓存写入免费（0）**，其他价格对齐官网。
这是站点价格例外，不是官网免费；保留缓存读取费用。只用于这两款型号，
未来新型号的缓存写入政策必须按新需求确定。

## 清单与生成脚本

维护 `skills/sub2api-add-model/scripts/openai-supplemental.json`；价格单位是 USD/百万 token。
更新 `verified_at` 和每项 `source`。保留已有模型，修改相应条目或追加新条目。
脚本负责四个数据面：Go 默认模型/上下文、两套 Go 计费静态表、前端配置、
`backend/resources/model-pricing/model_prices_and_context_window.json`。

```sh
python3 "$REPO/skills/sub2api-add-model/scripts/sync_openai_models.py" --repo "$REPO"
python3 "$REPO/skills/sub2api-add-model/scripts/sync_openai_models.py" --repo "$REPO" --write
python3 "$REPO/skills/sub2api-add-model/scripts/sync_openai_models.py" --repo "$REPO" --check
```

默认只预览，`--write` 写入，`--check` 漂移返回非零；重复生成必须无差异。
生成脚本不联网、不修改生产、不提交、不发布。不要手改 `*_generated.go`。
当前生成器只支持已验证的 Responses 文本/图像输入、none 至 max、Flex 0.5x、
缓存写入免费合同。不同合同应先扩展生成器和运行时及测试，不能伪造字段绕过校验。

## 检查完整链路

- `openai.DefaultModels`、`getNormalizedCodexModel`、OAuth 模型转换：精确名、供应商前缀、日期后缀；不能把 Sol/Luna 映射成 Astra。裸 `gpt-6` 仍属于原有 Astra 别名。
- `PricingService.GetModelPricing` 与 `BillingService.getFallbackPricing`：两套兜底都必须可用。远端价格表可能缺失新型号，也可能重新带上官网缓存写入费用；免费写入政策不能被刷新覆盖。
- 分组/渠道显式定价沿用原有优先级；默认价格政策不覆盖管理员自定义价格。
- Codex descriptor：reasoning、context、图片输入、Fast；API key 路径禁止误启 Responses Lite。不要为新型号复制 Astra 专用 Pro/Ultra 或 ChatGPT 内部工具参数。
- 前端白名单、预设映射、UseKeyModal 生成的 OpenCode 配置使用同一生成数据；展示成功不代表上游账号有调用权限。
- 新增默认模型不会改写现存账号映射、分组 allowlist、固定 `/v1/models` 列表。确需修改这些生产配置时另读实际配置和授权。

## 验证与交付

使用相同输入验证修改前、修改后和独立副本回滚后行为，保留源哈希、补丁、
literal stdout/stderr/退出码及可执行回滚脚本。证据放仓库外，不提交证据和凭据。

```sh
cd "$REPO/backend"
CGO_ENABLED=0 go test ./internal/pkg/openai ./internal/service -run 'Supplemental|GPT6|Pricing|Reasoning|CodexModel' -count=1
cd "$REPO/frontend"
pnpm exec vitest run src/components/account/__tests__/ModelWhitelistSelector.spec.ts src/components/keys/__tests__/UseKeyModal.spec.ts
pnpm run typecheck
```

至少覆盖两个兜底、远端目录更新、标准/Fast/priority/Flex、272000 与 272001
的边界、输入/缓存读取/输出成本、缓存写入为零、模型别名和客户端配置。
Mac 本地若默认 CGO 链接报 `unknown architecture arm64e.x1`，保留错误证据后
改用 `CGO_ENABLED=0`；不要修改系统 SDK。

按 `sub2api-vps-release` 技能完成测试、提交、合并 `main`、推送 `cdy/main`
及 SHA 回读。代码交付与生产发布分开：只有用户明确要求发布时才执行五节点流程；
同时同步 `backend/resources`。新模型的真实上游调用能力须另有请求证据，
不能用单元测试代替。最终给出三项官网价格、写入免费例外、交付/发布状态及技能路径。
