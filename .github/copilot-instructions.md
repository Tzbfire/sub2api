# Copilot / AI Agent Instructions — sub2api

> 所有 AI 协作者（Copilot CLI / Claude / Cursor / 等）在本仓库写代码前必读。

## Go Lint 红线（CI 强制，会直接 fail backend-ci）

仓库 `backend/.golangci.yml` 启用了 **`errcheck.check-type-assertions: true`** 和 `disable-default-exclusions: true`，比默认严格很多。常见踩坑：

### 1. 类型断言必须用 comma-ok 形式

❌ 错误（CI 会报 `Error return value is not checked (errcheck)`）：
```go
return v.(*req.Client)
return actual.(*MyType)
x := iface.(Concrete)
```

✅ 正确：
```go
if c, ok := v.(*req.Client); ok {
    return c
}
// 或在确定不会失败时显式 panic / fallback
c, _ := v.(*req.Client) // 仅在 check-blank: false 时允许；但更推荐 ok 分支
```

`sync.Map.Load` / `LoadOrStore` 的返回值断言尤其要注意。

### 2. 错误返回值不能直接丢

❌ `someFunc()` 当 `someFunc` 返回 error 时
✅ `if err := someFunc(); err != nil { ... }` 或 `_ = someFunc()` 显式忽略（注意 `check-blank: false` 仅放行赋值给 `_` 的写法）

### 3. 默认 exclusion 已关闭

`disable-default-exclusions: true` 意味着像 `(*os.File).Close()`、`(*bytes.Buffer).Write()` 等"通常可忽略"的错误也会被报。要么处理，要么加入 `.golangci.yml` 的 `exclude-functions` 白名单（需谨慎）。

### 4. 其他启用的 linter

`depguard` / `gosec` / `govet` / `ineffassign` / `staticcheck` / `unused`，以及 `gofmt`（含 `interface{}` → `any` 重写规则）。提交前如本地装了 `golangci-lint`，跑：

```bash
cd backend && golangci-lint run --timeout 5m ./...
```

> pre-commit hook 检测到未安装 golangci-lint 会跳过深度 lint —— **跳过 ≠ 通过**，CI 仍会跑。

## 架构红线（depguard 强制）

- `internal/service/**` **不得** 直接 import `internal/repository`、`gorm.io/gorm`、`go-redis/v9`（少数 ops_* 文件例外，见 `.golangci.yml`）
- `internal/handler/**` 同上

## 提交 / 发布流程

- 镜像构建由 push `v*` tag 触发 `.github/workflows/release.yml`
- VERSION 文件单独由 `chore: sync VERSION` commit 维护，带 `[skip ci]`
- 推 main 之前先确保 backend-ci 能过；force push 前用 `--force-with-lease`

## 历史踩坑案例

- **2026-05-16** `v0.1.133` 之后引入 `driver_responses.go` 的 `clientForProxy`，单值类型断言直接挂掉 backend-ci。修复见 commit `b503e7b9`。
