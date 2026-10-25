# R19 独立集成复核（trellis-check）

- 复核对象：分支 `fix/deepseek-concurrency-20261021` 上 R19 的四条工作流
  （p4 Compose/Dockerfile、p6 导出器/测试、p7 CI、pA 文档）
- 复核者：pane `w1C:pA`（只读复核 + 文档 owner；不改他人产物文件）
- 复核方法：**以真实源码为准**，不把「测试通过」当作「可运行」的证据。
  每条结论都给出 `文件:行号` 证据；无法在本机验证的部分单列在「残余风险」。
- 未执行：任何 `git commit/push/tag`、镜像构建、GH/CNB 流水线真实运行、
  生产启动、读取真实 `.env`/`config.json`/`auths/`。

---

## 1. 结论摘要

| 工作流 | 结论 | 关键依据 |
|---|---|---|
| p4 Compose / Dockerfile / `.env.example` | **通过（含 1 个已由文档补位的集成缺口）** | 默认发布镜像 + 默认启用更新器 + overlay 门控；渲染验证一致 |
| p6 导出器 / 契约测试 | **通过（测试强度需正确理解）** | 清单完整；27 条契约断言全绿，但多数是文本/结构断言 |
| p7 GitHub + CNB 流水线 | **通过（3 条低危改进项）** | 信任边界正确、Action 版本真实存在、CNB 语义经官方文档核对 |
| pA 三语 README / 部署指南 | **通过** | 三语结构等价、无「已发布」断言、无 token 打印 |

阻断级问题：**0**。高危：**1**（私有 registry 下更新器无凭据，已由文档补齐，
Compose 侧有意不改）。中危：**2**（p6 守卫与测试强度）。低危：**3**（p7：外部
registry 提前失败、中间标签文档化、删除重复 `go.yml`）。

采纳与落定：主协调已采纳全部发现。本文件冻结时，**已落实**的是「中间标签改为
文档化」与本次文档修订（§10）；「删除 `go.yml`」「多语 updater 守卫」
「外部 registry 提前失败」三项仍为 p7/p6 待完成项。

---

## 2. 复核证据（本机可复现）

```
python -m unittest scripts.test_deployment_contracts            → Ran 28 tests  OK
python -m unittest scripts.test_release_metadata scripts.test_release_tools
                                                              → Ran 57 tests  OK

合计 85 条绿。数量随 p6/p7 迭代变动（初稿 22 → 27 → 28），以冻结时为准。
```

三语 README 结构等价（脚本统计）：`README.md` / `README.zh-TW.md` / `README.en.md`
均为 `H2=8  H3=18  H4=1  围栏=44`；`docs/deployment.md` 为 `H2=11 H3=2 H4=0 围栏=30`。

Compose 渲染（复制 compose 文件到 `.local-test/r19-render/` + 合成 `.env`，
**从未读取真实 `.env`**，使用 `.local-test/tools/docker-compose-v2.29.7.exe`）：

- 默认：`config --services` = `codearts2api`, `watchtower`；镜像
  `ghcr.io/azurekiln2333/codearts2api:latest`；`init: true`；`7866:7866`；
  三个挂载 `auths`/`data`/`config.json`；更新器镜像 `nickfedor/watchtower:1.23.2`；
  仅挂 `docker.sock`。
- overlay：`config --services` = 仅 `codearts2api`；镜像 `codearts2api:local`；
  `pull_policy: build`；应用容器 `...enable: "false"`。

---

## 3. p4：Compose / Dockerfile / `.env.example`

### 3.1 通过项

- 默认镜像为已发布镜像，且没有 `build:`（`docker-compose.yml:6`）——默认部署
  不再依赖本地源码。
- 单端口驱动三处：宿主映射、容器内 `CA2A_PORT`、`CA2A_LISTEN`（`docker-compose.yml:11,18,20`），
  与 `Dockerfile` 的 HEALTHCHECK 回退链（`CA2A_PORT` → `CA2A_LISTEN` → 7866）一致。
