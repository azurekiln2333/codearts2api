# 部署与升级手册（CodeArts2API）

本文是 README「部署与升级」的扩展运维手册，覆盖镜像拉取、自动更新、镜像源切换、
版本发布、回滚与常见故障边界。三篇 README 均包含同一套完整部署命令：
[简体中文](../README.md) · [繁體中文](../README.zh-TW.md) · [English](../README.en.md)。

> 适用范围：Docker Engine 24+、Docker Compose v2（`docker compose`）。
> 本文不声称任何镜像、Release 或远程流水线已经存在；仅描述本仓库内的部署与发布契约。

## 1. 组成与默认值

| 项目 | 默认值 | 说明 |
|------|--------|------|
| 应用镜像 | `ghcr.io/azurekiln2333/codearts2api:latest` | 由仓库实际 origin 推导；可用 `CA2A_IMAGE` 覆盖 |
| 服务名 / 容器名 | `codearts2api` | 容器名可用 `CA2A_CONTAINER_NAME` 覆盖 |
| 端口 | `CA2A_PORT=7866` | **唯一**端口入口，同时驱动宿主机端口、容器端口与 `CA2A_LISTEN` |
| 应用变量 | `env_file: .env` | 直接读取，无需逐项写入 YAML |
| 凭证目录 | `./auths` → `/app/auths` | `CA2A_AUTHS_PATH` 可改宿主机侧 |
| 数据目录 | `./data` → `/app/data` | `CA2A_DATA_PATH` 可改宿主机侧 |
| 配置文件 | `./config.json` → `/app/config.json:ro` | 只读挂载，`CA2A_CONFIG_PATH` 可改 |
| 自动更新 | `nickfedor/watchtower:1.23.2` | 项目内隔离，仅更新本项目 `codearts2api` |
| 更新器容器名 | `codearts2api-watchtower` | `CA2A_WATCHTOWER_CONTAINER_NAME` 可覆盖 |
| 日志 | json-file 轮转 | `max-size: 10m`、`max-file: 3`，有界增长 |
| 健康检查 | 镜像内置 `HEALTHCHECK` | 用 `CA2A_PORT`（其次 `CA2A_LISTEN` 的端口）探测 `/healthz` |

`.env` 必须存在。CLI 的 `--env-file` 只改变 Compose 插值来源，不会改变服务里固定的
`env_file: .env`；只在 shell 里 `export` 的可选应用变量不会自动进入容器。

## 2. 默认部署：拉取已发布镜像（无本地构建）

```bash
# 仅首次执行；升级时不要覆盖已有文件
cp -n .env.example .env
cp -n config.example.json config.json
mkdir -p auths data

# .env 至少填 CA2A_API_KEY；换端口只改 CA2A_PORT
docker compose config --quiet      # 只校验语法与插值，不产生副作用
docker compose pull
docker compose up -d               # 标准部署：应用 + 自动更新
```

标准部署会同时启动 Watchtower 更新器（见第 4 节）。**Opt-out**：不要自动更新时用
`docker compose up -d codearts2api`，只启动应用服务，更新器不会启动。

检查：

```bash
curl -fsS http://127.0.0.1:${CA2A_PORT:-7866}/healthz
docker inspect --format '{{.Config.Image}} {{.Image}}' codearts2api
```

升级（保留全部挂载）：

```bash
docker compose pull
docker compose up -d
```

`docker compose up -d` 只重建变化的服务；重启旧容器不会替换镜像。

## 3. 首次发布的边界（重要）

- `ghcr.io/azurekiln2333/codearts2api:latest` 只有在**推送第一个 `v*` 标签且发布流水线
  成功**之后才存在。在那之前 `docker compose pull` 会报 manifest 不存在 / denied，
  这是预期结果，不是本机配置错误。
- GHCR 包**首次推送默认 private**。要匿名拉取需在 GitHub 包设置里改为 public；
  否则先登录：

  ```bash
  echo "$CR_PAT" | docker login ghcr.io -u <你的 GitHub 用户名> --password-stdin
  # 令牌至少含 read:packages（私有包）
  ```

