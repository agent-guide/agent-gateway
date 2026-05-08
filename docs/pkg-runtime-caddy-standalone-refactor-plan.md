# Core Runtime Package Refactor Plan

## Goal

将核心运行时逻辑从 Caddy 模块适配层中拆出来，统一放到 `pkg/` 下，并在此基础上提供两个启动形态：

1. Caddy module 版本：继续作为自定义 Caddy 二进制运行，保留现有 Caddyfile 和 Caddy Admin API 集成。
2. Standalone 版本：不依赖 Caddy runtime，直接用标准 `net/http` 启动 LLM gateway 和 Admin API。

这次重构的目标不是简单移动目录，而是建立清晰边界：

- `pkg/...` 只放可复用核心逻辑，不直接依赖 Caddy。
- `caddy/...` 只放 Caddy 模块注册、Caddyfile 解析、`caddy.Context` 适配。
- `cmd/...` 只负责选择启动形态和装配依赖。

## Current State

当前核心包主要位于仓库根级目录：

```text
admin/
cliauth/
configstore/
dispatcher/
gateway/
llm/
pkg/httpclient/
```

其中一部分已经接近可复用 runtime：

- `gateway/agentgateway.go`：主要是 runtime orchestration，可迁入 `pkg/gateway`。
- `admin/handler.go`：已经是普通 `net/http` handler，可迁入 `pkg/admin`。
- `cliauth/`、`llm/credentialmgr/`、`gateway/route/`、`gateway/virtualkey/`：大多数逻辑不需要 Caddy。

但也有明显 Caddy 耦合：

- `gateway/app.go`：Caddy app module bootstrap。
- `gateway/caddyfile.go`：Caddyfile global option parser。
- `dispatcher/dispatcher.go`：当前直接实现 `caddyhttp.MiddlewareHandler`。
- `dispatcher/common.go`：`LLMApiHandler` 接口嵌入了 `caddy.Module`。
- `admin/module.go`：Admin API 的 Caddy middleware adapter。
- `configstore/sqlite/configstore.go`：`Provision(caddy.Context)`、`caddy.AppDataDir()` 和 Caddy logger 混在 SQLite store 里。
- `llm/provider/*/provider.go`：Provider runtime、provider factory 注册、Caddy module 注册和 Caddyfile 解析混在同一个包里。

## Feasibility

该重构可行，而且有利于后续维护。主要收益：

- Standalone 启动不再被 Caddy 生命周期和 Caddyfile 绑定。
- 核心 runtime 更容易测试，集成测试可以绕过 Caddy module loader。
- Provider、protocol adapter、Admin API 能被不同启动器复用。
- Caddy module 版本变成一层薄适配器，升级或替换启动形态更容易。

主要风险：

- 导入路径变化大，需要分阶段控制 PR 粒度。
- Caddy module ID 必须保持兼容，否则会破坏现有 Caddyfile。
- Provider 和 LLM API handler 当前通过 `init()` 同时注册 runtime factory 和 Caddy module，需要拆分后保证两种注册路径都完整。
- 如果一次性移动所有目录，测试和文档会出现大量无关 diff，建议采用兼容壳过渡。

## Target Package Layout

建议最终结构：

```text
pkg/
  admin/
  cliauth/
  cliauth/authenticator/
  configstore/intf/
  configstore/sqlite/
  dispatcher/
  dispatcher/llmapi/openai/
  dispatcher/llmapi/anthropic/
  gateway/
  gateway/modelcatalog/
  gateway/route/
  gateway/virtualkey/
  llm/credentialmgr/
  llm/credentialmgr/model/
  llm/credentialmgr/scheduler/
  llm/provider/
  llm/provider/openai/
  llm/provider/anthropic/
  llm/provider/gemini/
  llm/provider/ollama/
  llm/provider/openrouter/
  llm/provider/deepseek/
  llm/provider/zhipu/
  httpclient/

caddy/
  admin/
  dispatcher/
  dispatcher/llmapi/openai/
  dispatcher/llmapi/anthropic/
  gateway/
  configstore/sqlite/
  provider/openai/
  provider/anthropic/
  provider/gemini/
  provider/ollama/
  provider/openrouter/
  provider/deepseek/
  provider/zhipu/

standalone/
  server/
  config/
  bootstrap/

cmd/
  agw/
  agwd/
  agwctl/
```