- 原产品挂载保留（`docker-compose.yml:21-24`），更新器重建容器不会丢 `auths/`、`data/`、
  `config.json`。
- 更新器隔离是**双条件**：`WATCHTOWER_LABEL_ENABLE: "true"` + `WATCHTOWER_SCOPE`
  （`docker-compose.yml:47,49`），应用侧标签 `enable: "true"` + 同 scope
  （`docker-compose.yml:27,28`），更新器自身 `enable: "false"`（`:56`）不自我更新。
- 日志上限 10m×3（`:32,61`），`init: true`（`:9`）——避免 PID 1 僵尸进程。
- 默认部署**不带 profile**（渲染确认），所以标准 `docker compose up -d` 确实会拉起
  更新器；`docker compose up -d codearts2api` 是干净的 opt-out。overlay 侧才用
  `profiles: [auto-update]`（渲染确认）。
- `.env.example:22-37` 的命令、禁用方式、overlay profile 说明与真实 Compose 一致。

### 3.2 集成缺口（高危 → 已由 pA 文档补位，Compose 有意不改）

**更新器在私有 registry 下没有任何凭据。** 更新器只挂载 `docker.sock`
（`docker-compose.yml:52-53`），而宿主机 `docker login` 只写宿主机自己的
`~/.docker/config.json`，**不会传递进容器**。维护版官方文档
（`nicholas-fedor/watchtower` → `docs/advanced-features/private-registries`）明确：
凭据来自容器内 `/config.json`，或用 `DOCKER_CONFIG` 指向 config 所在目录；
Compose 场景示例即 `- <PATH>/.docker/config.json:/config.json`。

影响：GHCR 包若保持 private，或使用私有 `docker.cnb.cool` 制品库，
更新器会持续 401，自动更新「看起来启用、实际不生效」。

处置（已落地在文档，非 Compose）：

- 三语 README + `docs/deployment.md` 新增「私有仓库镜像的更新器鉴权」小节，
  给出**可选** `docker-compose.override.yml`：
  `DOCKER_CONFIG: /config` + `${HOME}/.docker/config.json:/config/config.json:ro`。
- **不**在默认 Compose 挂载该文件：公开镜像部署不能因缺少本地私密文件而失败
  （用户明确要求），因此是 opt-in。
- 说明 `-f` 显式列出多文件时 Compose 不会自动合并 `override`，需再追加
  `-f docker-compose.override.yml`。
- 补充官方文档「Credential helpers」一节的实际坑：若宿主机 `~/.docker/config.json`
  使用 `credsStore`/`credHelpers`（Docker Desktop 常见），更新器镜像内没有对应 helper，
  鉴权仍会失败；此时需改用手工 `auths` 的 `config.json` 或一并挂载 helper。

遗留：该 override 是用户自建文件，契约测试覆盖不到；属于文档契约而非代码契约。

---

## 4. p6：导出器与契约测试

### 4.1 通过项

- `REQUIRED_FILES`（`scripts/release-source.py`）已覆盖 R19 全部新产物：
  `.cnb.yml`、`docker-compose.build.yml`、三语 README、`scripts/release-metadata.py`、
  `test_release_metadata.py`、`test_deployment_contracts.py`、`verify-deployment.py`，
  以及两条 workflow。`docs/*.md` 经 `product_path` 规则随源码发布，
  故 `docs/deployment.md` 会进源码包（测试亦有断言）。
- 导出器按 git 树取文件，未跟踪的 `scripts/__pycache__/` 不会进入源码包。
- `test_no_credential_literals` 守住 Compose 里不出现明文凭据。

### 4.2 测试强度（需正确理解，中危）

`scripts/test_deployment_contracts.py` 的多数断言是**文本/结构断言**，不能证明
「流水线真的能跑」。例如 `test_race_jobs_have_a_c_toolchain`
（`test_deployment_contracts.py:539`）只检查 `-race` 条目文本里出现
`build-base`/`gcc` 之类 token 与 `CGO_ENABLED=1`；它无法证明运行容器里真有 gcc。
这正是本次复核坚持读源码、并去核对 CNB 官方语义的原因（见 §5.2）。
建议在实现说明里保留这句话，避免后来者把绿灯当成可运行性证明。

