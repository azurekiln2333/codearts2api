# CodeArts2API

> 華為雲 CodeArts Agent（盤古助手／碼道）的 OpenAI 相容代理。**無需執行 CodeArts Agent
> 用戶端**，純 Go 直連華為雲 API，多帳號輪替 + token 自動續期 + WebUI。

**語言 / Language / 語言：** [简体中文](README.md) · [繁體中文](README.zh-TW.md) · [English](README.en.md)

## 參考專案

本專案是 [Sliverkiss](https://github.com/Sliverkiss) 同系列開源專案的延伸實作，架構與維運形態參考了以下儲存庫：

- [workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) — WorkBuddy CN OpenAI 相容反向代理（帳號池／輪替／簽到架構）
- [traework2api](https://github.com/Sliverkiss/traework2api) — TRAE Work OpenAI 相容反向代理（零依賴 Go 骨架）
- [qoderwork2api](https://github.com/Sliverkiss/qoderwork2api) — QoderWork CN OpenAI 相容反向代理（OAuth 授權流程）

感謝原作者的開源與優秀設計。

## 快速開始（Ubuntu / Linux）

```bash
make linux            # bin/ 下 4 個 Linux 靜態二進位檔
make test
```

### 登入（華為雲帳號）

```bash
# 本機有瀏覽器
./login.sh

# 伺服器（無瀏覽器）：印出連結，任意機器瀏覽器開啟，ticket 輪詢下發
./login.sh -print-only

# 憑證落盤 auths/codearts-{user_id}.json
```

### 啟動

```bash
cp config.example.json config.json
export CA2A_API_KEY=你的隨機密鑰
./bin/codearts2api -config config.json
```

### 驗證 + WebUI

```bash
curl http://127.0.0.1:7866/healthz
curl http://127.0.0.1:7866/v1/models -H "Authorization: Bearer $CA2A_API_KEY"
curl -X POST http://127.0.0.1:7866/v1/chat/completions \
  -H "Authorization: Bearer $CA2A_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"snap-chat","messages":[{"role":"user","content":"你好"}]}'
```

瀏覽器開啟 **http://127.0.0.1:7866/panel/** 即管理主控台：帳號授權與狀態、模型目錄、請求稽核與連線日誌。多輪上下文依帳號自動續接（chat_id 分組）；也可用請求標頭
`X-Codearts-Chat-Id: <chatId>` 或 body 裡 `conversation_id` 明確指定工作階段。

串流請求設定 `"stream_options":{"include_usage":true}` 後，會在 `[DONE]` 前回傳一次
`choices: []` 的 usage chunk；其餘 chunk 的 `usage` 為 `null`。未啟用時不回傳 usage。
串流與非串流均優先使用上游 usage；上游未提供時依文字長度估算，估算值不是精確 token 計數。

後台「請求稽核」儲存成功完整請求的輸入、輸出、總 token，並區分「上游回傳」與「估算」；串流用戶端未開啟 `include_usage` 也會記錄後台用量。依帳號、模型彙總的是**目前篩選條件下的近期稽核視窗**，不受載入分頁影響，也不代表全部歷史或上游帳單。舊記錄、失敗請求及上游未提供的分項顯示未知；保留視窗之外的磁碟歸檔不會被掃描計入，重啟後會從已保留日誌恢復視窗。稽核關閉或遺失記錄時統計可能不完整。帳號頁「憑證剩餘時間」表示登入憑證何時過期，與模型 token 用量無關。

工具呼叫支援 `tool_choice` 的 `auto` / `none` / `required` 及指定函式物件。
指定函式時，閘道只向上游傳送該函式的定義，並轉換為上游支援的字串 `required`；
指定函式不存在或重複定義時回傳 400。上游 `InferHub.001001005` 參數錯誤也回傳 400，
不輪替、不冷卻帳號；若串流回應已開始，則用 SSE 錯誤事件結束，同樣不處罰帳號。

CodeArts 上游請求的 `max_tokens` / `max_completion_tokens` 超過 65536 時，閘道會限制為
65536，`/v1/models` 中已知的更大輸出上限也會相應限制。較小或未提供的值保持不變。
`InferHub.001001005.400` 是通用參數錯誤，其他參數或請求本文過大仍可能觸發它。

### 積分與 New API 餘額

總覽顯示帳號積分套餐的總額、已使用與剩餘；後台啟動後每 5 分鐘讀取一次，也可在總覽手動重新整理。
福利模型的獨立每日額度不包含在內。相同 UID 去重，但不同帳號可能共用同一套餐，彙總可能重複。
讀取失敗、過期或部分帳號未知時，不會把不完整總額當作零。完整快照若有無限積分套餐，總覽顯示「無限」。

`GET /v1/quota` 使用服務 Bearer Key 回傳原始積分和完整性狀態，快取查詢不會觸發上游請求；
無限額度以 `unlimited: true` 表示，總額和剩餘為 `null`：

```bash
curl http://127.0.0.1:7866/v1/quota -H "Authorization: Bearer $CA2A_API_KEY"
```

本專案目錄中的 New API 支援「進階自訂」渠道餘額路由。該渠道把餘額當作美元，因此只有在
自行確定換算比例後才應設定 `credits_per_usd`；下方的 `100` 只是設定範例，並非官方匯率。
將渠道 Base URL 設為服務根位址（例如 `http://127.0.0.1:7866`，不要以 `/v1` 結尾），
Key 設為服務 Key。新建渠道時，可在「進階自訂」渠道的「設定路由」→「完整 JSON」中使用
以下範例；它是路由編輯器的 JSON，不是整個渠道的 `settings`：

```json
{"advanced_routes":[
  {"incoming_path":"/v1/chat/completions","upstream_path":"/v1/chat/completions","converter":"none"},
  {"incoming_path":"/v1/responses","upstream_path":"/v1/responses","converter":"none"},
  {"incoming_path":"/v1/messages","upstream_path":"/v1/messages","converter":"none"},
  {"incoming_path":"/v1/models","upstream_path":"/v1/models","converter":"none"},
  {"incoming_path":"/v1/dashboard/billing/credit_grants","upstream_path":"/v1/quota?credits_per_usd=100","converter":"none"}
]}
```

Responses 和 Messages 是協定轉接器，僅支援文字和部分工具等功能。Responses 不支援持久化
（`store=true`）或透過 `previous_response_id` 延續；兩種轉接器均不支援圖片，使用時會回傳錯誤。
Messages 助手歷史中的文字會合併，交錯文字和工具呼叫之間的原始順序無法完整保留。
Chat 也僅支援部分參數；圖片、`stop` 和非文字回應格式等不支援的內容會回傳 400。

範例設定了聊天、Responses、Messages、模型列表和餘額路由；其他介面需要新增對應路由。已有渠道應保留原有路由，
在路由編輯器的「餘額查詢」中新增或編輯餘額路由；尚未設定時先選擇「新增管理路由」。將上游路徑
設為 `/v1/quota?credits_per_usd=100`，認證選擇「預設 Bearer」，儲存路由後再儲存渠道。依自行
確定的比例替換 `100`。多金鑰渠道不支援餘額查詢。

重新整理渠道餘額時，只會換算完整且新鮮的有限積分快照；快照不完整、過期或無限會回傳 503，不會寫入
虛假的零餘額，但 New API 可能繼續顯示上次的舊餘額。一般 OpenAI 類型渠道不使用該餘額路由，
需使用支援此功能的 New API 版本。

福利模型的獨立額度不計入積分餘額。New API 批次或定時更新餘額時，若積分餘額為零，可能自動停用
開啟自動停用的渠道，即使福利模型仍有額度。依賴福利模型時，可關閉該渠道的「異常自動停用」，或避免
批次及定時餘額更新；關閉自動停用也會影響該渠道其他異常的自動停用。單渠道手動查詢餘額不會
觸發上述批次或定時更新的自動停用行為。

### 多使用者併發與逾時（New API 接入）

請求會優先使用有空閒槽位且模型能力匹配的健康帳號；同負載帳號輪替分配，已有工作階段
保留有效帳號親和。挑號只讀已快取的模型目錄，未知目錄允許嘗試，不在聊天請求中
同步發現模型。單帳號預設 `max_concurrent: 1`，可依帳號實際容量調整。

等待槽位採用有界佇列，預設全行程 `max_pending: 64`。整個請求生命週期（含讀取
請求體、等待、重試間隔、生成與回應寫入）的在途數也限制為**啟用帳號的併發容量之和
+ max_pending**，防止上游快速拒絕後累積大量重試請求。共用同一帳號池的 handler
共用限制；多行程／多副本各自計數，部署多副本時需依共用帳號的實際額度分配容量。
佇列滿或累計排隊超過預設 10 秒，回傳 429 與 `Retry-After`，閘道應退避重試。
取消請求會退出排隊、關閉上游並釋放槽位，不因用戶端逾時而冷卻健康帳號。

預設時間預算如下，均可透過設定或環境變數覆蓋：

| 階段 | 設定 | 預設 |
|------|------|------|
| 讀請求體至上游首個資料（含排隊、刷新、重試、換號） | `startup_timeout_seconds` | 90 秒 |
| 所有帳號槽位等待累計 | `account_wait_seconds` | 10 秒 |
| 上游回應標頭、首資料及後續讀取閒置 | `upstream.timeout_seconds` | 60 秒 |
| 下游單次寫入或 flush | `stream_write_timeout_seconds` | 30 秒 |
| 上游併發／TPM 重試 | `queue_max_attempts` / `queue_retry_seconds` | 每帳號最多 3 次（含首次），間隔 2 秒 |

收到首個上游資料後停止啟動計時，持續輸出的長回覆可以超過 90 秒；上游持續靜默
達到閒置逾時才終止。回應標頭尚未送出時逾時回傳 504；已開始的串流在可寫時送出錯誤
與 `[DONE]`。收到協定結束標記立即關閉上游連線，不繼續等 TCP 斷開。New API 和
反向代理的逾時應大於所設定的啟動／閒置預算，並允許預期的長回覆時長。

升級時既有設定中的 `max_concurrent: 5`、`upstream.timeout_seconds: 120` 等明確值
會保留。`upstream.timeout_seconds` 現在實際用於聊天用戶端，初始帳號與執行期
新增／重載帳號都使用它；舊版該項對聊天未生效。需要新預設值時請同步修改自己的
`config.json`，不要只更新範例。日誌記錄排隊、首資料、總耗時、嘗試次數和結果，
便於與 New API 請求時間對應，不記錄提示詞或憑證。

### 模型清單與限時福利

`/v1/models` 回傳上游**精確模型 ID**（區分大小寫，如 `GLM-5.2`、`Qwen3-VL-235B`），
同時為含大寫的 ID 補一條小寫別名（`glm-5.2`），兩者都能用於聊天。限時福利
（免費套餐）模型額外帶 `benefit: true` 標記，聊天時伺服器會自動追加上游要求的
`maas_type: benefit` 請求標頭（依發起請求的帳號判定，多帳號套餐不同也不會串——
清單是各帳號可用模型的聯集，實際路由會優先挑目錄裡真有這個模型的帳號）。

New API 可以統一使用小寫模型名，例如 `glm-5.3`。每次請求會依選中帳號及目前憑證的目錄，映射成該路由實際回傳的名稱；其他帳號的大小寫不會覆蓋這次請求，福利來源和一般來源名稱不同時也會使用對應來源的原始 ID。沒有目錄證據的新名稱不會被猜成其他模型。稽核保留用戶端原名，「規範名」記錄最後一次真正發往上游的名稱；名稱映射成功仍不代表上游已授予模型權限。

福利模型清單隨免費套餐輪換，用下面這條指令核對目前帳號實際可用的模型：

```bash
go run ./cmd/models                 # 帳號可用模型（內建 + 福利）
go run ./cmd/models -json           # 機器可讀
go run ./cmd/models -claim          # 先領取限時福利再查詢（冪等，屬寫入操作）
```

限時福利模型**必須先領取才能呼叫**：未領取時上游一律回傳
`InferHub.4004.200 benefit not found`（實測）。領取是冪等操作，官方用戶端開啟模型
選單時也會呼叫，因此服務預設會領取（`benefit_auto_claim: true`）。不想讓服務寫帳號
可明確關閉：

```jsonc
"benefit_auto_claim": false   // 或 CA2A_BENEFIT_AUTO_CLAIM=0
```

關閉後不會主動領取福利；已領取的權益仍以上游帳號狀態為準，未領取時上游可能拒絕呼叫。

後台「模型目錄 → 重新拉取」會真正重新請求各帳號的 Agent、內建和福利來源，顯示來源狀態、嘗試／成功時間、保留的舊目錄及帳號模型排除。多次點擊共用同一個有界任務，任務啟動至少間隔 15 秒，單次最多 60 秒、平行處理 2 個帳號；失敗來源保留上次成功資料並標為舊資料。`GET /admin/api/models` 只讀快取，`POST /admin/api/models/refresh` 回傳 202 與任務狀態。

`model_not_available` 表示目前符合調度條件的帳號均未提供該模型或存在有效的能力拒絕快取。先在目錄查看精確模型 ID、來源失敗與排除原因。成功拉取對應的有效來源後，會重置這次拉取前的相關舊排除，允許下次真實呼叫重新判斷；不會把目錄收錄當成權限驗證，也不會清除併發請求剛寫入的新拒絕。官網能看到模型不等於每個已設定帳號都有同樣權益。完整且新鮮的帳號目錄確認沒有請求模型時，聊天回傳 404 `model_not_available`，公開 `/v1/models` 也會過濾這類無帳號支援的預置項。`glm-5.3-flash` 與 `glm-5.3` 是不同模型，不會相互替換。503 `no_healthy_account` 則表示目前沒有健康可調度帳號，例如帳號全部停用、冷卻或憑證不可用；併發排隊滿／等待逾時回傳 429，啟動或上游閒置逾時回傳 504。稽核裡的 `attempts: 0` 表示尚未向上游送出推理請求。

`TM.00001041` / `concurrency_limit` 表示上游併發工作階段拒絕。若還有合格的未嘗試帳號且輪替預算允許，會優先換號；沒有後備時沿用有限重試。預設三個帳號全部回傳該錯誤時最多形成 A/B/CCC 五次嘗試，單帳號仍最多三次。HTTP 400、HTTP 429 或 SSE 包裝的明確併發拒絕都不會誤冷卻整個帳號；一般限速和 TPM 仍保留原有策略。`attempts` 是實際出站次數，需結合 `tried` 和連線軌跡判斷是否換過帳號。

### 模型可用性探測

`/v1/models` 回傳發現的目錄並過濾已知不可用項；未被拒絕不代表已驗證可呼叫。上游模型發現（agent-center / builtin / 福利
閘道）可能列出目前帳號尚未註冊的模型（如回傳 `InferHub.002002009.404`）。服務會週期性用一條最小請求探測並快取結論，
真實請求失敗也會被學習（`not registered` / `benefit not found` 記為帳號能力問題，
不會給健康帳號記錯誤冷卻）。探測結果快取 30 分鐘。

### API Key 必填

服務沒有 API Key 會**拒絕啟動**（舊版本會回退到公開的 `dummy-key-for-codearts`，
等於把介面暴露給任何人）。三選一：

```bash
# 1) env（建議，搭配 .env / systemd EnvironmentFile）
export CA2A_API_KEY=$(openssl rand -hex 24)
# 2) config.json 的 "api_key" 欄位
# 3) docker compose 的 .env
```

### 帳號續期

`refresh_token` 與登入時的 `client_id`、DPoP 私鑰綁定，因此憑證檔會儲存
`client_id` 與 `dpop_private_key`；刷新時原樣重用並寫回輪替後的新 refresh_token。
若手動換過 `login_client_id`，老帳號仍依自己記錄的 client_id 刷新。

### 每日簽到與新手點數

服務預設在啟動和每次 watchdog 輪詢時檢查活動，自動領取每日簽到點數及
上游回傳的可領取新使用者點數。新增帳號會在下一次輪詢處理；也可在 WebUI
點擊「簽到」／「全員簽到」，或經鑑權呼叫 `POST /admin/api/checkin`（可傳 `{"uid":"帳號 ID"}`）。
領取後必須確認；已領取未確認時只補確認，已確認不重複領取。資格、金額、
每日刷新和活動起止時間均以上游回傳為準，失敗會在下一次輪詢重試，
不會因活動介面故障冷卻或停用推理帳號。

`credits_auto_claim: false` 或 `CA2A_CREDITS_AUTO_CLAIM=0` 可關閉自動領取，
`watch.enabled: false` 會關閉整個輪詢。手動簽到仍可使用。
這項活動與 `benefit_auto_claim` 的模型 Token 福利獨立；面板顯示最近領取結果，
不把領取量或健康帳號數當作點數餘額。`credit.sh` 查登入態，`apply.sh` 僅續期。

目前[官方每日簽到活動](https://support.huaweicloud.com/offers-codeartsagent/codeartsagent_offers_0004.html)
為已升級點數計費的使用者每日發放 1000 點數，30 天有效，活動截止於
2026-12-31 00:00（UTC+8）；[新使用者註冊獎勵](https://support.huaweicloud.com/price-codeartsagent/codeartsagent_billing_0029.html)
為 4000 點數。無資格或已自動發放的新手獎勵不會再次領取。

## 部署與升級

部署方式有兩種：**預設使用已發布映像**（無需本機建置），或在原始碼目錄**本機建置**。
兩種方式共用同一份 `docker-compose.yml` 變數與掛載；本機建置用疊加檔案
`docker-compose.build.yml` 覆寫映像來源。

需求：Docker Engine 24+ 與 Docker Compose v2（`docker compose`）。

### 方式一：預設已發布映像（建議）

```bash
# 僅首次部署複製範例；升級時不要覆寫既有檔案
cp -n .env.example .env
cp -n config.example.json config.json
mkdir -p auths data

# 編輯 .env：至少設定 CA2A_API_KEY；如需更換連接埠設定 CA2A_PORT（預設 7866）
docker compose config --quiet   # 只做本機語法／插值檢查
docker compose pull
docker compose up -d            # 標準部署：應用 + 自動更新
```

標準部署命令會同時啟動應用容器與 Watchtower 更新器（預設啟用）。只想手動部署、不要
自動更新時用 `docker compose up -d codearts2api`：只啟動應用服務，不啟動更新器。

預設映像為 `ghcr.io/azurekiln2333/codearts2api:latest`，與儲存庫實際 origin
（`github.com/azurekiln2333/codearts2api`）一致。可在 `.env` 用 `CA2A_IMAGE`
切換到其他映像或固定版本（見下文「映像切換與固定版本」）。

> **首次發布的邊界**：`ghcr.io/azurekiln2333/codearts2api:latest` 只有在推送第一個
> `v*` 版本標籤、CI 成功發布後才會存在。在此之前 `docker compose pull` 會因
> manifest 不存在而失敗，這是預期行為，並非本機設定錯誤；此時請改用方式二本機建置。
> GHCR 套件**首次推送預設是 private**：要在 GitHub 套件設定裡改為 public，或先執行
> `docker login ghcr.io`（憑證需含 `read:packages`）再拉取。

升級：保留既有 `.env`、`config.json`、`auths/` 與 `data/`，然後

```bash
docker compose pull
docker compose up -d
```

### 方式二：本機原始碼建置

```bash
# 疊加本機建置檔案：使用本機映像、pull_policy: build，並停用自動更新目標
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

疊加檔案把應用映像固定為本機 `codearts2api:local`（可用 `CA2A_LOCAL_IMAGE` 覆寫），Watchtower
不會把本機建置的容器替換成已發布映像。要標記建置身分（後台「執行設定」會顯示實際提交）：

```bash
CA2A_BUILD_COMMIT="$(git rev-parse HEAD)" \
CA2A_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

`CA2A_BUILD_CONTEXT` 可覆寫建置上下文。原始碼更新後必須重新 `up -d --build`；重啟舊容器
不會替換舊映像。疊加建置 overlay 時更新器預設不啟動（保留 `auto-update` profile）；
即使明確加上 `--profile auto-update`，應用容器的 `...enable` 標籤為 `false`，也不會被自動替換。

### 自動更新（Watchtower，標準部署啟用，可明確關閉）

標準部署命令 `docker compose up -d` 會同時啟動一個**專案內隔離**的 Watchtower，自動檢查
並更新本專案的 `codearts2api` 容器。Compose 預設啟動更新器；要 opt-out 時只啟動應用
服務：

```bash
# 標準部署：應用 + 自動更新
docker compose up -d

# 不要自動更新：只啟動應用（更新器不會執行）
docker compose up -d codearts2api
```

Watchtower 行為：

- 映像固定為維護分支 `nickfedor/watchtower:1.23.2`（原 `containrrr/watchtower`
  已歸檔不再維護；Docker Hub 命名空間為 `nickfedor`，GitHub 儲存庫為
  `nicholas-fedor/watchtower`）。
- **作用域限定**：同時啟用 `--label-enable` 與 scope。只有帶
  `com.centurylinklabs.watchtower.enable: "true"`（本專案的 `codearts2api` 服務）
  且 `com.centurylinklabs.watchtower.scope` 等於本專案名的容器才會被更新。
  scope 預設取 `COMPOSE_PROJECT_NAME`，回退 `codearts2api`；因此不會觸碰其他專案、
  其他堆疊或無關容器。
- Watchtower 自身標籤 `...enable: "false"`，永不自我更新。
- 需要掛載 `/var/run/docker.sock`（Docker 控制通道）；這是自動更新容器映像的
  必要權限，不會改寫 `.env`、`config.json` 或憑證。
- 可設定項（寫入 `.env`）：
  - `WATCHTOWER_POLL_INTERVAL`：檢查間隔（秒），預設 `600`。
  - `CA2A_WATCHTOWER_CONTAINER_NAME`：更新器容器名，預設 `codearts2api-watchtower`。
  - 更新後清理舊映像（`WATCHTOWER_CLEANUP: "true"`）在 YAML 中固定開啟，未暴露為 `.env` 變數。
- 保留的 `.env`、`config.json`、`auths/`、`data/` 掛載在更新重建容器後不變。

**關閉已啟用的自動更新：**

```bash
# 只停用／移除更新器，應用容器不受影響
docker compose stop watchtower
docker compose rm -f watchtower
```

注意：`docker compose up -d codearts2api` 只啟動應用，**不會停掉**已在執行的更新器
（Compose 不會清理未列出的服務），要停止請用上面兩條命令。更新器只能拉取**已發布**的
映像；首個 `v*` 標籤發布前沒有可拉取的映像，請用本機建置（方式二）。本機建置方式
（方式二）不啟動更新器，也不會更新本機建置的容器。

#### 私有倉庫映像的更新器鑑權

主機上的 `docker login` 只寫入主機自己的 `~/.docker/config.json`，**不會自動傳給
Watchtower 容器**。更新器預設只掛載 Docker socket，沒有 registry 憑證：公開映像（例如
已設為 public 的 GHCR 套件）不需要憑證；私有 GHCR/CNB 映像若要讓自動更新真正可用，需給
更新器提供**唯讀**的 Docker config。

新建可選的 `docker-compose.override.yml`（使用預設檔案鏈時 Compose 會自動合併；用 `-f`
明確列出多個檔案時，需再追加 `-f docker-compose.override.yml`）：

```yaml
services:
  watchtower:
    environment:
      DOCKER_CONFIG: /config
    volumes:
      - ${HOME}/.docker/config.json:/config/config.json:ro
```

儲存庫**預設不掛載**該檔案，因此公開映像的部署不會因缺少本機私密檔案而失敗；這是可選的
opt-in。不要把 token 寫進 YAML 或日誌，登入一律用 `--password-stdin`。更新器重建容器時
沿用原掛載（`.env`、`config.json`、`auths/`、`data/`），不需要重新匯入憑證。

若 `~/.docker/config.json` 使用 `credsStore`/`credHelpers`（例如 Docker Desktop），更新器
容器內沒有對應 helper，鑑權會失敗；此時改用手工 `auths` 寫法的 `config.json`
（官方文件提供 `auth` 的 base64 產生方式），或把 helper 一併掛載。

### 映像切換與固定版本（GHCR / CNB / 映像源）

`CA2A_IMAGE` 決定拉取的映像，預設 GHCR。寫入 `.env` 後 `docker compose pull && up -d`
生效：

```bash
# 固定到某個版本（建議生產回滾使用）
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.3

# 切換到 CNB 製品庫；slug 是動態變數，請填你的實際儲存庫路徑（小寫）
CA2A_IMAGE=docker.cnb.cool/<CNB_REPO_SLUG_LOWERCASE>:latest
```

**架構中間標籤**：CNB 流水線做多架構組裝時會先建置並推送兩個**按架構的中間標籤**
`vX.Y.Z-amd64`、`vX.Y.Z-arm64`，再合成多架構的 `vX.Y.Z`（stable 額外加
`latest`）。這些 `-amd64` / `-arm64` 標籤由 CNB 流水線產生，保留供除錯與單架構
排查使用；**常規部署不要用它們**，繼續用多架構的 `vX.Y.Z` 或 `latest`，Compose 與
Watchtower 會自動取得符合宿主架構的映像。

CNB 製品庫登入（`CNB_TOKEN` 由 CNB 流水線簽發，本機可用存取權杖代替；用標準輸入傳密碼，
避免把權杖寫進命令列歷史或日誌）：

```bash
printf '%s' "$CNB_TOKEN" | docker login docker.cnb.cool -u cnb --password-stdin
```

本儲存庫未宣告固定 CNB slug；文件中的 `<CNB_REPO_SLUG_LOWERCASE>` 是佔位符，
請勿把它當成已存在的公開儲存庫。

### 版本標籤、自動發布與權限（v*）

推送一個 `v*` 版本標籤（如 `v1.2.3`）會觸發受信任的發布流程：

1. **測試門檻**：先跑 Go `test`/`race`/`vet`/`build` 與 Python 發布工具測試；
   測試不通過不發布。
2. **發布映像**：發布 `ghcr.io/azurekiln2333/codearts2api:v1.2.3`；穩定版
   （無 `-` 預發布後綴）**同時**更新 `latest`。
3. **原始碼 Release**：建立對應 GitHub Release，附明確清單匯出的原始碼 ZIP 與
   sha256 校驗檔（`scripts/release-source.py`）。
4. **預發布**：`v1.2.3-rc.1` 之類只標記為 prerelease，**不會覆寫 `latest`**。
5. **Pull Request** 只做校驗，絕不鑑權或發布；工作流程使用最小權限
   （`contents: read`，發布時才 `packages: write` / `contents: write`）。

CNB 側使用同樣規則：`main`/`pull_request` 只校驗，`v*` 標籤才發布原始碼 Release
與映像。CI 需要平台簽發的臨時權杖（`CNB_TOKEN`），不寫入任何真實憑證。

> 以上描述的是**儲存庫內工作流程的行為**；在你推送標籤並讓遠端流水線執行之前，
> 映像與 Release 都不存在。本文件不聲稱任何遠端流程已經執行。

CNB 預設使用平台儲存庫路徑與登入，無須覆寫登錄檔變數。自訂發布目標請在 **CNB 儲存庫 CI 變數**中設定 `CNB_IMAGE`（不含標籤的映像儲存庫路徑）、`CNB_REGISTRY`、`CNB_REGISTRY_USER`、`CNB_REGISTRY_PASSWORD`；外部登錄檔必須提供密碼。優先使用 `CNB_IMAGE`，避免與伺服器 `.env` 中帶標籤或摘要的部署變數 `CA2A_IMAGE` 混淆。發布標籤僅支援 `v主版本.次版本.修補版本[-預發布]`，不支援 `+build` 中繼資料；長度還須容納架構後綴。

### 回滾

映像更新可獨立回退，資料與憑證格式是增量的，無需遷移：

```bash
# 記錄目前執行的映像 ID 以便精確回退
docker inspect --format '{{.Image}}' codearts2api

# 回退到固定版本或摘要
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose pull
CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api:v1.2.2 docker compose up -d
# 或依摘要：CA2A_IMAGE=ghcr.io/azurekiln2333/codearts2api@sha256:<digest>
```

回退時**保留同一組掛載目錄**（`.env`、`config.json`、`auths/`、`data/`），不要切到空目錄。
如需停止自動更新以免回退後又被升級，先依上文停用 Watchtower 再回退。

### 保留的掛載與憑證（升級安全）

Compose 透過 `env_file: .env` 直接讀取應用變數；連接埠只填 `CA2A_PORT`（預設 `7866`），
它同時控制主機連接埠、容器連接埠與服務監聽，Compose 自動產生 `CA2A_LISTEN`。
主機掛載路徑使用 `CA2A_AUTHS_PATH`、`CA2A_DATA_PATH`、`CA2A_CONFIG_PATH`；`CA2A_AUTH_DIR`
與 `CA2A_STATE_FILE` 則是容器內應用路徑。升級時保持原掛載路徑即可繼續使用已加入的憑證。

YAML 只保留 API Key 必填校驗、時區預設值和衍生監聽位址這三個 `environment` 例外，
其餘應用項直接寫入 `.env`；非空值覆寫 `config.json`，未設定或留空則沿用設定檔及內建預設值。
可選應用變數僅在 shell 中 `export` 不會自動透傳到容器。`.env` 檔必須存在；CLI 的
`--env-file` 只更換 Compose 插值來源，不會更換 YAML 中固定的 `env_file: .env`。

升級前備份既有 `.env`、`config.json`、`auths/` 和 `data/`，記錄正在使用的映像 ID；
保留原目錄和掛載路徑。升級只重建容器，不要求重新匯入憑證。若先前在 `config.json`
明確設定過併發 `5`，該值仍然生效；初次驗證上游額度時可在 `.env` 明確設定
`CA2A_MAX_CONCURRENT=1`。

### systemd 部署（無 Docker）

```bash
sudo mkdir -p /opt/codearts2api && sudo cp -r bin config.example.json auths /opt/codearts2api/
sudo cp deploy/codearts2api.service /etc/systemd/system/
# 編輯 /opt/codearts2api/.env 寫 CA2A_API_KEY，改好 config.json
sudo systemctl daemon-reload && sudo systemctl enable --now codearts2api
```

### 完整原始碼包與部署核對

完成程式碼提交後，可用 Python 3.9+ 產生完整原始碼包；僅從指定 Git 提交匯出產品清單，
包含 Compose、建置疊加檔、Dockerfile、範例設定、Go 原始碼、CI／發布輸入和校驗腳本，
排除真實 `.env`、`config.json`、憑證、資料和工作區工具。相同提交產生相同 ZIP，
不覆寫既有檔案：

```bash
python3 scripts/release-source.py --commit HEAD
```

產物為 `codearts2api-source-<提交前12位>.zip`，附 `release.json` 檔案校驗和及僅含
原始碼提交的 `release.env`。原始碼包不包含 `.git`；解壓後在原部署目錄保留既有掛載路徑，
依包內身分本機建置：

```bash
set -a
. ./release.env
set +a
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

部署核對使用環境變數提供密鑰，僅送出 GET：檢查健康、實際提交、清理後的主控台及
稽核／模型／連線介面；舊頁面、404、401、身分未知或提交不符都會回傳非零狀態。
反向代理有路徑前綴時將其寫入 `--url`。以下網域替換為自己的服務位址：

```bash
read -r -s -p "服務 API Key: " CA2A_API_KEY
export CA2A_API_KEY
python3 scripts/verify-deployment.py --url https://gateway.example.com --expected-commit "$CA2A_BUILD_COMMIT"
unset CA2A_API_KEY
```

此校驗不會發起聊天、模型探測或重新拉取；稽核被設定關閉時會明確提示。它驗證交付的
版本和介面，模型實際權益需結合來源結果及真實呼叫判斷。

更完整的拉取／升級／自動更新／私有儲存庫登入／回滾說明見 [docs/deployment.md](docs/deployment.md)（簡體中文擴充手冊）。

## 請求稽核與帳號狀態

後台的「請求稽核」顯示最近請求的模型、選中帳號、HTTP 狀態與執行結果、安全供應商錯誤碼、嘗試次數，以及排隊／首資料／總耗時。回應標頭 `X-Request-Id` 可關聯服務日誌和稽核記錄。`GET /admin/api/requests` 與 `GET /admin/api/audit` 使用現有 Bearer Key 鑑權；請求清單支援 `limit`（最多 200）、`before`（序號游標）、`model`、`account`、`outcome`、`status`、`since` 過濾。

預設稽核保留 7 天，最多 8 個 8 MiB 檔案（含目前檔案，合計不超過 64 MiB），入列上限 256，近期視窗 512 筆。重啟會恢復近期記錄和序號；新請求可能讓視窗中最舊的記錄退出分頁。更早的保留檔案位於 `<state_file 所在目錄>/audit/`，供本機歸檔查看。佇列滿或磁碟寫入失敗不會等待寫盤，後台會顯示丟棄與寫入失敗計數。稽核僅儲存中繼資料，不儲存提示詞、回覆、工具參數、請求標頭、密鑰或登入憑證；共用服務 Key 無法區分 New API 的具體終端使用者。

「連線日誌」在請求尚未結束時顯示目前階段、階段／總耗時、帳號、嘗試次數及實際最後上游讀取進展，可區分等待帳號、等待憑證刷新、等待上游回應標頭、等待首個 SSE 資料、轉發／彙總和寫回。首個 SSE 資料不是首個生成 token，長串流耗時大也不代表無進展。頁面僅在登入且位於前景時約每 2 秒讀取 `GET /admin/api/connections`，離開頁面、切背景或登出即停止。

福利目錄會先查詢該帳號的福利通道開關。上游明確回傳關閉時，面板顯示「福利通道已關閉」，跳過舊福利領取和目錄請求，並撤銷該帳號舊福利路由；模型以 Agent／內建目錄實際回傳的識別為準。開關查詢失敗或格式不明時保留原有證據並顯示失敗，不會猜測所有帳號都已關閉。HTTP 成功但福利業務碼非成功值，會單獨顯示業務錯誤，不再統稱 HTTP 錯誤；業務碼 `4000` 也不會單獨被當作全域關閉。

活動追蹤最多 512 個請求，每個請求最多 32 個階段；超限和截斷會顯示累計計數。結束後的記錄重用稽核近期視窗，舊版記錄沒有階段歷史時明確顯示不可用。關閉請求稽核也會關閉連線追蹤。

帳號的「可調度／冷卻／停用」與歷史失敗分別展示。`err N` 是連續失敗計數，預設達到 3 次冷卻 10 分鐘；冷卻自然到期不會自動清除歷史計數。實際請求成功後會清理已過時的目前錯誤並持久化一次，保留歷史失敗，不會撤銷目前冷卻或人工停用。升級前的失敗時間無法恢復，顯示為「時間未知」。簽到領取點數與具體模型的福利資格不同，`benefit not found` 仍需要核對該模型的權益。

## 環境變數設定

除了 `config.json`，還支援以下環境變數覆寫：

| 變數名 | 說明 | 預設值 |
|--------|------|--------|
| `CA2A_API_KEY` | API 存取密鑰 | - |
| `CA2A_LISTEN` | 監聽位址 | `:7866` |
| `CA2A_AUTH_DIR` | 憑證目錄 | `./auths` |
| `CA2A_STATE_FILE` | 狀態檔 | `./data/state.json` |
| `CA2A_DEFAULT_MODEL` | 預設模型 | `glm-5.2` |
| `CA2A_OAUTH_CALLBACK_HOST` | OAuth 回呼連接埠覆寫（舊變數名；僅明確連接埠生效，回呼主機仍為瀏覽器自身的 `127.0.0.1`） | 跟隨服務連接埠 |
| `CA2A_WATCH_ENABLED` | 調度器開關 | `true` |
| `CA2A_WATCH_POLL_MINUTES` | 輪詢間隔（分鐘） | `30` |
| `CA2A_WATCH_REFRESH_SKEW` | 提前刷新時間（分鐘） | `30` |
| `CA2A_WATCH_KEEPALIVE_INTERVAL` | 保活間隔（分鐘） | `15` |
| `CA2A_MAX_CONCURRENT` | 單帳號最大活躍請求數 | `1` |
| `CA2A_MAX_PENDING` | 行程內等待上限（另限制總在途數為帳號容量加此值） | `64` |
| `CA2A_STARTUP_TIMEOUT_SECONDS` | 讀取請求至上游首資料總預算（秒） | `90` |
| `CA2A_ACCOUNT_WAIT_SECONDS` | 每請求累計帳號槽位等待預算（秒） | `10` |
| `CA2A_UPSTREAM_TIMEOUT_SECONDS` | 上游回應標頭／首資料／讀取閒置逾時（秒） | `60` |
| `CA2A_STREAM_WRITE_TIMEOUT_SECONDS` | 下游單次寫入／flush 逾時（秒） | `30` |
| `CA2A_QUEUE_RETRY_SECONDS` | 上游排隊／TPM 重試間隔（秒） | `2` |
| `CA2A_QUEUE_MAX_ATTEMPTS` | 每帳號總嘗試次數（含首次） | `3` |
| `CA2A_KEEPALIVE_WINDOW` | 保活視窗 | `10m` |
| `CA2A_BENEFIT_AUTO_CLAIM` | 福利通道明確啟用時自動領取（冪等；關閉後不主動領取） | `true` |
| `CA2A_CREDITS_AUTO_CLAIM` | watchdog 自動領取每日簽到及新使用者點數 | `true` |
| `CA2A_LOGIN_CLIENT_ID` | WebUI 登入使用的 OAuth client_id | 既有帳號的取值，否則 `codearts-agent` |
| `CA2A_AUDIT_ENABLED` | 開啟請求中繼資料稽核 | `true` |
| `CA2A_AUDIT_DIR` | 容器內稽核目錄 | `<state_file 所在目錄>/audit` |
| `CA2A_AUDIT_MAX_QUEUE` | 非阻塞稽核佇列容量 | `256` |
| `CA2A_AUDIT_RECENT` | 可查詢的近期記錄數 | `512` |
| `CA2A_AUDIT_MAX_FILE_BYTES` | 單個稽核檔案上限（位元組） | `8388608` |
| `CA2A_AUDIT_MAX_FILES` | 檔案數量上限（含目前檔案） | `8` |
| `CA2A_AUDIT_MAX_TOTAL_BYTES` | 所有稽核檔案合計上限（位元組） | `67108864` |
| `CA2A_AUDIT_RETENTION_DAYS` | 稽核檔案保留天數 | `7` |
| `CA2A_AUDIT_FLUSH_INTERVAL` | 稽核檔案同步週期 | `1s` |

部署相關變數（Compose 插值）另見 `.env.example`：`CA2A_PORT`、`TZ`、
`CA2A_CONTAINER_NAME`、`CA2A_RESTART_POLICY`、`CA2A_AUTHS_PATH`、`CA2A_DATA_PATH`、
`CA2A_CONFIG_PATH`、`CA2A_IMAGE`、`CA2A_WATCHTOWER_CONTAINER_NAME`、`WATCHTOWER_POLL_INTERVAL`，
以及僅本機建置用的 `CA2A_BUILD_CONTEXT`、`CA2A_BUILD_COMMIT`、`CA2A_BUILD_TIME`、`CA2A_LOCAL_IMAGE`。

## 目錄結構

```
cmd/server/        HTTP 服務（config + main）
cmd/login/         華為雲 OAuth2 PKCE 登入
cmd/credit/        帳號登入態日報（新增併發資訊）
cmd/apply/         批次 token 續期（使用 pool 套件）
cmd/models/        檢視帳號可用模型（內建 + 限時福利，可選 -claim 領取）
internal/auth/     auth 檔案讀寫
internal/upstream/ 雲端用戶端（登入／聊天／SSE／模型發現）+ 逆向常數
internal/pool/     帳號池（token 校驗／自動刷新／冷卻／併發控制）
internal/scheduler/ token 續期看門狗（新增保活機制）
internal/server/   OpenAI 相容路由
internal/audit/    有界請求稽核與活動連線追蹤
internal/telemetry/ 請求階段與讀取進展觀察器
scripts/           完整原始碼打包、發布中繼資料與只讀部署核對
deploy/            systemd unit 範例
docs/              逆向過程、介面清單與部署說明
```

## 免責聲明

僅供學習與研究使用。使用者需遵守華為雲服務條款，自行承擔使用風險。

## License

MIT