Notes:

- `pkg/gateway.AgentGateway.Bootstrap(...)` 是核心 runtime bootstrap 入口。
- `standalone/` 与 `caddy/` 并列，负责独立启动版本的配置解析、HTTP server 装配和进程生命周期适配。
- `cmd/agw` 保持 Caddy module 版本。
- `cmd/agwd` 是 standalone daemon 的薄入口，实际实现放在 `standalone/`。
- `cmd/agwctl` 可以暂时不迁入本次主线，后续再更新导入路径。

## Boundary Rules

### `pkg/...`

`pkg` 包允许依赖：

- Go 标准库
- 第三方 provider/client SDK
- `go.uber.org/zap` 或内部 logger abstraction
- 本仓库其他 `pkg/...` 和必要的 `internal/...`

`pkg` 包不应依赖：

- `github.com/caddyserver/caddy/v2`
- `github.com/caddyserver/caddy/v2/modules/caddyhttp`
- `github.com/caddyserver/caddy/v2/caddyconfig/caddyfile`

例外只允许短期过渡，并应在对应 issue 或 TODO 中标记清理阶段。

### `caddy/...`

`caddy` 包负责：

- `caddy.RegisterModule(...)`
- `CaddyModule() caddy.ModuleInfo`
- `Provision(ctx caddy.Context)`
- `Start()` / `Stop()` 生命周期适配
- `UnmarshalCaddyfile(...)`
- Caddy module namespace 和 module ID 兼容
- 将 Caddy module config 转成 `pkg` runtime config

`caddy` 包不应承载核心请求处理、provider 调用、路由解析、凭证选择等业务逻辑。

### `standalone/...`

`standalone` 包负责：

- standalone 配置文件格式和解析。
- standalone 默认值和路径策略。
- HTTP listener、router 和 shutdown lifecycle。
- 将 standalone config 转成 config store、providers、routes、virtual keys、credential manager、CLI auth manager 等 runtime 依赖。
- 创建 `pkg/gateway.AgentGateway` 并调用 `AgentGateway.Bootstrap(...)`。
- 装配 Admin API 和 LLM dispatcher。

`standalone` 包与 `caddy` 包是并列入口适配层。它可以依赖 `pkg/...`，但不应承载 provider 调用、路由解析、凭证选择等核心业务逻辑。

## Target Runtime Shape

### Runtime Bootstrap

核心 runtime bootstrap 保持在 `pkg/gateway.AgentGateway` 上：

```go
gateway := gateway.NewAgentGateway()
err := gateway.Bootstrap(ctx, gateway.BootstrapOptions{
    StaticRoutes:        routes,
    StaticVirtualKeys:   virtualKeys,
    StaticProviders:     providers,
    StaticModels:        models,
    ConfigStore:         configStore,
    CLIAuthManager:      cliAuthManager,
    CLIAuthRefresher:    cliAuthRefresher,
    CredentialManager:   credentialManager,
    CredentialScheduler: credentialScheduler,
})
```

`AgentGateway.Bootstrap(...)` 只负责接收已经构造好的依赖并初始化 runtime managers。它不负责读取配置文件、不负责 Caddy module loading、不负责创建 HTTP server。

`caddy/` 和 `standalone/` 是两个并列装配层：

- `caddy/` 从 Caddyfile/JSON 和 Caddy module loader 构造 runtime 依赖，然后调用 `AgentGateway.Bootstrap(...)`。
- `standalone/` 从 standalone config 直接构造 runtime 依赖，然后调用 `AgentGateway.Bootstrap(...)`。

这样避免新增一个过宽的 `pkg/server` 共享层，也避免把 Caddy 和 standalone 的差异塞进同一个 bootstrap abstraction。