### 4.3 发现（低危）：文档一致性断言只覆盖一种语言

`test_updater_enablement_matches_documentation`
（`test_deployment_contracts.py:271-293`）的 `documents` 只含
`README.md`、`.env.example`、`docs/deployment.md`，**不含**
`README.zh-TW.md` / `README.en.md`。因此未来若只改英文/繁中 README 的 opt-out
与禁用命令说明，CI 不会报错，三语等价会悄悄退化。
建议：把 `documents` 改为遍历模块已有的 `README_LANGUAGES`
（`test_deployment_contracts.py:38`）。当前三语内容是一致的（见 §6），仅是守卫缺口。

---

## 5. p7：GitHub Actions 与 CNB 流水线

### 5.1 GitHub：通过

- 信任边界正确：工作流默认 `permissions: contents: read`
  （`build-and-release.yml:15-16`）；`release` 作业 `contents: write`（`:56`）、
  `image` 作业 `packages: write`（`:139`），两者都带
  `if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')`
  且 `needs: [test]`（`:52,134`）。PR 不可能发布，分支推送不可能发布。
- 版本/`latest`/校验和全部交给共享 helper，YAML 内不内联决策：
  `release_flags` 只有 `--prerelease` / `--latest` 二选一（`:117-120`），
  `image-tags` 产出 tag 列表（`:177`）。
- 多架构：`platforms: linux/amd64,linux/arm64`（`:176`），`push: true`（`:175`）。
- `test` 作业装 `pyyaml`（`:33`）后跑三套 Python 测试（`:42-46`），
  race 用 `CGO_ENABLED=1`（`:35`）。
- 7 个 Action 版本经 GitHub releases 源逐一核对**真实存在且为当前最新**：
  `actions/checkout@v7.0.1`、`setup-go@v7.0.0`、`setup-python@v7.1.0`、
  `docker/setup-qemu-action@v4.4.0`、`setup-buildx-action@v4.4.1`、
  `login-action@v4.7.0`、`build-push-action@v7.4.0`。
- 运行时版本可解析：`golang:1.27-alpine`、`alpine:3.24` 在 Docker Hub 均存在；
  `go-version: '1.27.x'` 与 `go.mod` 的 `go 1.22` 向前兼容。

### 5.2 CNB：通过（语义经官方文档核对）

> **更正并重新定性（原稿误写为“不成立”，已纠）。** CNB Alpine 缺 gcc 是**早期代码的
> 真实缺陷**：当时 `prepare-tools` 与 `-race` 任务不在同一个可共享工具链的位置，
> `go test -race` 需要 cgo，缺 `build-base` 就会链接失败。p7 已修复该缺陷（把
> `build-base` 安装与 `CGO_ENABLED=1 go test -race` 放进同一 pipeline entry 的流水线
> 容器内），并用契约测试锁住（`test_race_jobs_have_a_c_toolchain`）。复核结论是
>「缺陷存在过、现已修复」，不是「缺陷不存在」。

当前结构自洽，依据：

- 官方语义：`docker.image` 指定的镜像会作为**流水线容器**，该 Pipeline 下
  **未自带 `image` 的脚本任务都在这个容器里执行**（`grammar.md` pipeline-image）；
  跨独立容器的共享只有 `CNB_BUILD_WORKSPACE`（默认 `/workspace`）与
  `docker.volumes` 声明目录（`script-vs-plugin.md` file-sharing）。
- 现状：`prepare-tools`（`apk add --no-cache git build-base python3 py3-yaml`，
  `.cnb.yml:19`）与 `go-race`（`CGO_ENABLED=1 go test -race ./...`，`.cnb.yml:25`）
  位于**同一个 pipeline entry**、同一个流水线容器内，故工具链可用；
  且 `jobs: &validation-jobs` 被 `main`、`pull_request`、`publish-image` 复用，
  三处都在各自 entry 里先装工具链（`:19,51,118`）。