- 在此之前请使用第 6 节的本地构建方式；本地构建不需要任何已发布镜像。

## 4. 自动更新（Watchtower）

标准部署命令 `docker compose up -d` 默认同时启动应用与更新器：

```bash
# 标准部署：应用 + 自动更新
docker compose up -d

# Opt-out：不要自动更新时只启动应用（更新器不会运行）
docker compose up -d codearts2api
```

启动后的更新器行为如下：

| 属性 | 值 | 作用 |
|------|----|------|
| 镜像 | `nickfedor/watchtower:1.23.2` | 固定版本；原 `containrrr/watchtower` 已归档 |
| `WATCHTOWER_LABEL_ENABLE` | `true` | 只更新显式带 `com.centurylinklabs.watchtower.enable="true"` 的容器 |
| `WATCHTOWER_SCOPE` | `${COMPOSE_PROJECT_NAME:-codearts2api}` | 只更新同 scope 标签的容器，隔离其他项目 |
| `WATCHTOWER_POLL_INTERVAL` | `600`（秒） | 检查间隔，可写入 `.env` 覆盖 |
| `WATCHTOWER_CLEANUP` | `"true"`（YAML 固定） | 更新后清理旧镜像；未暴露为 `.env` 变量 |
| `CA2A_WATCHTOWER_CONTAINER_NAME` | `codearts2api-watchtower` | 更新器容器名 |
| 自身标签 | `...enable: "false"` | 更新器永不自我更新 |
| 挂载 | `/var/run/docker.sock` | 更新容器镜像所必需 |

作用域说明：`--label-enable` 与 `--scope` 是**两个**条件，同时满足才会更新。因此
其他项目、其他 Compose 栈、无关容器都不会被本更新器触碰。

调整间隔（`.env`）：

```bash
WATCHTOWER_POLL_INTERVAL=3600   # 每小时检查一次
```

关闭已启用的更新器：

```bash
# 只停用并移除更新器，应用容器不受影响
docker compose stop watchtower
docker compose rm -f watchtower
```

注意：`docker compose up -d codearts2api` 只启动应用，**不会停掉**已在运行的更新器
（Compose 不会清理未列出的服务），要关闭请用上面两条命令。

本地构建方式默认不启用更新器目标，也不会用已发布镜像替换本地镜像。

### 私有仓库映像的更新器鉴权

宿主机 `docker login` 只写入宿主机自己的 `~/.docker/config.json`，**不会自动传给
Watchtower 容器**。更新器默认只挂载 Docker socket，没有 registry 凭据，所以：

- 公开映像（如已设为 public 的 GHCR 包）无需凭据；
- 私有 GHCR/CNB 映像要让自动更新真正可用，需给更新器提供**只读** Docker config。

Watchtower 官方约定：默认读取容器内 `/config.json`；用 `DOCKER_CONFIG` 指定 config 所在
目录（文件会变动时更稳）。可选新建 `docker-compose.override.yml`（默认文件链自动合并；
用 `-f` 显式列出多文件时需再追加 `-f docker-compose.override.yml`）：

```yaml
services:
  watchtower:
    environment:
      DOCKER_CONFIG: /config
    volumes:
      - ${HOME}/.docker/config.json:/config/config.json:ro
```

仓库**默认不挂载**该文件，公开镜像部署不会因缺少本机私密文件而失败；这是可选 opt-in。
不要把 token 写进 YAML 或日志，登录一律 `--password-stdin`。

若 `~/.docker/config.json` 使用 `credsStore`/`credHelpers`（例如 Docker Desktop），更新器容器
内没有对应 helper，鉴权会失败；此时改用手工 `auths` 写法的 `config.json`（官方文档给出
`auth` 的 base64 生成方式），或把 helper 一并挂载。

## 5. 镜像源切换与固定版本

`CA2A_IMAGE` 决定拉取来源，写入 `.env` 或命令行前缀均可：

