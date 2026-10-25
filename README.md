# CodeArts2API

> 华为云 CodeArts Agent（盘古助手/码道）的 OpenAI 兼容代理。**无需运行 CodeArts Agent
> 客户端**，纯 Go 直连华为云 API，多账号轮转 + token 自动续期 + WebUI。

**语言 / Language / 語言：** [简体中文](README.md) · [繁體中文](README.zh-TW.md) · [English](README.en.md)

## 参考项目

本项目是 [Sliverkiss](https://github.com/Sliverkiss) 同系列开源项目的延伸实现，架构与运维形态参考了以下仓库：

- [workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) — WorkBuddy CN OpenAI 兼容反代（账号池 / 轮转 / 签到架构）
- [traework2api](https://github.com/Sliverkiss/traework2api) — TRAE Work OpenAI 兼容反代（零依赖 Go 骨架）
- [qoderwork2api](https://github.com/Sliverkiss/qoderwork2api) — QoderWork CN OpenAI 兼容反代（OAuth 授权流程）

感谢原作者的开源与优秀设计。

## 快速开始（Ubuntu / Linux）

```bash
make linux            # bin/ 下 4 个 Linux 静态二进制
make test
```

### 登录（华为云账号）

```bash
# 本机有浏览器
./login.sh

# 服务器（无浏览器）：打印链接，任意机器浏览器打开，ticket 轮询下发
./login.sh -print-only

# 凭证落盘 auths/codearts-{user_id}.json
```

### 启动

```bash
cp config.example.json config.json
export CA2A_API_KEY=你的随机密钥
./bin/codearts2api -config config.json
```

### 验证 + WebUI

```bash
curl http://127.0.0.1:7866/healthz
curl http://127.0.0.1:7866/v1/models -H "Authorization: Bearer $CA2A_API_KEY"
curl -X POST http://127.0.0.1:7866/v1/chat/completions \
  -H "Authorization: Bearer $CA2A_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"snap-chat","messages":[{"role":"user","content":"你好"}]}'
```

浏览器打开 **http://127.0.0.1:7866/panel/** 即管理控制台：账号授权与状态、模型目录、请求审计和连接日志。多轮上下文按账号自动续接（chat_id 分组）；也可用请求头
`X-Codearts-Chat-Id: <chatId>` 或 body 里 `conversation_id` 显式指定会话。

流式请求设置 `"stream_options":{"include_usage":true}` 后，会在 `[DONE]` 前返回一次
`choices: []` 的 usage chunk；其余 chunk 的 `usage` 为 `null`。未启用时不返回 usage。
流式和非流式均优先使用上游 usage；上游未提供时按文本长度估算，估算值不是精确 token 计数。

后台“请求审计”保存成功完整请求的输入、输出、总 token，并区分“上游返回”和“估算”；流式客户端未开启 `include_usage` 也会记录后台用量。按账号、模型汇总的是**当前筛选条件下的近期审计窗口**，不受加载分页影响，也不代表全部历史或上游账单。旧记录、失败请求及上游未提供的分项显示未知；保留窗口之外的磁盘归档不会被扫描计入，重启后会从已保留日志恢复窗口。审计关闭或丢失记录时统计可能不完整。账号页“凭证剩余时间”表示登录凭证何时过期，与模型 token 用量无关。

工具调用支持 `tool_choice` 的 `auto` / `none` / `required` 及指定函数对象。
指定函数时，网关只向上游发送该函数的定义，并转换为上游支持的字符串 `required`；
指定函数不存在或重复定义时返回 400。上游 `InferHub.001001005` 参数错误也返回 400，
不轮换、不冷却账号；若流式响应已开始，则用 SSE 错误事件结束，同样不处罚账号。

### 多用户并发与超时（New API 接入）

请求会优先使用有空闲槽位且模型能力匹配的健康账号；同负载账号轮转分配，已有会话
保留有效账号亲和。挑号只读已缓存的模型目录，未知目录允许尝试，不在聊天请求中
同步发现模型。单账号默认 `max_concurrent: 1`，可按账号实际容量调整。

等待槽位采用有界队列，默认全进程 `max_pending: 64`。整个请求生命周期（含读取
请求体、等待、重试间隔、生成和响应写入）的在途数也限制为**启用账号的并发容量之和
+ max_pending**，防止上游快速拒绝后累积大量重试请求。共享同一账号池的 handler
共用限制；多进程/多副本各自计数，部署多副本时需按共享账号的实际额度分配容量。
队列满或累计排队超过默认 10 秒，返回 429 与 `Retry-After`，网关应退避重试。
取消请求会退出排队、关闭上游并释放槽位，不因客户端超时而冷却健康账号。

默认时间预算如下，均可通过配置或环境变量覆盖：

| 阶段 | 配置 | 默认 |
|------|------|------|
| 读请求体至上游首个数据（含排队、刷新、重试、换号） | `startup_timeout_seconds` | 90 秒 |
| 所有账号槽位等待累计 | `account_wait_seconds` | 10 秒 |
| 上游响应头、首数据及后续读取空闲 | `upstream.timeout_seconds` | 60 秒 |
| 下游单次写入或 flush | `stream_write_timeout_seconds` | 30 秒 |
| 上游并发/TPM 重试 | `queue_max_attempts` / `queue_retry_seconds` | 每账号最多 3 次（含首次），间隔 2 秒 |

收到首个上游数据后停止启动计时，持续输出的长回复可以超过 90 秒；上游持续静默
达到空闲超时才终止。响应头尚未发送时超时返回 504；已开始的流在可写时发送错误
与 `[DONE]`。收到协议结束标记立即关闭上游连接，不继续等 TCP 断开。New API 和
反向代理的超时应大于所配置的启动/空闲预算，并允许预期的长回复时长。

升级时已有配置中的 `max_concurrent: 5`、`upstream.timeout_seconds: 120` 等显式值
会保留。`upstream.timeout_seconds` 现在实际用于聊天客户端，初始账号与运行时
新增/重载账号都使用它；旧版该项对聊天未生效。需要新默认值时请同步修改自己的
`config.json`，不要只更新示例。日志记录排队、首数据、总耗时、尝试次数和结果，
便于与 New API 请求时间对应，不记录提示词或凭证。

### 模型列表与限时福利

`/v1/models` 返回上游**精确模型 ID**（区分大小写，如 `GLM-5.2`、`Qwen3-VL-235B`），
同时为含大写的 ID 补一条小写别名（`glm-5.2`），两者都能用于聊天。限时福利
（免费套餐）模型额外带 `benefit: true` 标记，聊天时服务端会自动追加上游要求的
`maas_type: benefit` 请求头（按发起请求的账号判定，多账号套餐不同也不会串——
列表是各账号可用模型的并集，实际路由会优先挑目录里真有这个模型的账号）。

New API 可以统一使用小写模型名，例如 `glm-5.3`。每次请求会按选中账号及当前凭证的目录，映射成该路由实际返回的名称；其他账号的大小写不会覆盖这次请求，福利来源和普通来源名称不同时也会使用对应来源的原始 ID。没有目录证据的新名称不会被猜成其他模型。审计保留客户端原名，“规范名”记录最后一次真正发往上游的名称；名称映射成功仍不代表上游已授予模型权限。

福利模型列表随免费套餐轮换，用下面这条命令核对当前账号实际可用的模型：

```bash
go run ./cmd/models                 # 账号可用模型（内置 + 福利）
go run ./cmd/models -json           # 机器可读
go run ./cmd/models -claim          # 先领取限时福利再查询（幂等，属写操作）
```

限时福利模型**必须先领取才能调用**：未领取时上游一律返回
`InferHub.4004.200 benefit not found`（实测）。领取是幂等操作，官方客户端打开模型
菜单时也会调用，因此服务默认会领取（`benefit_auto_claim: true`）。不想让服务写账号
可显式关闭：

```jsonc
"benefit_auto_claim": false   // 或 CA2A_BENEFIT_AUTO_CLAIM=0
```

关闭后不会主动领取福利；已领取的权益仍以上游账号状态为准，未领取时上游可能拒绝调用。

后台“模型目录 → 重新拉取”会真正重新请求各账号的 Agent、内置和福利来源，显示来源状态、尝试/成功时间、保留的旧目录及账号模型排除。多次点击共享同一个有界任务，任务启动至少间隔 15 秒，单次最多 60 秒、并行处理 2 个账号；失败来源保留上次成功数据并标为旧数据。`GET /admin/api/models` 只读缓存，`POST /admin/api/models/refresh` 返回 202 与任务状态。

`model_not_available` 表示当前符合调度条件的账号均未提供该模型或存在有效的能力拒绝缓存。先在目录查看精确模型 ID、来源失败与排除原因。成功拉取对应的有效来源后，会重置这次拉取前的相关旧排除，允许下次真实调用重新判断；不会把目录收录当成权限验证，也不会清除并发请求刚写入的新拒绝。官网能看到模型不等于每个已配置账号都有同样权益。完整且新鲜的账号目录确认没有请求模型时，聊天返回 404 `model_not_available`，公开 `/v1/models` 也会过滤这类无账号支持的预置项。`glm-5.3-flash` 与 `glm-5.3` 是不同模型，不会相互替换。503 `no_healthy_account` 则表示当前没有健康可调度账号，例如账号全部禁用、冷却或凭证不可用；并发排队满/等待超时返回 429，启动或上游空闲超时返回 504。审计里的 `attempts: 0` 表示尚未向上游发送推理请求。

`TM.00001041` / `concurrency_limit` 表示上游并发会话拒绝。若还有合格的未尝试账号且轮换预算允许，会优先换号；没有后备时沿用有限重试。默认三个账号全部返回该错误时最多形成 A/B/CCC 五次尝试，单账号仍最多三次。HTTP 400、HTTP 429 或 SSE 包装的明确并发拒绝都不会误冷却整个账号；普通限速和 TPM 仍保留原有策略。`attempts` 是实际出站次数，需结合 `tried` 和连接轨迹判断是否换过账号。

### 模型可用性探测

`/v1/models` 返回发现的目录并过滤已知不可用项；未被拒绝不代表已验证可调用。上游模型发现（agent-center / builtin / 福利
网关）可能列出当前账号尚未注册的模型（如返回 `InferHub.002002009.404`）。服务会周期性用一条最小请求探测并缓存结论，
真实请求失败也会被学习（`not registered` / `benefit not found` 记为账号能力问题，
不会给健康账号记错误冷却）。探测结果缓存 30 分钟。

### API Key 必填

服务没有 API Key 会**拒绝启动**（旧版本会回退到公开的 `dummy-key-for-codearts`，
等于把接口暴露给任何人）。三选一：

```bash
# 1) env（推荐，配合 .env / systemd EnvironmentFile）
export CA2A_API_KEY=$(openssl rand -hex 24)
# 2) config.json 的 "api_key" 字段
# 3) docker compose 的 .env
```

### 账号续期

`refresh_token` 与登录时的 `client_id`、DPoP 私钥绑定，因此凭证文件会保存
`client_id` 与 `dpop_private_key`；刷新时原样复用并写回轮转后的新 refresh_token。
若手工换过 `login_client_id`，老账号仍按自己记录的 client_id 刷新。

### 每日签到与新手积分

服务默认在启动和每次 watchdog 轮询时检查活动，自动领取每日签到积分及
上游返回的可领取新用户积分。新增账号会在下一次轮询处理；也可在 WebUI
点击「签到」/「全员签到」，或鉴权调用 `POST /admin/api/checkin`（可传 `{"uid":"账号 ID"}`）。
领取后必须确认；已领取未确认时只补确认，已确认不重复领取。资格、金额、
每日刷新和活动起止时间均以上游返回为准，失败会在下一次轮询重试，
不会因活动接口故障冷却或禁用推理账号。

`credits_auto_claim: false` 或 `CA2A_CREDITS_AUTO_CLAIM=0` 可关闭自动领取，
`watch.enabled: false` 会关闭整个轮询。手动签到仍可使用。
这项活动与 `benefit_auto_claim` 的模型 Token 福利独立；面板显示最近领取结果，
不把领取量或健康账号数当作积分余额。`credit.sh` 查登录态，`apply.sh` 仅续期。

当前[官方每日签到活动](https://support.huaweicloud.com/offers-codeartsagent/codeartsagent_offers_0004.html)
为已升级积分计费的用户每日发放 1000 积分，30 天有效，活动截止于
2026-12-31 00:00（UTC+8）；[新用户注册奖励](https://support.huaweicloud.com/price-codeartsagent/codeartsagent_billing_0029.html)
为 4000 积分。无资格或已自动发放的新手奖励不会再次领取。

## 部署与升级

部署方式有两种：**默认使用已发布镜像**（无需本地构建），或在源码目录**本地构建**。
两种方式共用同一份 `docker-compose.yml` 变量与挂载；本地构建用叠加文件
`docker-compose.build.yml` 覆盖镜像来源。

要求：Docker Engine 24+ 与 Docker Compose v2（`docker compose`）。

### 方式一：默认已发布镜像（推荐）

```bash
# 仅首次部署复制示例；升级时不要覆盖已有文件
cp -n .env.example .env
cp -n config.example.json config.json
mkdir -p auths data

# 编辑 .env：至少设置 CA2A_API_KEY；如需更换端口设置 CA2A_PORT（默认 7866）
docker compose config --quiet   # 只做本地语法/插值检查
docker compose pull
docker compose up -d            # 标准部署：应用 + 自动更新
```

标准部署命令会同时启动应用容器与 Watchtower 更新器（默认启用）。只想手动部署、不要
自动更新时用 `docker compose up -d codearts2api`：只启动应用服务，不启动更新器。

默认镜像为 `ghcr.io/azurekiln2333/codearts2api:latest`，与仓库实际 origin
（`github.com/azurekiln2333/codearts2api`）一致。可在 `.env` 用 `CA2A_IMAGE`
切换到其他镜像或固定版本（见下文“镜像切换与固定版本”）。

> **首次发布的边界**：`ghcr.io/azurekiln2333/codearts2api:latest` 只有在推送第一个
> `v*` 版本标签、CI 成功发布后才会存在。在此之前 `docker compose pull` 会因
> manifest 不存在而失败，这是预期行为，并非本机配置错误；此时请改用方式二本地构建。
> GHCR 包**首次推送默认是 private**：要么在 GitHub 包设置里改为 public，要么先执行
> `docker login ghcr.io`（凭据需含 `read:packages`）再拉取。

升级：保留现有 `.env`、`config.json`、`auths/` 与 `data/`，然后

```bash
docker compose pull
docker compose up -d
```

### 方式二：本地源码构建

```bash
# 叠加本地构建文件：使用本地镜像、pull_policy: build，并停用自动更新目标
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

叠加文件把应用镜像固定为本地 `codearts2api:local`（可用 `CA2A_LOCAL_IMAGE` 覆盖）且不删除已发布镜像变量；Watchtower
不会把本地构建容器替换成已发布镜像。要标记构建身份（后台“运行配置”会显示实际提交）：

```bash
CA2A_BUILD_COMMIT="$(git rev-parse HEAD)" \
CA2A_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

`CA2A_BUILD_CONTEXT` 可覆盖构建上下文。源码更新后必须重新 `up -d --build`；重启旧容器
不会替换旧镜像。叠加构建 overlay 时更新器默认不启动（保留 `auto-update` profile）；
即使显式加 `--profile auto-update`，应用容器的 `...enable` 标签为 `false`，也不会被自动替换。

### 自动更新（Watchtower，标准部署启用，可显式关闭）

标准部署命令 `docker compose up -d` 会同时启动一个**项目内隔离**的 Watchtower，自动检查
并更新本项目的 `codearts2api` 容器。Compose 默认启动更新器；要 opt-out 时只启动应用
服务：

```bash
# 标准部署：应用 + 自动更新
docker compose up -d

# 不要自动更新：只启动应用（更新器不会运行）
docker compose up -d codearts2api
```

Watchtower 行为：

- 镜像固定为维护分支 `nickfedor/watchtower:1.23.2`（原 `containrrr/watchtower`
  已归档不再维护；Docker Hub 命名空间为 `nickfedor`，GitHub 仓库为
  `nicholas-fedor/watchtower`）。
- **作用域限定**：同时启用 `--label-enable` 与 scope。只有带
  `com.centurylinklabs.watchtower.enable: "true"`（本项目的 `codearts2api` 服务）
  且 `com.centurylinklabs.watchtower.scope` 等于本项目名的容器才会被更新。
  scope 默认取 `COMPOSE_PROJECT_NAME`，回退 `codearts2api`；因此不会触碰其他项目、
  其他栈或无关容器。
- Watchtower 自身标签 `...enable: "false"`，永不自我更新。
- 需要挂载 `/var/run/docker.sock`（Docker 控制通道）；这是自动更新容器镜像的
  必要权限，不会改写 `.env`、`config.json` 或凭证。
- 可配置项（写入 `.env`）：
  - `WATCHTOWER_POLL_INTERVAL`：检查间隔（秒），默认 `600`。
  - `CA2A_WATCHTOWER_CONTAINER_NAME`：更新器容器名，默认 `codearts2api-watchtower`。
  - 更新后清理旧镜像（`WATCHTOWER_CLEANUP: "true"`）在 YAML 中固定开启，未暴露为 `.env` 变量。
- 保留的 `.env`、`config.json`、`auths/`、`data/` 挂载在更新重建容器后不变。

**关闭已启用的自动更新：**

```bash
# 只停用/移除更新器，应用容器不受影响
docker compose stop watchtower
docker compose rm -f watchtower
```

注意：`docker compose up -d codearts2api` 只启动应用，**不会停掉**已在运行的更新器
（Compose 不会清理未列出的服务），要停止请用上面两条命令。更新器只能拉取**已发布**的
镜像；首个 `v*` 标签发布前没有可拉取的镜像，请用本地构建（方式二）。本地构建方式
（方式二）不启动更新器，也不会更新本地构建的容器。

#### 私有仓库镜像的更新器鉴权

宿主机上的 `docker login` 只写入宿主机自己的 `~/.docker/config.json`，**不会自动传给
Watchtower 容器**。更新器默认只挂载 Docker socket，没有 registry 凭据：公开镜像（例如
已设为 public 的 GHCR 包）不需要凭据；私有 GHCR/CNB 镜像若要让自动更新真正可用，需给
更新器提供**只读**的 Docker config。

新建可选的 `docker-compose.override.yml`（使用默认文件链时 Compose 会自动合并；用 `-f`
显式列出多个文件时，需再追加 `-f docker-compose.override.yml`）：

```yaml
services:
  watchtower:
    environment:
      DOCKER_CONFIG: /config
    volumes:
      - ${HOME}/.docker/config.json:/config/config.json:ro
```

仓库**默认不挂载**该文件，因此公开镜像的部署不会因缺少本地私密文件而失败；这是可选的
opt-in。不要把 token 写进 YAML 或日志，登录一律用 `--password-stdin`。更新器重建容器时
沿用原挂载（`.env`、`config.json`、`auths/`、`data/`），不需要重新导入凭证。

若 `~/.docker/config.json` 使用 `credsStore`/`credHelpers`（例如 Docker Desktop），更新器
容器内没有对应 helper，鉴权会失败；此时改用手工 `auths` 写法的 `config.json`
（官方文档给出 `auth` 的 base64 生成方式），或把 helper 一并挂载。

### 镜像切换与固定版本（GHCR / CNB / 镜像源）

`CA2A_IMAGE` 决定拉取的镜像，默认 GHCR。写入 `.env` 后 `docker compose pull && up -d`
生效：

```bash
# 固定到某个版本（推荐生产回滚使用）
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.3

# 切换到 CNB 制品库；slug 是动态变量，请填你的实际仓库路径（小写）
CA2A_IMAGE=docker.cnb.cool/<CNB_REPO_SLUG_LOWERCASE>:latest
```

**架构中间标签**：CNB 流水线做多架构组装时会先构建并推送两个**按架构的中间标签**
`vX.Y.Z-amd64`、`vX.Y.Z-arm64`，再把它们合成多架构的 `vX.Y.Z`（stable 额外加
`latest`）。这些 `-amd64` / `-arm64` 标签由 CNB 流水线生成，保留用于调试与单架构
排查；**常规部署不要用它们**，继续用多架构的 `vX.Y.Z` 或 `latest`，Compose 与
Watchtower 会自动取匹配宿主架构的镜像。

CNB 制品库登录（`CNB_TOKEN` 由 CNB 流水线签发，本地可用访问令牌代替；用标准输入传密码，
避免把令牌写进命令行历史或日志）：

```bash
printf '%s' "$CNB_TOKEN" | docker login docker.cnb.cool -u cnb --password-stdin
```

本仓库未声明固定 CNB slug；文档中的 `<CNB_REPO_SLUG_LOWERCASE>` 是占位符，
请勿把它当成已存在的公开仓库。

### 版本标签、自动发布与权限（v*）

推送一个 `v*` 版本标签（如 `v1.2.3`）会触发受信任的发布流程：

1. **测试门禁**：先跑 Go `test`/`race`/`vet`/`build` 与 Python 发布工具测试；
   测试不通过不发布。
2. **发布镜像**：发布 `ghcr.io/azurekiln2333/codearts2api:v1.2.3`；稳定版
   （无 `-` 预发布后缀）**同时**更新 `latest`。
3. **源码 Release**：创建对应 GitHub Release，附显式清单导出的源码 ZIP 与
   sha256 校验文件（`scripts/release-source.py`）。
4. **预发布**：`v1.2.3-rc.1` 之类只标记为 prerelease，**不会覆盖 `latest`**。
5. **Pull Request** 只做校验，绝不认证或发布；工作流使用最小权限
   （`contents: read`，发布时才 `packages: write` / `contents: write`）。

CNB 侧使用同样的规则：`main`/`pull_request` 只校验，`v*` 标签才发布源码 Release
与镜像。CI 需要平台签发的临时令牌（`CNB_TOKEN`），不写入任何真实凭据。

> 以上描述的是**仓库内工作流的行为**；在你推送标签并让远程流水线运行之前，
> 镜像与 Release 都不存在。本文档不声称任何远程流程已经运行。

CNB 默认使用平台仓库路径与登录，无需覆盖注册表变量。自定义发布目标请在 **CNB 仓库 CI 变量**中设置 `CNB_IMAGE`（不含标签的镜像仓库路径）、`CNB_REGISTRY`、`CNB_REGISTRY_USER`、`CNB_REGISTRY_PASSWORD`；外部注册表必须提供密码。优先使用 `CNB_IMAGE`，避免与服务器 `.env` 中带标签或摘要的部署变量 `CA2A_IMAGE` 混淆。发布标签仅支持 `v主版本.次版本.补丁版本[-预发布]`，不支持 `+build` 元数据；长度还须容纳架构后缀。

### 回滚

镜像更新可独立回退，数据与凭证格式是增量的，无需迁移：

```bash
# 记录当前运行的镜像 ID 以便精确回退
docker inspect --format '{{.Image}}' codearts2api

# 回退到固定版本或摘要
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose pull
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose up -d
# 或按摘要：CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api@sha256:<digest>
```

回退时**保留同一组挂载目录**（`.env`、`config.json`、`auths/`、`data/`），不要切到空目录。
如需停止自动更新以免回退后又被升级，先按上文停用 Watchtower 再回退。

### 保留的挂载与凭证（升级安全）

Compose 通过 `env_file: .env` 直接读取应用变量；端口只填 `CA2A_PORT`（默认 `7866`），
它同时控制宿主机端口、容器端口与服务监听，Compose 自动生成 `CA2A_LISTEN`。
宿主机挂载路径使用 `CA2A_AUTHS_PATH`、`CA2A_DATA_PATH`、`CA2A_CONFIG_PATH`；`CA2A_AUTH_DIR`
与 `CA2A_STATE_FILE` 则是容器内应用路径。升级时保持原挂载路径即可继续使用已添加的凭证。

YAML 只保留 API Key 必填校验、时区默认值和派生监听地址这三个 `environment` 例外，
其余应用项直接写入 `.env`；非空值覆盖 `config.json`，未设置或留空则沿用配置文件及内置默认值。
可选应用变量仅在 shell 中 `export` 不会自动透传到容器。`.env` 文件必须存在；CLI 的
`--env-file` 只更换 Compose 插值来源，不会更换 YAML 中固定的 `env_file: .env`。

升级前备份现有 `.env`、`config.json`、`auths/` 和 `data/`，记录正在使用的镜像 ID；
保留原目录和挂载路径。升级只重建容器，不要求重新导入凭证。若此前在 `config.json`
显式配置过并发 `5`，该值仍然生效；初次验证上游额度时可在 `.env` 明确设置
`CA2A_MAX_CONCURRENT=1`。

### systemd 部署（无 Docker）

```bash
sudo mkdir -p /opt/codearts2api && sudo cp -r bin config.example.json auths /opt/codearts2api/
sudo cp deploy/codearts2api.service /etc/systemd/system/
# 编辑 /opt/codearts2api/.env 写 CA2A_API_KEY，改好 config.json
sudo systemctl daemon-reload && sudo systemctl enable --now codearts2api
```

### 完整源码包与部署核对

完成代码提交后，可用 Python 3.9+ 生成完整源码包；仅从指定 Git 提交导出产品清单，
包含 Compose、构建叠加文件、Dockerfile、示例配置、Go 源码、CI/发布输入和校验脚本，
排除真实 `.env`、`config.json`、凭证、数据和工作区工具。相同提交生成相同 ZIP，
不覆盖已有文件：

```bash
python3 scripts/release-source.py --commit HEAD
```

产物为 `codearts2api-source-<提交前12位>.zip`，附带 `release.json` 文件校验和及仅含
源码提交的 `release.env`。源码包不包含 `.git`；解压后在原部署目录保留现有挂载路径，
按包内身份本地构建：

```bash
set -a
. ./release.env
set +a
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

部署核对使用环境变量提供密钥，仅发送 GET：检查健康、实际提交、清理后的控制台及
审计/模型/连接接口；旧页面、404、401、身份未知或提交不符都会返回非零状态。
反向代理有路径前缀时将其写入 `--url`。以下域名替换为自己的服务地址：

```bash
read -r -s -p "服务 API Key: " CA2A_API_KEY
export CA2A_API_KEY
python3 scripts/verify-deployment.py --url https://gateway.example.com --expected-commit "$CA2A_BUILD_COMMIT"
unset CA2A_API_KEY
```

此校验不会发起聊天、模型探测或重新拉取；审计被配置关闭时会明确提示。它验证交付的
版本和接口，模型实际权益需结合来源结果及真实调用判断。

更完整的拉取/升级/自动更新/私有仓库登录/回滚说明见 [docs/deployment.md](docs/deployment.md)。

## 请求审计与账号状态

后台的“请求审计”展示最近请求的模型、所选账号、HTTP 状态与执行结果、安全供应商错误码、尝试次数，以及排队/首数据/总耗时。响应头 `X-Request-Id` 可关联服务日志和审计记录。`GET /admin/api/requests` 与 `GET /admin/api/audit` 使用现有 Bearer Key 鉴权；请求列表支持 `limit`（最多 200）、`before`（序号游标）、`model`、`account`、`outcome`、`status`、`since` 过滤。

默认审计保留 7 天，最多 8 个 8 MiB 文件（含当前文件，合计不超过 64 MiB），入队上限 256，近期窗口 512 条。重启会恢复近期记录和序号；新请求可能让窗口中最旧的记录退出分页。更早的保留文件位于 `<state_file 所在目录>/audit/`，供本地归档查看。队列满或磁盘写入失败不会等待写盘，后台会显示丢弃与写入失败计数。审计仅保存元数据，不保存提示词、回复、工具参数、请求头、密钥或登录凭证；共享服务 Key 无法区分 New API 的具体终端用户。

“连接日志”在请求尚未结束时显示当前阶段、阶段/总耗时、账号、尝试次数及实际最后上游读取进展，可区分等待账号、等待凭证刷新、等待上游响应头、等待首个 SSE 数据、转发/聚合和写回。首个 SSE 数据不是首个生成 token，长流耗时大也不代表无进展。页面仅在登录且位于前台时约每 2 秒读取 `GET /admin/api/connections`，离开页面、切后台或退出登录即停止。

福利目录会先查询该账号的福利通道开关。上游明确返回关闭时，面板显示“福利通道已关闭”，跳过旧福利领取和目录请求，并撤销该账号旧福利路由；模型以 Agent / 内置目录实际返回的标识为准。开关查询失败或格式不明时保留原有证据并显示失败，不会猜测所有账号都已关闭。HTTP 成功但福利业务码非成功值，会单独显示业务错误，不再统称 HTTP 错误；业务码 `4000` 也不会单独被当作全局关闭。

活动跟踪最多 512 个请求，每个请求最多 32 个阶段；超限和截断会显示累计计数。结束后的记录复用审计近期窗口，旧版记录没有阶段历史时明确显示不可用。关闭请求审计也会关闭连接跟踪。

账号的“可调度 / 冷却 / 禁用”与历史失败分别展示。`err N` 是连续失败计数，默认达到 3 次冷却 10 分钟；冷却自然到期不会自动清除历史计数。实际请求成功后会清理已过时的当前错误并持久化一次，保留历史失败，不会撤销当前冷却或人工禁用。升级前的失败时间无法恢复，显示为“时间未知”。签到领取积分与具体模型的福利资格不同，`benefit not found` 仍需要核对该模型的权益。

## 环境变量配置

除了 `config.json`，还支持以下环境变量覆盖：

| 变量名 | 说明 | 默认值 |
|--------|------|--------|
| `CA2A_API_KEY` | API 访问密钥 | - |
| `CA2A_LISTEN` | 监听地址 | `:7866` |
| `CA2A_AUTH_DIR` | 凭证目录 | `./auths` |
| `CA2A_STATE_FILE` | 状态文件 | `./data/state.json` |
| `CA2A_DEFAULT_MODEL` | 默认模型 | `glm-5.2` |
| `CA2A_OAUTH_CALLBACK_HOST` | OAuth 回调端口覆盖（旧变量名；仅显式端口生效，回调主机仍为浏览器自身的 `127.0.0.1`） | 跟随服务端口 |
| `CA2A_WATCH_ENABLED` | 调度器开关 | `true` |
| `CA2A_WATCH_POLL_MINUTES` | 轮询间隔（分钟） | `30` |
| `CA2A_WATCH_REFRESH_SKEW` | 提前刷新时间（分钟） | `30` |
| `CA2A_WATCH_KEEPALIVE_INTERVAL` | 保活间隔（分钟） | `15` |
| `CA2A_MAX_CONCURRENT` | 单账号最大活跃请求数 | `1` |
| `CA2A_MAX_PENDING` | 进程内等待上限（另限制总在途数为账号容量加此值） | `64` |
| `CA2A_STARTUP_TIMEOUT_SECONDS` | 读取请求至上游首数据总预算（秒） | `90` |
| `CA2A_ACCOUNT_WAIT_SECONDS` | 每请求累计账号槽位等待预算（秒） | `10` |
| `CA2A_UPSTREAM_TIMEOUT_SECONDS` | 上游响应头/首数据/读取空闲超时（秒） | `60` |
| `CA2A_STREAM_WRITE_TIMEOUT_SECONDS` | 下游单次写入/flush 超时（秒） | `30` |
| `CA2A_QUEUE_RETRY_SECONDS` | 上游排队/TPM 重试间隔（秒） | `2` |
| `CA2A_QUEUE_MAX_ATTEMPTS` | 每账号总尝试次数（含首次） | `3` |
| `CA2A_KEEPALIVE_WINDOW` | 保活窗口 | `10m` |
| `CA2A_BENEFIT_AUTO_CLAIM` | 福利通道明确启用时自动领取（幂等；关闭后不主动领取） | `true` |
| `CA2A_CREDITS_AUTO_CLAIM` | watchdog 自动领取每日签到及新用户积分 | `true` |
| `CA2A_LOGIN_CLIENT_ID` | WebUI 登录使用的 OAuth client_id | 已有账号的取值，否则 `codearts-agent` |
| `CA2A_AUDIT_ENABLED` | 开启请求元数据审计 | `true` |
| `CA2A_AUDIT_DIR` | 容器内审计目录 | `<state_file 所在目录>/audit` |
| `CA2A_AUDIT_MAX_QUEUE` | 非阻塞审计队列容量 | `256` |
| `CA2A_AUDIT_RECENT` | 可查询的近期记录数 | `512` |
| `CA2A_AUDIT_MAX_FILE_BYTES` | 单个审计文件上限（字节） | `8388608` |
| `CA2A_AUDIT_MAX_FILES` | 文件数量上限（含当前文件） | `8` |
| `CA2A_AUDIT_MAX_TOTAL_BYTES` | 所有审计文件合计上限（字节） | `67108864` |
| `CA2A_AUDIT_RETENTION_DAYS` | 审计文件保留天数 | `7` |
| `CA2A_AUDIT_FLUSH_INTERVAL` | 审计文件同步周期 | `1s` |

部署相关变量（Compose 插值）另见 `.env.example`：`CA2A_PORT`、`TZ`、
`CA2A_CONTAINER_NAME`、`CA2A_RESTART_POLICY`、`CA2A_AUTHS_PATH`、`CA2A_DATA_PATH`、
`CA2A_CONFIG_PATH`、`CA2A_IMAGE`、`CA2A_WATCHTOWER_CONTAINER_NAME`、`WATCHTOWER_POLL_INTERVAL`，
以及仅本地构建用的 `CA2A_BUILD_CONTEXT`、`CA2A_BUILD_COMMIT`、`CA2A_BUILD_TIME`、`CA2A_LOCAL_IMAGE`。

## 目录结构

```
cmd/server/        HTTP 服务（config + main）
cmd/login/         华为云 OAuth2 PKCE 登录
cmd/credit/        账号登录态日报（新增并发信息）
cmd/apply/         批量 token 续期（使用 pool 包）
cmd/models/        查看账号可用模型（内置 + 限时福利，可选 -claim 领取）
internal/auth/     auth 文件读写
internal/upstream/ 云端客户端（登录/聊天/SSE/模型发现）+ 逆向常量
internal/pool/     账号池（token 校验/自动刷新/冷却/并发控制）
internal/scheduler/ token 续期看门狗（新增保活机制）
internal/server/   OpenAI 兼容路由
internal/audit/    有界请求审计与活动连接跟踪
internal/telemetry/ 请求阶段与读取进展观察器
scripts/           完整源码打包、发布元数据与只读部署核对
deploy/            systemd unit 样例
docs/              逆向过程、接口清单与部署说明
```

## 免责声明

仅供学习和研究使用。使用者需遵守华为云服务条款，自行承担使用风险。

## License

MIT