- `services: [docker]` 会注入 docker daemon + CLI，并**自动 `docker login` 到
  `CNB_DOCKER_REGISTRY`**，buildx 多架构默认可用（`grammar.md` service:docker）。
  所以默认路径不用显式登录，覆盖路径才需自带凭据（见 §5.3 第 1 条）。
- `CNB_COMMIT`（全 SHA）、`CNB_BRANCH`（tag_push 时为 tag 名）、
  `CNB_REPO_SLUG_LOWERCASE` 均为真实内置变量（`build/build-in-env.md`）。
- `cnb:read-file` 按 `.json` 解析并把 `exports` 的 key 映射为环境变量
  （`.cnb.yml:93-98`），与 helper `manifest` 产出的 `archive` 字段对齐；
  `git:release` 无附件能力，改用 `cnbcool/attachments`（`:99-104`）——与官方
  `internal-steps/git/release.md` 一致。
- 镜像路径动态：默认 `$CNB_DOCKER_REGISTRY/$CNB_REPO_SLUG_LOWERCASE`，
  可用 `CA2A_IMAGE`/`CNB_IMAGE` 覆盖，未写死 slug。
- 架构中间标签已抽到 helper：`arch-tags` 统一产出 `vX.Y.Z-amd64` / `vX.Y.Z-arm64`
  （固定 amd64 在前），流水线据此构建、推送再 `imagetools create` 合成多架构 tag。

### 5.3 CNB 发现（低危）与采纳结果

三项均已上报主协调，采纳与落定情况如下（以冻结时工作区实际内容为准）：

1. **外部 registry 无凭据会静默跳过登录 → 采纳：改为提前失败**。
   原状：平台自动登录只覆盖 `CNB_DOCKER_REGISTRY`，若把 `CA2A_IMAGE` 指到 GHCR
   等外部仓库又没提供 `CNB_REGISTRY_PASSWORD`，会到 `docker push` 才报
   `unauthorized`。采纳后要求：目标 registry ≠ 默认值且无密码时提前 `exit 1`
   并提示需要 `CNB_REGISTRY_PASSWORD`。**本文件冻结时该改动尚未落地**
   （`.cnb.yml` 仍是条件登录），属 p7 待完成项。
2. **架构中间标签残留 → 采纳：保留供调试，改为文档化**。主协调决定保留
   `vX.Y.Z-amd64` / `-arm64` 以方便单架构排查，因此不再要求清理；改由文档
   声明来源与用途。**已落实**：三语 README 与 `docs/deployment.md` 均新增
   「架构中间标签」说明，明确这些标签由 CNB 流水线生成、保留供调试，
   常规部署仍用多架构的 `vX.Y.Z` / `latest`。
3. **`go.yml` 与 `build-and-release.yml` 重复 → 采纳：删除 `go.yml`**。
   目标状态：只保留完整的 `build-and-release.yml` 作为唯一门禁，并同步
   `release-source.py` 的 `REQUIRED_FILES`、`test_release_tools.py` 与
   `test_deployment_contracts.py` 中的引用。**本文件冻结时尚未落地**
   （`go.yml` 仍存在且仍在清单与测试里），属 p7 + p6 待完成项。

---

## 6. pA：三语 README 与部署指南

### 6.1 通过项

- 三语**等价**：结构计数完全相同（8/18/1/44），互相链接齐全
  （`ReadmeLanguageTests.test_every_language_links_to_the_other_two` 通过）。
- 部署内容**加入**而非替换：多轮上下文、token、审计、模型目录等原有产品文档
  三语均保留。
- 命令/环境变量/模型名未翻译；CNB slug 保持动态占位
  `<CNB_REPO_SLUG_LOWERCASE>`，并明确「请勿把它当成已存在的公开仓库」。
- 不声称镜像/Release 已存在：三语都写了「首个 `v*` 标签发布前没有可拉取的镜像」
  的边界说明。