### Caddy Version

Caddy 版本继续保持现有用户接口：

```text
agent_gateway
http.handlers.agent_route_dispatcher
http.handlers.agent_gateway_admin
llm.providers.*
agent_route_dispatcher.llm_apis.*
agent_gateway.config_stores.sqlite
```

模块 ID 不应改变。即使代码移动到 `caddy/...`，Caddyfile 中的模块名仍保持兼容。

Caddy app adapter 的职责：

1. 解析 Caddyfile/JSON config。
2. 通过 Caddy module loader 加载 provider、config store、LLM API handler adapter。
3. 创建 credential manager、CLI auth manager/refresher 和其他 runtime 依赖。
4. 创建 `pkg/gateway.AgentGateway` 并调用 `AgentGateway.Bootstrap(...)`。
5. 在 `Start()` 中启动 refresher，在 `Stop()` 中关闭后台任务。

### Standalone Version

Standalone 版本建议新增：

```text
standalone/
cmd/agwd
```

基础启动流程：

```text
cmd/agwd
  -> standalone/server.Run(...)
  -> read config file
  -> build config store, providers, credentials, routes, virtual keys
  -> AgentGateway.Bootstrap(...)
  -> mount dispatch handler
  -> mount admin handler
  -> http.ListenAndServe(...)
```

建议先支持 JSON 或 YAML 配置，不建议第一阶段复用 Caddyfile。Caddyfile 是 Caddy adapter 的职责，standalone 配置应直接面向 runtime model。

示例 HTTP mount：

```text
/admin/  -> pkg/admin.Handler
/*       -> pkg/dispatcher.Handler
```

`cmd/agwd/main.go` 应保持很薄，只处理 CLI flags、日志初始化和调用 `standalone/server.Run(...)`。独立服务的配置 schema、默认值、HTTP mux、graceful shutdown 和 runtime 装配应放在 `standalone/` 下。

## Refactor Phases

重构顺序应遵循一个原则：**先逐模块剥离 Caddy 耦合并保持 Caddy 版本可运行，等所有核心模块都迁入 `pkg` 后，再实现 standalone 入口**。

每个模块拆分阶段都应满足：

- Caddy adapter 继续存在，并调用新的 `pkg/...` runtime。
- Caddy module ID 和 Caddyfile 行为保持兼容。
- 该阶段结束后 `go test ./...` 和 Caddy binary build 仍可通过。

Standalone 不应在核心模块剥离完成前开始实现。它应该作为最后的装配验证：如果 `pkg` 边界足够干净，standalone 只需要解析配置、创建依赖、调用 `AgentGateway.Bootstrap(...)`、挂载 Admin 和 Dispatcher handler。

迁移顺序应尽量自底向上：先迁依赖少、被依赖多的底层包，再迁依赖多的聚合层和 HTTP 入口层。当前推荐顺序是：

```text
configstore/intf
  -> llm/credentialmgr
  -> cliauth
  -> llm/provider
  -> configstore/sqlite
  -> gateway
  -> dispatcher
  -> admin
  -> standalone
```

Model config storage should not depend on `gateway/modelcatalog`. The storage layer should depend only on a minimal key contract in `configstore/intf`, while `modelcatalog.ManagedModel` implements that contract.

### Phase 0: Safety Baseline

目标：建立迁移前的可验证基线。

Tasks:

- 跑通 `go test ./...`。
- 跑通 `go build -o agw ./cmd/main.go` 或当前 Makefile。
- 记录当前 Caddyfile example 的 smoke test 命令。
- 确认 README、DESIGN、AGENTS 中提到的模块 ID 和路径。

Exit criteria:

- 当前 mainline 行为已验证。
- 后续每个阶段都能用同一组命令回归。

### Phase 1: Extract Config Store Interfaces And Credential Manager

目标：先迁底层存储接口和 credential manager，降低后续模块的 import churn。

Tasks:

