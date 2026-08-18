# Dex CLI 实施计划

状态：Implemented
日期：2026-07-31

## 1. 目标

新增 Homebrew package 和命令 `dexcli`。实现放在仓库根目录 `cli/`。

用户只需：

```bash
brew install dexcli
dexcli dev
```

`dexcli dev` 默认启动并管理 Dex Server、Dex Web 和内部 workflow backend。就绪输出展示 Dex Web、Dex Server、Local DB、blob store 和 server log folder，不展示 Temporal endpoint。

用户也可以连接已有 Temporal：

```bash
dexcli dev --external-temporal-address localhost:7233
```

此时不启动、不关闭 local Temporal；只管理 Dex Server 和 Dex Web。

本地 Attribute Store 使用标准 Dex YAML 的 `attributeStore` section：

```bash
dexcli dev --attribute-store-config ./attribute-store.yaml
```

CLI 只从该文件读取 Attribute Store 配置；API、Temporal、Web 和 blob store
仍由 `dexcli dev` 的本地参数管理。

最终安装和运行不要求用户手动安装或启动 Node.js。Node.js 只用于构建 React/TypeScript 前端。

## 2. 非目标

- 不把 `go.temporal.io/server` 源码嵌入 Dex module。
- 不复制 Temporal CLI 的 `internal/devserver` 实现。
- 不在 `dexcli dev` 中支持 Cadence；现有 Dex Server 的 Cadence 支持保持不变。
- 不保留 Next.js API routes 作为兼容层。
- 不在第一阶段实现生产集群部署、进程守护或多节点 Dex。
- 不自动修改用户提供的 external Temporal cluster。

## 3. 总体架构

### 3.1 Local Temporal 模式

```text
                         dexcli
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
          ▼                 ▼                 ▼
 temporal server      Dex runtime       Go Web server
 start-dev             in-process         in-process
   :7233                  :8801              :8802
   :8233 UI                                  │
                                             ├── embedded SPA
                                             └── /api/* → Dex gRPC
```

Temporal Dev Server 是 `dexcli` 拥有的唯一 child process。Dex Server 和 Dex Web 在 `dexcli` 进程内运行。

### 3.2 External Temporal 模式

```text
External Temporal :7233
          ▲
          │
       dexcli
          ├── Dex runtime :8801
          └── Go Web server :8802
```

`dexcli` 只检查 external Temporal readiness，不管理其生命周期。Dex Server 启动时自动同步系统 Indexed Attributes。

### 3.3 Web 请求路径

```text
Browser
  │ HTTP/JSON
  ▼
web Go package
  │ Dex gRPC
  ▼
Dex FlowService
  │
  ▼
Temporal
```

Temporal/Cadence credentials、protobuf 解码和 backend history 不进入浏览器。

## 4. Repository 和 Go module 布局

```text
cli/
  go.mod
  cmd/dexcli/
    main.go
  internal/dev/
    command.go
    config.go
    supervisor.go
    temporal_process.go

web/
  go.mod
  server.go
  api/
    handlers.go
    mapping.go
    types.go
  assets/
    embed.go
    dist/                 # generated static frontend
  app/
    App.tsx
    main.tsx
    components/
    flows/
  index.html
  vite.config.ts
  package.json

server/
  service/bootstrap/
    bootstrap.go
```

Module ownership：

- `server/` 继续提供 Dex API/interpreter 实现。
- `web/` 成为独立 Go module，提供可复用的 HTTP server、Dex gRPC adapter 和 embedded assets。
- `cli/` 成为独立 Go module，import server bootstrap 和 web module，负责组合与生命周期。
- 根目录 `go.work` 增加 `./web` 和 `./cli`。
- `script/licenseheaders/mapping.yaml` 为 `cli` 和新增的 Web Go 文件指定 MIT header。

`cli/` 不复制 Web handlers；它只调用 `web.NewServer(...)`。

## 5. Web 重构

### 5.1 删除 Node API server

将以下实现迁移到 Go 后删除：