- 默认部署命令与真实 Compose 一致：`docker compose up -d`（应用 + 更新器），
  opt-out 为 `docker compose up -d codearts2api`；禁用命令 `stop`/`rm -f watchtower`。
- 不打印 token：全部改用 `printf '%s' "$CNB_TOKEN" | docker login ... --password-stdin`
  （四份文档均为 0 处 `-p "$CNB_TOKEN"`）。
- 私有 registry 更新器鉴权已按维护版官方文档写清（见 §3.2），并明确
  「宿主机 `docker login` 不保证自动更新可用」。
- `docs/deployment.md` 随源码发布，故障排查表补了 401 与「更新器无凭据」两行。

### 6.2 说明

- 英文版字符数约为中文版两倍（35.6k vs 18.1k），是语言密度差异，非内容缺失
  （结构与 token 校验均通过）。
- 文档只描述「首个 `v*` 标签之后才有公开镜像」这一边界，未对当前是否已发布做
  任何断言。

---

## 7. 发现清单（按严重度）

| # | 严重度 | 归属 | 发现 | 采纳与落实状态 |
|---|---|---|---|---|
| F1 | 高 | p4 + pA | 更新器只挂 socket，私有 registry 无凭据；宿主机 `docker login` 不传递 | 主协调采纳；文档已补位（可选 override + `credsStore` 坑）；Compose 有意不改（公开部署不能因缺私密文件失败） |
| F2 | 中 | p6 | 文档一致性断言只覆盖 `README.md`，漏 zh-TW/en | 主协调采纳（p6 同步三语 updater enable 守卫）；**冻结时未落地** |
| F3 | 中 | p6 | 契约测试多为文本/结构断言，绿灯≠可运行 | 已在本文档 §4.2 记录，供后续参考（无需代码改动） |
| F4 | 低 | p7 | 覆盖到外部 registry 时静默跳过登录，失败原因不清晰 | 主协调采纳：改为提前失败；**冻结时未落地** |
| F5 | 低 | p7 | 每架构中间标签 `-amd64`/`-arm64` 保留在制品库 | 主协调决定**保留供调试**；**已落实（改为文档化）**：三语 README + `docs/deployment.md` |
| F6 | 低 | p7 | `go.yml` 与 `build-and-release.yml` 重复且门禁更弱 | 主协调采纳：删除 `go.yml`，只留 `build-and-release.yml`；**冻结时未落地**（清单/测试引用需同步） |

无阻断项。

---

## 8. 残余风险（本机无法验证，需真实运行确认）

1. **未在 CNB 真机跑过**：结论基于官方文档语义 + YAML 结构，
   首次 `v*` tag_push 仍需确认 `apk add` 是否在流水线容器内生效、
   `docker buildx imagetools create` 的实际行为、`cnbcool/attachments` 上传结果。
2. **未在 GitHub 真机跑过**：`gh release edit --latest` / `--prerelease`
   需 runner 上 `gh` 版本支持（ubuntu-latest 满足，但未实跑）。
3. **未真实构建多架构镜像**：`linux/arm64` 交叉编译与 `HEALTHCHECK` 端口推导
   仅静态核对。现阶段已由 root 补齐**二进制级**证据（见 §10）：两个架构各 4 个
   binary 真实交叉编译并通过 ELF 头校验；但**仍不是 Docker runtime 验证**
   （未跑 `docker build` / `docker run` / 容器内 HEALTHCHECK）。
4. **未验证 GHCR 首次推送后的可见性**：包默认 private 的结论来自 GitHub 文档，
   首次发布后需人工确认是否需要切 public 或配置 `read:packages`。
5. **未验证真实 `.env` 兼容性**：渲染使用合成 `.env`，未读取用户真实 `.env`。
6. **更新器 override 未被自动化覆盖**：用户自建 `docker-compose.override.yml`，
   契约测试无法断言其正确性。

---

## 9. 复跑清单