- 将 `configstore/intf` 迁入 `pkg/configstore/intf`。
- 将 `llm/credentialmgr` 迁入 `pkg/llm/credentialmgr`。
- 将 `llm/credentialmgr/model` 和 `llm/credentialmgr/scheduler` 迁入 `pkg/llm/credentialmgr/...`。
- 更新现有调用方导入路径，必要时保留旧路径 alias 作为短期兼容层。

Exit criteria:

- `pkg/configstore/intf` 和 `pkg/llm/credentialmgr` 不依赖 Caddy。
- credential manager tests 通过。
- Caddy binary 仍能构建。

### Phase 2: Extract CLI Auth Runtime

目标：将 CLI auth runtime 迁入 `pkg`，保持 authenticator factory 注册不依赖 Caddy。

Tasks:

- 将 `cliauth` 迁入 `pkg/cliauth`。
- 将 `cliauth/authenticator` 迁入 `pkg/cliauth/authenticator`。
- 更新 `cmd/agwctl` 和其他调用方导入路径。
- 确认 authenticator factory 注册路径仍由 blank import 控制。

Exit criteria:

- `pkg/cliauth` 不依赖 Caddy。
- cliauth tests 通过。
- `cmd/agwctl` 能构建。

### Phase 3: Extract Provider Runtime

目标：Provider 实现和 Caddy module 适配拆开。

Tasks:

- 将 `llm/provider` 迁入 `pkg/llm/provider`。
- 先用 `openai` provider 做模板，将 runtime 实现迁入 `pkg/llm/provider/openai`。
- 验证模板后，再机械迁移 `anthropic`、`gemini`、`ollama`、`openrouter`、`deepseek`、`zhipu` 等 provider。
- Provider runtime 包只注册 `provider.RegisterProviderFactory(...)`。
- Caddy provider adapter 包负责 `caddy.RegisterModule(...)`、`CaddyModule()`、`Provision(caddy.Context)`、`UnmarshalCaddyfile(...)`。
- 将通用 Caddyfile provider config parser 放到 `caddy/provider` 或 `caddy/internal/providerconfig`，不要放在 `pkg/llm/provider`。

Exit criteria:

- Runtime 可以通过 provider factory 创建 provider。
- Caddy 可以通过 `llm.providers.<name>` module 创建 provider。
- Provider capability 和 request tests 通过。
- Caddy binary 仍能构建，现有 provider Caddyfile 配置保持兼容。

### Phase 4: Extract Config Store Runtime

目标：SQLite config store 可被 Caddy 和 standalone 共同使用。

Tasks:

- 将 SQLite store 主逻辑迁入 `pkg/configstore/sqlite`。
- 新增普通构造函数，例如 `sqlite.Open(ctx, Config, logger)`。
- Caddy SQLite adapter 负责默认路径 `caddy.AppDataDir()`，再调用 runtime 构造函数。
- Standalone 默认路径由 `standalone/config` 或 `standalone/bootstrap` 决定，不使用 `caddy.AppDataDir()`。
- 保持 SQLite model store 只依赖 `configstore/intf.ModelStorageKeyer` 这类最小 key contract，不反向依赖 `gateway/modelcatalog`。

Exit criteria:

- `pkg/configstore/sqlite` 不依赖 Caddy。
- Caddy module ID `agent_gateway.config_stores.sqlite` 不变。
- SQLite store tests 通过。

### Phase 5: Extract Gateway Runtime

目标：将 `AgentGateway` runtime 迁入 `pkg`，并保留 `AgentGateway.Bootstrap(...)` 作为核心 runtime 初始化入口。

Tasks:

- 将 `gateway/agentgateway.go`、`providerresolver.go`、`routedprovider.go` 迁入 `pkg/gateway`。
- 将 `gateway/route`、`gateway/virtualkey`、`gateway/modelcatalog` 迁入 `pkg/gateway/...`。
- 保留并整理 `AgentGateway.Bootstrap(...)`，确保它只接收已构造好的 runtime 依赖并初始化 managers。
- 将 `gateway/app.go` 中的 Caddy 专用装配逻辑迁到 `caddy/gateway`。
- Caddy app adapter 只保留 Caddy module config、Caddyfile parsing 和 lifecycle。
- 保留旧 `gateway` 包的短期兼容 alias，或一次性更新所有导入路径。推荐短期兼容 alias，降低 PR 风险。