```text
web/app/api/flows/search/route.ts
web/app/api/flows/summary/route.ts
web/app/api/flows/history/route.ts
web/app/api/flows/state/route.ts
web/app/api/flows/wait/route.ts
web/app/api/flows/time-travel/route.ts
web/app/api/_grpc/
```

Go handlers 保持现有浏览器 HTTP contract，内部使用 generated Dex gRPC client：

```text
POST /api/flows/search
GET  /api/flows/summary
GET  /api/flows/history
GET  /api/flows/state
GET  /api/flows/wait
POST /api/flows/time-travel
GET  /healthz
```

迁移完成后只有 Go 版本，不增加兼容 shim 或双写测试。

### 5.2 静态 React SPA

当前页面主要是 client components。将 Next.js App Router 改为 Vite + React Router：

- `/` 显示 Flow Search；
- `/flows/:flowId` 解析 current run 并跳转 canonical URL；
- `/flows/:flowId/:runId` 显示 Run Details；
- 其他非 `/api/*` 路径由 Go server fallback 到 `index.html`；
- 保留现有 React components、styles、query、graph 和 type definitions；
- 删除 Next.js runtime、server routes 和 `next/*` imports。

前端 build 输出到 `web/assets/dist/`，由 Web Go package 使用 `go:embed` 编译进 `dexcli`。

Node.js 只存在于以下阶段：

```text
npm ci
npm run check
npm run build
go build ./cli/cmd/dexcli
```

运行 `dexcli` 时不启动 Node process，也不读取 `node_modules`。

### 5.3 Go Web server

`web.NewServer` 使用 constructor injection 接收：

- Web config pointer；
- Dex `FlowServiceClient`；
- logger；
- embedded asset filesystem。

Web config 至少包含：

```text
BindAddress  127.0.0.1
Port         8802
```

Server 提供：

- `Serve(net.Listener) error`，便于 readiness 和 integration test 使用动态端口；
- `Shutdown(context.Context) error`，停止 long poll 和 HTTP requests；
- SPA fallback，但绝不把 `/api/*` 的 404 fallback 成 HTML；
- same-origin API，因此默认不需要 CORS；
- gRPC status 到稳定 HTTP status/error JSON 的统一映射。

`WaitForHistoryEvent` handler 必须把 HTTP request cancellation 传入 gRPC context。

## 6. Dex server bootstrap 重构

当前 `server/cmd/server/dex/dex.go` 在 goroutine 中使用 `log.Fatal`，最后永久等待 `sync.WaitGroup`。它不能被 `dexcli` 安全管理。

新增 `server/service/bootstrap`：

```go
dexRuntime, err := bootstrap.New(config)
err = dexRuntime.Run(ctx)
```

Runtime 负责构造和持有：

- Temporal/Cadence client；
- unified client；
- worker client pool；
- blob store；
- API gRPC server；
- interpreter worker；
- logger 和 metrics dependencies。

要求：

- `New` 只使用 constructor injection，并接收完整 config section pointer；
- 启动错误向调用方返回，不调用 `log.Fatal`；
- 任一内部 component 非预期退出时，`Run` 返回错误；
- context cancellation 时停止接收请求；
- shutdown 顺序为 API `GracefulStop`、interpreter `Close`、clients/pools `Close`；
- shutdown 有总 timeout，超时才强制停止；
- 现有 `server/cmd/server` 也改用同一个 runtime，避免两套 bootstrap。

Local `dexcli dev` 构造 typed `config.Config`，不生成临时 YAML：

- Temporal address 为 local 或 external address；
- namespace 默认 `default`；
- API port 默认 `8801`；
- external storage 默认使用 local filesystem backend，threshold 为 1 KB；
- internal service target 指向本次 Dex API listener。

`--blob-store-dir` 指定持久目录；未指定时使用数据库文件同目录下的
`dex.blobs`。local mode 未指定数据库时使用
`$HOME/.dex/dev/<port>/dex.sqlite.db` 与
`$HOME/.dex/dev/<port>/dex.blobs`。退出 `dexcli dev` 不会删除这些目录。显式
`--blob-store-dir` 优先于相邻的 `dex.blobs`。