```bash
python -m unittest scripts.test_deployment_contracts
python -m unittest scripts.test_release_metadata scripts.test_release_tools
# 渲染复核（务必用合成 .env，勿读真实 .env）
#   .local-test/r19-render/ 下复制 compose 文件 + 合成 .env
#   docker compose -f docker-compose.yml config --services          → codearts2api watchtower
#   docker compose -f docker-compose.yml -f docker-compose.build.yml config --services → codearts2api
# 架构标签契约（helper 统一产出，流水线据此构建/推送/合成）
python scripts/release-metadata.py arch-tags  --image <image> --tag v1.2.3   # → v1.2.3-amd64, v1.2.3-arm64
python scripts/release-metadata.py image-tags --image <image> --tag v1.2.3   # → v1.2.3, latest
```

---

## 10. 冻结时的最终状态

### 10.1 本轮（主协调采纳后）pA 侧已落实

1. **F5 改为文档化（已完成）**：三语 README 与 `docs/deployment.md` 均新增
   「架构中间标签」说明：`vX.Y.Z-amd64` / `vX.Y.Z-arm64` 由 CNB 流水线
   （`scripts/release-metadata.py arch-tags`，固定 amd64 在前、arm64 在后）先构建并
   推送，再合成多架构 `vX.Y.Z`（stable 额外 `latest`）；这些标签保留供调试与
   单架构排查，**常规部署仍用多架构 `vX.Y.Z` / `latest`**。
2. **gcc 定性更正（已完成）**：原稿把「CNB 缺 gcc」写成「不成立」，现已在 §5.2
   明确：那是**早期代码的真实缺陷**，p7 已修复并用契约测试锁住。
3. **验证数值同步（已完成）**：冻结时契约测试 28 条、release 测试 57 条，共 **85 条全绿**
   （初稿为 22 条）；三语结构计数在内容增长后仍保持一致（8/18/1/44，
   `docs/deployment.md` 为 11/2/0/30）。

### 10.2 root 已完成的真实验证（非本 pane 执行，已记录）

- 全仓 Go 检查：**normal / `-race` / `vet` / `build` 全部通过**。
- 交叉编译：**`linux/amd64` 与 `linux/arm64` 各 4 个 binary 真实编译成功**，
  并对产物做了 **ELF 头验证**（确认目标架构与静态链接预期）。
- 边界澄清：这是**编译产物级**验证，**不是 Docker runtime 验证**——
  未执行 `docker build`、未 `docker run` 容器、未验证容器内 `HEALTHCHECK`
  与运行时行为。因此 §8 中「未真实构建多架构镜像」的措辞已改为上文的
  精确表述，不夸大也不低估。

### 10.3 仍待其他 owner 完成（不属 pA）

| 项 | 归属 | 冻结时状态 |
|---|---|---|
| 删除重复 `.github/workflows/go.yml` | p7 | 仍未删除 |
| `REQUIRED_FILES` / `test_release_tools.py` / `test_deployment_contracts.py` 同步去掉 `go.yml` | p6 | 未同步（删除后必须同步，否则契约测试会失败） |
| `test_updater_enablement_matches_documentation` 覆盖三语 README | p6 | 仍只查 `README.md` |
| CNB 外部 registry 无凭据提前 `exit 1` | p7 | 仍是条件登录 |

### 10.4 pA 文件冻结

本 pane 交付并冻结的文件：`README.md`、`README.zh-TW.md`、`README.en.md`、
`docs/deployment.md`、`research/r19-reference-findings.md`、
`research/r19-docs-delivery.md`、`research/r19-quality-review.md`。
冻结后不再修改；未执行任何 commit / push / tag / 发布 / 部署。

## 11. Root 最终集成核对（2026-10-26）

§10.3 是 pA 冻结时快照；四项均已完成：旧 go.yml 删除并从显式导出清单排除；三个 README 均受自动更新契约检查；外部 registry 缺凭据在发布前失败。最终三组 Python 测试共85项通过，官方 Compose 四场景、actionlint、全仓 Go normal/race/vet/build 和两架构各四个 ELF 编译均通过。未运行 Docker 镜像或远端发布。最终导出验证另记发布报告。