```bash
# 固定版本（生产建议）
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.3

# 按摘要固定（完全不可变）
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api@sha256:<digest>

# 切换到 CNB 制品库（slug 为动态变量，填你的实际仓库路径小写形式）
CA2A_IMAGE=docker.cnb.cool/<CNB_REPO_SLUG_LOWERCASE>:latest
```

架构中间标签：CNB 流水线做多架构组装时会先构建并推送两个**按架构的中间标签**
`vX.Y.Z-amd64`、`vX.Y.Z-arm64`（由 `scripts/release-metadata.py arch-tags` 统一产出，
固定 amd64 在前、arm64 在后），再合成多架构的 `vX.Y.Z`（stable 额外加 `latest`）。
这些 `-amd64` / `-arm64` 标签由 CNB 流水线生成，保留供调试与单架构排查使用；
**常规部署不要使用它们**，请继续用多架构的 `vX.Y.Z` 或 `latest`，Compose 与
Watchtower 会自动选择匹配宿主架构的镜像。

CNB 制品库登录（`CNB_TOKEN` 在流水线内由平台签发，本地可用访问令牌；密码走标准输入，
不落命令行历史/日志）：

```bash
printf '%s' "$CNB_TOKEN" | docker login docker.cnb.cool -u cnb --password-stdin
```

本仓库没有声明固定的 CNB slug；`<CNB_REPO_SLUG_LOWERCASE>` 是占位符，不是已存在的
公开镜像仓库。其他镜像源/镜像加速同理，只需把 `CA2A_IMAGE` 指过去。

## 6. 本地源码构建（叠加文件）

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

叠加文件的作用：

- 应用镜像改为本地 `codearts2api:local`（`CA2A_LOCAL_IMAGE` 可覆盖），`pull_policy: build`；
- 给应用容器打上“不自动更新”的标签，更新器不会替换本地构建；
- 更新器被 profile 门控，普通 `up -d` 不启动。

注入构建身份（后台“运行配置”显示实际提交与 UTC 构建时间）：

```bash
CA2A_BUILD_COMMIT="$(git rev-parse HEAD)" \
CA2A_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

`CA2A_BUILD_CONTEXT` 可覆盖构建上下文。未注入时后台显示“未知构建”，不会猜测。
源码更新后必须重新构建；只 `restart` 不会替换旧镜像。叠加构建 overlay 时更新器默认不启动
（保留 `auto-update` profile）；即使显式加 `--profile auto-update`，应用容器 `...enable`
标签为 `false`，也不会被自动替换。

### 多平台镜像（buildx：linux/amd64 + linux/arm64）

Dockerfile 的 Go 编译阶段使用 `GOOS/GOARCH` 交叉编译（`CGO_ENABLED=0`），此阶段无需 QEMU。最终 Alpine 阶段的 `RUN apk add` 仍需要构建环境提供目标架构执行支持（原生节点或模拟）：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg BUILD_COMMIT="$(git rev-parse HEAD)" \
  --build-arg BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t <image>:<tag> .
```

镜像带 OCI 标签（`org.opencontainers.image.title/source/revision/created`）。其中
`org.opencontainers.image.source` 指向本仓库，有助于 GHCR 包与仓库关联；应用运行时
不依赖这些标签，构建身份仍以 `BUILD_COMMIT`/`BUILD_TIME` 注入的值为准。

## 7. 版本标签发布契约（v*）

推送 `v*` 标签（如 `v1.2.3`）触发受信任发布：

1. **测试门禁**：Go `test` / `race` / `vet` / `build` + Python 发布工具测试；失败即止。
2. **镜像**：发布 `ghcr.io/azurekiln2333/codearts2api:v1.2.3`；稳定版额外更新 `latest`。
3. **源码 Release**：附显式清单导出的源码 ZIP 与 sha256；清单排除真实 `.env`、
   `config.json`、凭证、数据、旧 ZIP 与平台工具。
4. **预发布**：`v1.2.3-rc.1` 等只标记 prerelease，**不覆盖 `latest`**。
5. **PR**：只校验，不认证、不发布；权限最小化。
6. **构建身份**：使用实际完整提交与 UTC 构建时间，不从未知信息推断。