高级 server 配置继续由现有 server binary/YAML 提供，不把所有 production config 暴露成 `dexcli dev` flags。

## 7. `dexcli dev` CLI contract

```text
dexcli dev [flags]

--bind-address string          default 127.0.0.1
--dex-port int                 default 8801
--web-port int                 default 8802
--blob-store-dir string        persistent Dex blob storage directory (default $HOME/.dex/blobs)
--open                         open Dex Web after readiness
--sqlite-db-filename string    default $HOME/.dex/dev/<port>/dex.sqlite.db
--server-log-folder string     keep server logs (default temp folder, deleted on exit)
--external-temporal-address string      non-empty selects external Temporal
--external-temporal-namespace string    default default; external mode only
```

`--external-temporal-address` 接受 `host:port`，不是 HTTP URL。设置后忽略 local SQLite flag，并为该无效组合返回 `InvalidArgument` 风格 CLI error。Local Temporal gRPC 与 Web 端口始终自动分配。

第一阶段 external Temporal 支持 plaintext local/self-hosted endpoint。Temporal Cloud TLS/API key flags 后续单独设计，避免把 secret 放进 process arguments。

## 8. Temporal process 管理

Homebrew Formula 声明 runtime dependency：

```ruby
depends_on "temporal"
```

用户仍只执行 `brew install dexcli`；Homebrew 自动安装 Temporal CLI。

Local mode 使用 `exec.LookPath("temporal")` 找到 dependency，并启动：

```bash
temporal server start-dev \
  --ip 127.0.0.1 \
  --port 7233 \
  --ui-ip 127.0.0.1 \
  --ui-port 8233 \
  --db-filename "$HOME/.dex/dev/7233/dex.sqlite.db"
```

默认 namespace 已由 Temporal Dev Server 创建。Local mode 始终使用 `default`；只有 `--external-temporal-namespace` 才会覆盖 namespace。

Process manager 必须：

- 为每个 `dexcli dev` 进程分配互不冲突的 Dex、Dex Web、Temporal 和 Temporal UI 端口；未占用时使用默认值，Dex/Web 已被占用且未显式指定时选择下一个空闲端口，Local Temporal 端口始终自动分配；
- 为每个 local Temporal 使用独立 SQLite 文件（默认 `$HOME/.dex/dev/<port>/dex.sqlite.db`）；
- `--server-log-folder` 将 Dex Server 与 local workflow engine 日志写入同一目录；默认使用系统临时目录，就绪输出只打印 `Server log folder`，正常退出时删除；失败时保留并在错误中给出路径；
- 启动前预占 Dex 与 Dex Web listeners，并确认 Temporal 端口可绑定；
- 丢弃 backend 子进程 stdout/stderr，避免向普通启动输出泄露内部 endpoint；
- 通过 Temporal API readiness 检查，不根据日志文本判断 ready；
- Dex Server 启动时注册并验证系统 Indexed Attributes；
- child process 非预期退出时取消整个 dev stack；
- shutdown 时先发送 `SIGTERM`，超时后才 kill；
- external mode 不查找、不启动、不 signal Temporal binary；
- external mode 仍由 Dex Server 自动同步 Indexed Attributes；权限不足时 Dex 启动失败。

第一版接受 external-mode 用户也会通过 Homebrew 安装 Temporal CLI 的体积成本。拆分 Formula 或私有 runtime packaging 不在本计划范围。

## 9. Supervisor 和生命周期

根 context 监听 `SIGINT`、`SIGTERM` 和 `SIGHUP`。

启动顺序：

```text
1. 解析并验证 flags
2. 为 owned ports 分配空闲端口，预占 Dex 与 Dex Web listeners，并分配独立 Temporal SQLite 文件
3. 启动 local Temporal，或检查 external Temporal
4. 等待 Temporal ready
5. 启动 Dex runtime
6. 等待 Dex gRPC health SERVING
7. 启动 embedded Dex Web
8. 等待 /healthz
9. 只打印 Dex URL；可选打开 Dex Web
```

停止顺序：

