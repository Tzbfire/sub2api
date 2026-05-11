# Kiro 账号管理 API 文档

> 适配 [kiro-account-manager](https://github.com/hj01857655/kiro-account-manager) 导出格式的 Kiro CodeWhisperer 账号批量管理接口，可对接 any-auto-register / 自建自动注册流水线。

---

## 目录

- [认证](#认证)
- [批量导入](#1-批量导入-kiro-账号)
- [刷新单账号配额](#2-刷新单账号配额)
- [批量刷新配额](#3-批量刷新所有-kiro-账号配额)
- [列出 / 查询账号](#4-列出账号)
- [删除账号](#5-删除账号)
- [测试连接](#6-测试账号连接)
- [推理 API（生产侧）](#7-推理-api生产侧)
- [字段约定](#字段约定)
- [错误码](#错误码)
- [对接示例（curl / Python / Node）](#对接示例)

---

## 认证

所有 admin 接口支持两种认证方式，**自动化场景推荐用 Admin API Key**：

| 方式 | Header | 说明 |
|---|---|---|
| Admin API Key | `x-api-key: sk-admin-xxx` | 在管理后台「设置 → API Keys」创建 type=admin 的 key（永不过期，可吊销） |
| JWT | `Authorization: Bearer <jwt>` | 来自 `POST /api/v1/auth/login`，约 7 天过期 |

**Base URL**：`https://你的域名` 或本地 `http://localhost:8080`

---

## 1. 批量导入 Kiro 账号

### 端点

```
POST /api/v1/admin/accounts/kiro/import
Content-Type: application/json
x-api-key: <Admin API Key>
```

### 请求体

```json
{
  "items": [
    {
      "id": "kam-uuid-or-any",
      "email": "user@example.com",
      "label": "可选展示名",
      "authMethod": "Social",
      "provider": "Google",
      "userId": "...",
      "machineId": "...",
      "accessToken": "aoaAAAAAGo...",
      "refreshToken": "eyJ...",
      "idToken": "eyJ...",
      "expiresAt": "2026-05-12T10:00:00Z",
      "profileArn": "arn:aws:codewhisperer:us-east-1:...",
      "startUrl": "https://...",
      "usageData": { "...": "上游 usage 完整透传，可选" }
    },
    {
      "authMethod": "IdC",
      "clientId": "q9yOBtYAu7Jdhj3b4Y_MZXVzLWVhc3QtMQ",
      "clientSecret": "eyJraWQi...",
      "region": "us-east-1",
      "accessToken": "aoa...",
      "refreshToken": "eyJ...",
      "expiresAt": "2026-05-12T10:00:00Z"
    }
  ],
  "group_ids": [1, 2],
  "concurrency": 5,
  "skip_mixed_channel_check": false
}
```

#### 字段表

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `items` | `[]Item` | ✅ | 账号数组；可直接传 KAM 导出 JSON（你的 `kiro-accounts-*.json`） |
| `group_ids` | `[]int64` | ⬜ | 导入后绑定到这些 group |
| `concurrency` | `int` | ⬜ | 并行导入度，默认 5 |
| `skip_mixed_channel_check` | `bool` | ⬜ | 跳过同 group 多 channel 类型校验 |

#### `items[]` 字段

| 字段 | 类型 | Social 必填 | IdC 必填 | 说明 |
|---|---|---|---|---|
| `id` | string | ⬜ | ⬜ | 上游 id，仅用于去重显示 |
| `email` | string | ⬜ | ⬜ | 邮箱（展示用） |
| `label` | string | ⬜ | ⬜ | 自定义展示名 |
| `authMethod` | string | ✅ | ✅ | `"Social"` 或 `"IdC"` |
| `provider` | string | ✅ | ⬜ | `Google` / `Github` / `BuilderId` |
| `accessToken` | string | ✅ | ✅ | Kiro access token |
| `refreshToken` | string | ✅ | ✅ | Kiro refresh token（必须有） |
| `idToken` | string | ⬜ | ⬜ | OIDC id_token（可选） |
| `expiresAt` | string | ⬜ | ⬜ | 过期时间字符串（仅展示，不解析） |
| `userId` | string | ⬜ | ⬜ | 上游 user id |
| `machineId` | string | ⬜ | ⬜ | 上游 machine id |
| `profileArn` | string | ✅ | ⬜ | Social 必填，发到 CodeWhisperer 的 ARN |
| `startUrl` | string | ⬜ | ⬜ | Social SSO start URL |
| `clientId` | string | ⬜ | ✅ | IdC 必填，registered client id |
| `clientSecret` | string | ⬜ | ✅ | IdC 必填，registered client secret |
| `region` | string | ⬜ | ✅ | IdC 必填，AWS region |
| `usageData` | object | ⬜ | ⬜ | 完整 usage 透传，存到 `Account.Extra["kiro_usage_data"]` |

> 💡 **不强校验**：缺字段不会报错，但运行时调用上游会失败 → 自动进入 quarantine。建议导入前自检。

### 响应

```json
{
  "results": [
    {"index": 0, "id": "abc", "email": "u@x.com", "created": true},
    {"index": 1, "created": false, "error": "missing refresh_token"}
  ],
  "summary": {"total": 2, "succeeded": 1, "failed": 1}
}
```

### curl 示例

```bash
# 标准格式（{items:[...]}）
curl -X POST https://你的域名/api/v1/admin/accounts/kiro/import \
  -H "x-api-key: $SUB2API_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d @payload.json

# KAM 导出的纯数组：用 jq 包一层
jq '{items: ., group_ids: [1]}' kiro-accounts.json | \
  curl -X POST https://你的域名/api/v1/admin/accounts/kiro/import \
    -H "x-api-key: $SUB2API_ADMIN_KEY" \
    -H "Content-Type: application/json" \
    --data-binary @-
```

---

## 2. 刷新单账号配额

```
POST /api/v1/admin/accounts/{id}/kiro-usage
x-api-key: <Admin API Key>
```

调用上游 `GetUsage`，把最新 quota 写入 DB。

**响应**：

```json
{
  "code": 0,
  "data": {
    "account_id": 123,
    "usage": {
      "subscription_status": "ACTIVE",
      "ai_request_count": 12,
      "ai_request_limit": 1000,
      "next_refill_at": "2026-06-01T00:00:00Z",
      "...": "完整 usageData 透传"
    }
  }
}
```

---

## 3. 批量刷新所有 Kiro 账号配额

```
POST /api/v1/admin/accounts/kiro/batch-refresh-usage
x-api-key: <Admin API Key>
Content-Type: application/json

{
  "ids": [1, 2, 3]    // 可选，缺省刷新所有 kiro 平台账号
}
```

**响应**：

```json
{
  "total": 50,
  "success": 47,
  "failed": 3,
  "details": [
    {"id": 12, "ok": false, "error": "rate limited"}
  ]
}
```

---

## 4. 列出账号

```
GET /api/v1/admin/accounts?platform=kiro&page=1&page_size=100
x-api-key: <Admin API Key>
```

`page_size` 上限 1000。返回标准账号列表 + Kiro 专属字段（`extra.kiro_usage_data`）。

---

## 5. 删除账号

```
DELETE /api/v1/admin/accounts/{id}
x-api-key: <Admin API Key>
```

---

## 6. 测试账号连接

```
POST /api/v1/admin/accounts/{id}/test
x-api-key: <Admin API Key>
```

实际向 Kiro CodeWhisperer 发一次最小 ping，验证 token 可用性。

---

## 7. 推理 API（生产侧）

导入完账号、绑定到 group、给 group 配 API Key 后，外部调用统一 OpenAI/Anthropic 协议：

| 协议 | 端点 |
|---|---|
| OpenAI Chat | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Anthropic Messages | `POST /v1/messages` |

均支持：
- ✅ stream + 非 stream
- ✅ Tool calling（含多轮 tool result feedback）
- ✅ 多账号 fail-over（最多 10 次）+ 智能 quarantine（429=5min, 423=30min）

**Header**：`Authorization: Bearer <生产 API Key>`

---

## 字段约定

### 平台标识

`platform` = `"kiro"`，`platform_type` = `"kiro_oauth"`。

### Quarantine 冷却时间表

| 上游状态 | cooldown |
|---|---|
| 429 Too Many Requests | 5 分钟 |
| 423 Locked | 30 分钟 |
| 401 / 403 | 5 分钟 |
| 503 / 529 | 60 秒 |
| 4xx 业务错（请求格式错等） | **0 秒**（不切账号，避免坏客户端烧账号池） |
| 5xx / 网络错 | 立即切下一个账号 |

### Trace ID 透传

每次推理响应头会带：

```
X-Upstream-Request-Id: <X-Amzn-Requestid>
```

排查 quarantine 原因时直接拿这个去 Kiro 后台查。

---

## 错误码

| HTTP | code | 说明 |
|---|---|---|
| 400 | `bad_request` | items 为空 / JSON 格式错 |
| 401 | `unauthorized` | x-api-key 无效或非 admin |
| 503 | `account_disabled` | group 内所有 Kiro 账号都被 quarantine |
| 503 | `exhausted_retries` | 10 次 fail-over 全失败 |

---

## 对接示例

### Python（推荐用于 any-auto-register）

```python
import requests
import json

API_BASE = "https://你的域名"
ADMIN_KEY = "sk-admin-xxx"

def import_kiro_accounts(items: list, group_ids: list[int] | None = None) -> dict:
    resp = requests.post(
        f"{API_BASE}/api/v1/admin/accounts/kiro/import",
        headers={
            "x-api-key": ADMIN_KEY,
            "Content-Type": "application/json",
        },
        json={
            "items": items,
            "group_ids": group_ids or [],
            "concurrency": 5,
        },
        timeout=60,
    )
    resp.raise_for_status()
    return resp.json()

# 直接用 KAM 导出文件
with open("kiro-accounts-1-2026-05-10.json") as f:
    items = json.load(f)

result = import_kiro_accounts(items, group_ids=[1])
print(f"成功 {result['summary']['succeeded']} / {result['summary']['total']}")
for r in result["results"]:
    if not r["created"]:
        print(f"  失败 #{r['index']}: {r.get('error')}")
```

### Node.js

```js
const fs = require('node:fs/promises');

const API_BASE = 'https://你的域名';
const ADMIN_KEY = process.env.SUB2API_ADMIN_KEY;

async function importKiroAccounts(items, groupIds = []) {
  const r = await fetch(`${API_BASE}/api/v1/admin/accounts/kiro/import`, {
    method: 'POST',
    headers: {
      'x-api-key': ADMIN_KEY,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ items, group_ids: groupIds, concurrency: 5 }),
  });
  if (!r.ok) throw new Error(`HTTP ${r.status}: ${await r.text()}`);
  return await r.json();
}

const items = JSON.parse(await fs.readFile('kiro-accounts.json', 'utf8'));
const result = await importKiroAccounts(items, [1]);
console.log(`成功 ${result.summary.succeeded}/${result.summary.total}`);
```

### Go

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func main() {
	raw, _ := os.ReadFile("kiro-accounts.json")
	var items []map[string]any
	_ = json.Unmarshal(raw, &items)

	body, _ := json.Marshal(map[string]any{
		"items":       items,
		"group_ids":   []int{1},
		"concurrency": 5,
	})

	req, _ := http.NewRequest("POST",
		"https://你的域名/api/v1/admin/accounts/kiro/import",
		bytes.NewReader(body))
	req.Header.Set("x-api-key", os.Getenv("SUB2API_ADMIN_KEY"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil { panic(err) }
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	fmt.Printf("%+v\n", result["summary"])
}
```

---

## any-auto-register 对接建议

1. 在管理后台创建一个「Kiro 自动注册」**专属 admin API Key**，加好备注便于日后吊销
2. 注册流水线产出 KAM 兼容 JSON 后直接 POST 到 `/kiro/import`，**带上 `group_ids`** 自动入组
3. 强烈建议加：
   - 失败重试：HTTP 5xx 重试 3 次，4xx 不重试
   - 增量导入：导入前用 `GET /accounts?platform=kiro&q=<email>` 查重
   - 注册完成后立即调用 `POST /accounts/{id}/kiro-usage` 拉一次配额，确认账号可用
4. 监控指标：
   - 导入成功率 = `summary.succeeded / summary.total`
   - 账号可用率 = 总数 - quarantined 数（看 admin 列表的 status 字段）

---

## 变更记录

| 日期 | 版本 | 说明 |
|---|---|---|
| 2026-05-11 | v1.0 | 首版，覆盖三协议（chat/messages/responses）+ tool calling + KAM 导入兼容 |