Exit criteria:

- `pkg/gateway` 不依赖 Caddy。
- Caddy app module ID `agent_gateway` 不变。
- 现有 gateway tests 通过。

### Phase 6: Extract Dispatcher And Admin Core

目标：最后迁移依赖较多的 HTTP 入口层，让 dispatcher 和 admin 调用已经稳定的 `pkg` runtime。

Tasks:

- 将 `dispatcher.LLMApiHandler` 中的 `caddy.Module` 移除，变成纯 runtime 接口。
- 新增 `pkg/dispatcher.Handler`，实现普通 `http.Handler` 或接近 `http.Handler` 的 service。
- 将当前 `AgentRouteDispatcher.ServeHTTP(...)` 的主逻辑迁入 `pkg/dispatcher`。
- Caddy dispatcher adapter 保留 `caddyhttp.MiddlewareHandler`，内部调用 `pkg/dispatcher`。
- OpenAI/Anthropic handler 的核心逻辑迁入 `pkg/dispatcher/llmapi/...`。
- Caddy LLM API module adapter 只负责注册模块、Provision logger、构造 runtime handler。
- 将 `admin/handler.go`、路由处理、session、models/providers/routes/virtual_keys 等主逻辑迁入 `pkg/admin`。
- 将 `admin/module.go` 保留为 `caddy/admin` adapter。
- Admin adapter 的 `Provision` 从 Caddy app 取得 `AgentGateway` 后调用 `pkg/admin.NewHandler(...)`。

Exit criteria:

- `pkg/dispatcher` 和 `pkg/admin` 不依赖 Caddy。
- 现有 dispatcher 和 admin tests 通过。
- 新增至少一个不依赖 Caddy 的 dispatcher runtime test。
- Caddyfile 里的 `llm_api openai`、`llm_api anthropic` 行为不变。
- Caddy module ID `http.handlers.agent_route_dispatcher` 和 `http.handlers.agent_gateway_admin` 不变。

### Phase 7: Add Standalone Server

目标：在所有核心模块完成 `pkg` 迁移后，实现可运行的 standalone gateway。

Tasks:

- 确认 `pkg/admin`、`pkg/dispatcher`、`pkg/gateway`、`pkg/configstore`、`pkg/llm/provider`、`pkg/cliauth`、`pkg/llm/credentialmgr` 均不依赖 Caddy。
- 新增 `standalone/` 目录，与 `caddy/` 并列。
- 新增 `cmd/agwd`。
- 将 standalone server 的实际实现放到 `standalone/server`，`cmd/agwd` 只做薄 main。
- 将 standalone 配置 schema、解析和默认值放到 `standalone/config`。
- 定义 standalone config 格式。建议先用 JSON，后续再加 YAML。
- 支持配置：
  - listen address
  - admin listen/path
  - config store
  - providers
  - routes
  - virtual keys
  - managed models
  - enabled provider types
  - enabled llm api handler types
- 创建 `pkg/gateway.AgentGateway` 并调用 `AgentGateway.Bootstrap(...)`。
- 启动前加载 provider/authenticator/llmapi runtime factories。
- 提供最小 example config。

Exit criteria:

- `go build -o agwd ./cmd/agwd` 通过。
- 使用 example config 可以完成一次 OpenAI-compatible 或 Anthropic-compatible smoke test。
- Admin API 可访问。

### Phase 8: Cleanup Compatibility Shells

目标：清理短期兼容层，稳定最终包边界。

Tasks:

- 移除旧根级 runtime 包，或只保留明确需要的 backward compatibility aliases。
- 更新 README、DESIGN、AGENTS。
- 更新 `cmd/main.go` 到新的 Caddy adapter import 路径。
- 更新 `Makefile`：
  - `make build` 构建 `agw`、`agwd`、`agwctl`
  - 保留单独 build targets