```text
1. Dex Web Shutdown
2. Dex API GracefulStop 和 interpreter Close
3. local Temporal SIGTERM
4. 等待退出；超时后强制终止 owned child
```

如果任一 owned component 在 root context 取消前退出：

- 记录 component name 和原始 error；
- 取消其他 components；
- 完成 cleanup；
- `dexcli` 返回非零 exit code。

避免使用 shell command 和 `npm` wrapper 启动 child process，确保 signal 直接送达 Temporal process。

## 10. Startup output 和错误体验

所有服务 ready 后一次性打印：

```text
Dex development environment is ready

Dex Web:       http://127.0.0.1:8802
Dex Server:    127.0.0.1:8801
Local DB:      $HOME/.dex/dev/7233/dex.sqlite.db
Blob store:    $HOME/.dex/dev/7233/dex.blobs
Server log folder: /tmp/dexcli-logs-123

Press Ctrl+C to stop.
```

Local mode 打印 SQLite、blob 与 server log folder，不展示 Temporal endpoint。External Temporal mode 省略 Local DB 行，仍打印 blob store 和 server log folder，且不展示 backend endpoint。

错误必须指出 component 和修复方法，例如：

```text
cannot start Dex Web: 127.0.0.1:8802 is already in use
external Temporal is missing search attribute FlowType (Keyword)
Temporal CLI was not found; reinstall dexcli with Homebrew
```

显式 `--*-port` 被占用时返回 already in use。未指定的端口自动改绑到下一个空闲端口。

## 11. 实施阶段

### Phase 1：可复用 runtime 和 module 骨架

1. 创建 `cli/go.mod`、`web/go.mod`，更新 `go.work` 和 license mapping。
2. 从旧 server CLI 抽出 `server/service/bootstrap`。
3. 让旧 `server/cmd/server` 使用新 runtime，并支持 signal-driven shutdown。
4. 保持现有 Temporal/Cadence server integration suites 全部通过。

### Phase 2：Go Web server

1. 在 `web/` 实现 Go HTTP server 和 Dex gRPC adapter。
2. 按现有 TypeScript contract 迁移 search、summary、history、state、wait、time travel。
3. 用真实 Dex FlowService integration tests 验证 handlers。
4. 删除 Next API routes 和 Node gRPC dependencies。

### Phase 3：静态 SPA 和 embedded assets

1. 将 Next route shell 迁移到 Vite + React Router。
2. 保留现有 UI components 和行为。
3. 生成静态 assets 并嵌入 Web Go package。
4. 验证直接打开和刷新动态 Flow URL 都返回 SPA。

### Phase 4：`dexcli dev` supervisor

1. 实现 flags、typed local config 和 port validation。
2. 实现 local Temporal child lifecycle 和 readiness。
3. 实现 external Temporal 模式。
4. 组合 Dex runtime 和 Web server。
5. 实现 signals、failure propagation、graceful shutdown 和 startup output。

### Phase 5：Homebrew 和发布

1. 新增 `dexcli` release build，产出 macOS arm64/amd64 和 Linux arm64/amd64 binaries。
2. Release build 先生成 Web assets，再构建 Go binary。
3. Homebrew Formula 使用名称 `dexcli`，依赖 `temporal`；Node 仅为 source build dependency。
4. 添加 checksum、version injection、`dexcli version` 和 Formula smoke test。
5. 发布前在干净机器验证 `brew install dexcli && dexcli dev`。

## 12. Tests

以 integration/E2E 为主，不为可通过完整调用路径覆盖的逻辑新增 Go unit tests。

### Server integration

- 新 runtime 在 Temporal 和 Cadence 下运行现有 server suites，证明 bootstrap 重构没有改变执行语义。
- context cancellation 后 API health 先变为 NOT_SERVING，然后 interpreter、clients 和 worker pool 全部关闭。
- component startup failure 返回原始错误，不触发 `log.Fatal` 或遗留 goroutine。

### Web API integration