CNB 侧规则相同：`main` / `pull_request` 只校验，`v*` 标签才发布源码 Release 与镜像；
平台令牌（`CNB_TOKEN`）为临时凭据，仓库内不保存任何真实凭据。

上述是仓库内工作流的**契约**。在推送标签并让远程流水线实际运行前，镜像与 Release
并不存在；本文档不作相反声称。

CNB 默认使用平台仓库路径与登录，无需覆盖变量。可选发布覆盖应配置在 **CNB 仓库 CI 变量**中：`CNB_IMAGE`（不含标签的仓库路径）、`CNB_REGISTRY`、`CNB_REGISTRY_USER`、`CNB_REGISTRY_PASSWORD`。外部注册表必须提供密码。优先使用 `CNB_IMAGE`，不要混用服务器 `.env` 中带标签或摘要的 `CA2A_IMAGE`。标签只支持 `vMAJOR.MINOR.PATCH[-prerelease]`，禁止 `+build` 元数据，长度须容纳架构后缀。

## 8. 回滚

```bash
docker inspect --format '{{.Config.Image}} {{.Image}}' codearts2api   # 先记录当前镜像
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose pull
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose up -d
```

- 回滚保留同一组挂载（`.env`、`config.json`、`auths/`、`data/`），不要切到空目录。
- 凭证与数据结构是增量的，回退镜像无需数据迁移。
- 若不希望回滚后又被自动升级，先停用更新器（第 4 节）再回滚。
- 需要完全确定内容时用摘要固定（`@sha256:...`）。

## 9. 故障对照表

| 现象 | 含义 | 处理 |
|------|------|------|
| `pull access denied` / `manifest unknown` | 镜像尚未发布，或 GHCR 包为 private | 用本地构建；或改为 public / 登录 GHCR |
| `unauthorized: authentication required` | 私有仓库未登录或令牌权限不足 | `docker login <registry>`，令牌含 `read:packages`；自动更新还需给更新器挂只读 config |
| 自动更新对私有镜像反复报 401 | 更新器无 registry 凭据（宿主 login 不传递） | 加 `docker-compose.override.yml` 挂只读 Docker config（第 4 节） |
| `CA2A_API_KEY` 报错导致起容器失败 | 未设置服务密钥（服务拒绝启动） | 在 `.env` 设置 `CA2A_API_KEY` |
| 端口冲突 | `CA2A_PORT` 被占用 | 改 `.env` 的 `CA2A_PORT`（一个变量同步三处） |
| 更新器反复重启容器 | 使用了可移动标签（`latest`） | 固定版本或摘要；或停用更新器 |
| 本地构建的镜像被替换 | 未使用叠加文件 | 用 `-f docker-compose.build.yml` 且保持“不自动更新”标签 |
| 凭证丢失 | 升级时切换了挂载路径 | 恢复原 `CA2A_AUTHS_PATH` / `CA2A_DATA_PATH` |

## 10. 部署核对（只读）

```bash
read -r -s -p "服务 API Key: " CA2A_API_KEY
export CA2A_API_KEY
python3 scripts/verify-deployment.py --url https://gateway.example.com \
  --expected-commit "$CA2A_BUILD_COMMIT"
unset CA2A_API_KEY
```

仅发送有界 GET：健康、清理后的控制台标记、实际构建身份，以及审计/请求/模型/连接
接口结构。旧页面、404、401、身份未知或提交不符均返回非零。审计被关闭时会明确提示，
不当成“缺路由成功”。它不发起聊天、模型探测或重新拉取。

## 11. 升级安全检查清单

- [ ] 备份 `.env`、`config.json`、`auths/`、`data/`
- [ ] 记录当前镜像与标签/摘要
- [ ] `docker compose config --quiet` 通过
- [ ] 挂载路径未改变
- [ ] 明确这次升级走“拉取已发布镜像”还是“本地构建”
- [ ] 需要固定版本时写死 `CA2A_IMAGE` 标签或摘要
- [ ] 升级后跑一次只读核对并检查审计/连接日志