- 检查 `rg "github.com/agent-guide/caddy-agent-gateway/(admin|dispatcher|gateway|llm|cliauth|configstore)"`，确认旧导入路径是否都已处理。

Exit criteria:

- `go test ./...` 通过。
- `go vet ./...` 通过。
- Caddy module 版本和 standalone 版本都能构建并完成 smoke test。

## Suggested PR Breakdown

推荐按以下顺序提交，避免一个 PR 同时处理太多 import churn：

1. Move `configstore/intf` and `llm/credentialmgr` into `pkg`.
2. Move `cliauth` and authenticators into `pkg`.
3. Move provider core and one provider runtime first, preferably `openai`.
4. Migrate remaining provider runtimes.
5. Move SQLite config store runtime into `pkg`, preserving the storage-layer boundary around `configstore/intf`.
6. Move `gateway` runtime into `pkg`, preserving `AgentGateway.Bootstrap(...)`.
7. Move dispatcher core and OpenAI/Anthropic LLM API runtime handlers into `pkg`.
8. Move admin core into `pkg`.
9. Repository-wide import cleanup and Caddy compatibility verification.
10. Standalone server MVP.
11. Documentation and compatibility cleanup.

Provider 拆分建议先用 `openai` 做模板，验证 runtime factory 和 Caddy module 都能工作后，再机械迁移其他 provider。Standalone server 应等待所有这些拆分完成后再开始。

## Compatibility Requirements

以下外部行为必须保持兼容：

- Caddy app module ID：`agent_gateway`
- Dispatcher middleware module ID：`http.handlers.agent_route_dispatcher`
- Admin middleware module ID：`http.handlers.agent_gateway_admin`
- Provider module namespace：`llm.providers.<name>`
- LLM API handler namespace：`agent_route_dispatcher.llm_apis.<name>`
- Config store namespace：`agent_gateway.config_stores.sqlite`
- Caddyfile directives：
  - `agent_gateway`
  - `provider`
  - `provider_type`
  - `virtualkey`
  - `route`
  - `llm_api`
  - `require_virtual_key`
  - `agent_route_dispatcher`
  - `agent_gateway_admin`

Standalone 配置可以是新格式，不需要兼容 Caddyfile。

## Testing Strategy

每个阶段至少运行：

```bash
go test ./...
go build -o agw ./cmd/main.go
go build -o agwctl ./cmd/agwctl
```

新增 standalone 后运行：

```bash
go build -o agwd ./cmd/agwd
```

建议补充测试类型：

- `pkg/dispatcher` runtime tests，不依赖 Caddy。
- `pkg/gateway` bootstrap tests，使用 in-memory 或临时 SQLite config store 验证 `AgentGateway.Bootstrap(...)`。
- `standalone` assembly tests，验证 standalone config 能正确构造 runtime 依赖。
- Caddy adapter tests，验证 module ID、Caddyfile parsing、module loading。
- Standalone smoke test，验证请求链路：

```text
HTTP request
  -> standalone dispatch handler
  -> AgentGateway.ResolveRoute(...)
  -> VirtualKey validation
  -> protocol handler
  -> routed provider
```

## Migration Checklist

- [ ] `pkg` 核心包不再直接 import Caddy。
- [ ] Caddy adapters 保持原 module ID。
- [ ] Provider runtime factory 和 Caddy module registration 分离。
- [ ] LLM API handler runtime interface 和 Caddy module registration 分离。
- [ ] SQLite config store 有普通构造函数，不依赖 `caddy.Context`。
- [ ] `AgentGateway.Bootstrap(...)` 是 Caddy app 和 standalone server 共同调用的核心 runtime bootstrap。
- [ ] Standalone server 有最小可运行配置。
- [ ] README、DESIGN、AGENTS、Caddyfile examples 已更新。
- [ ] `go test ./...`、`go vet ./...`、所有 build targets 通过。