- 对真实 Temporal Dex flow 验证 search、summary、history pagination、state、wait cancellation 和 time travel。
- 对 Cadence 重复同一组 Web API integration scenarios，保证 Go rewrite 不降低现有 backend 覆盖。
- 验证 gRPC `InvalidArgument`、`NotFound`、`FailedPrecondition`、`DeadlineExceeded` 到 HTTP response 的稳定转换。
- 验证 `/api/*` unknown route 返回 JSON 404，而不是 SPA HTML。

### `dexcli` integration

- 默认 local mode 启动四个 endpoints，并能通过 Dex Web API 搜索一个实际 flow。
- local Temporal 自动包含 `default` namespace 和 `FlowType=Keyword`。
- `--sqlite-db-filename` 重启后保留 Temporal executions。
- local blob backend 使用安全路径编码和 atomic rename。
- 默认 `$HOME/.dex/blobs`、`--blob-store-dir` 或相邻的 `dex.blobs` 重启后保留 step inputs 和大 Value。
- external mode 连接预先启动的 Temporal，不创建第二个 Temporal process。
- 退出 external mode 后 external Temporal 仍然可用。
- occupied 8801、8802 在显式指定且已被占用时返回明确错误；Local Temporal 端口被占用时自动选择下一个空闲端口。
- Temporal child 非预期退出会停止 Dex runtime 和 Web server，并返回非零状态。
- SIGINT/SIGTERM 后所有 owned ports 可立即被下一次启动复用。
- 在 PATH 中没有 `node`/`npm` 时，release binary 仍可提供完整 Dex Web。

### Browser E2E

- 从 `http://127.0.0.1:8802` 搜索并进入 Flow Details。
- 直接加载和刷新 `/flows/:flowId/:runId` 不产生 404。
- Basic/Advanced query、pagination、long poll、Step Graph、Timeline 和 Time Travel 保持现有行为。
- Dex Server 暂时不可用时展示可操作错误，恢复后能够重新加载。

### Packaging

- 在 Homebrew bottle 支持的每个平台运行 `dexcli version` 和 embedded Web smoke test。
- 在干净 macOS 环境只执行 `brew install dexcli`，不预装 Node 或 Temporal。
- Formula 自动安装 Temporal dependency，随后 `dexcli dev` readiness 全部通过。

## 13. Documentation

- 新增 `cli/README.md`：安装、`dexcli dev`、全部 flags、local/external Temporal 和 troubleshooting。
- 更新根 `README.md`：将 `brew install dexcli` 作为最短本地启动路径。
- 重写 `web/README.md`：Go Web server、Vite build、embedded assets 和 contributor development flow。
- 更新 `server/CONTRIBUTING.md`：server bootstrap、`dexcli` integration tests 和 Temporal prerequisite。
- 更新 `server/lite/README.md` 或相应文档：说明 Docker lite 与 `dexcli dev` 的定位；确认替代后再删除旧脚本。
- 更新 `docs/README.md` 链接本文。

## 14. UI/UX

- Dex Web 的稳定默认入口是 `http://127.0.0.1:8802`。
- `dexcli` 只在全部 services ready 后打印成功 banner。
- `--open` 只打开 Dex Web，不自动打开 Temporal Web。
- External Temporal 未提供 UI 地址时不显示猜测链接。
- Browser API errors 使用当前页面内错误状态，不显示 Go/gRPC internal stack。
- Local SYNC/ASYNC history、Step Graph、Timeline 和 Time Travel 的 UI 功能不得因静态 SPA 迁移减少。

## 15. 完成标准

满足以下条件后本计划完成：

1. `brew install dexcli` 是用户唯一的安装命令。
2. `dexcli dev` 默认提供 Temporal、Temporal Web、Dex Server 和 Dex Web。
3. `dexcli dev --external-temporal-address localhost:7233` 不启动或停止 local Temporal。
4. Dex Web 默认运行在 `http://127.0.0.1:8802`。
5. Release runtime 不依赖 Node.js，不存在 Next.js API server。
6. `cli` import `web` Go module，不复制 Web API implementation。
7. 所有 server integration、Web API integration、browser E2E 和 packaging tests 通过。
