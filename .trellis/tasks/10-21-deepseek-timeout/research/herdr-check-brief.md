Active task: .trellis/tasks/10-21-deepseek-timeout

你是本任务的 trellis-check 复核执行者，由主协调 Codex 按用户明确要求通过 Herdr 委派给同项目工作区现有 Pi。直接检查、修复和验证；不要再递归委派 implement/check，不要新建任务、重新规划审批、提交、推送、部署或请求真实上游做诊断。

工作目录 D:\My2KProjects\codearts2api；分支 fix/deepseek-concurrency-20261021；baseline 8d1e43d。现有实现已完成并冻结源码，原 server worker 仅做最后只读验证。你不是唯一协作者，不可还原别人的修改。父协调负责任务/spec 文档及全量 race；你负责本次任务相关产品代码的完整审查及有证据的局部修复和回归。保留所有预先存在的未跟踪 .agents/.claude/.codex/.cursor/.gitattributes/.pi/.trellis/AGENTS.md，不要 git add 整个目录。

先读 check.jsonl 的实际上下文，再读 prd.md、design.md、implement.md、research/contracts.md，以及 .trellis/spec/backend/index.md。注意 git diff 不包含新增未跟踪源码，请同时检查 internal/pool/admission.go、refresh.go、internal/server/chat.go、lifecycle.go、所有 lifecycle/admission/refresh 测试，以及 internal/upstream/stream_body.go。列出所有受影响包并按 trellis-check 检查全范围。

用户原问题：DeepSeek Flash 经 New API 起初能用，随后 120s+ 超时、网关卡住；明确要求多用户并发、账号调度和过载保护。已复现并修正终止 SSE 后仍等 EOF、流体/预检超时失效、180s 首账号等待、无限重试堆积、取消误罚账号与无所有权释放。重点审查 AC1-AC9，尤其全生命周期 ticket 与 account lease 是否跨重试/取消/刷新/重载正确释放、多个 handler 共享上限、429 retry sleepers 是否有界、公平性、第一轮相同 prompt 不隐式粘号、真实历史/显式 affinity 保持、模型能力缓存/换号、慢下游写、半截请求体、活跃长流及终止 usage/tools。

当前合同：startup 90s（预检成功后停止，但保留客户端取消）；累计 account wait 10s；upstream 空闲/headers 60s；stream write 30s；每账号并发默认 1；MaxPending 64；队列每账号总尝试默认 3、延时 2s，单次 401 refresh 可额外重试。整个 handler 限额为 enabled account capacity + MaxPending。未知目录账号的成功 availability 不得晋升其 priority 导致其他未知账号饿死。所有网络边界测试仅 localhost/fake auth。

临时 Go：D:\AppData\Local\Temp\codearts2api-toolchain-go1.27.3\go\bin\go.exe。保持模块 Go 1.22 兼容。原版全套测试基线通过；各 worker 的上游、pool/scheduler、server/cmd/server 测试及局部 vet/build 已通过。父协调会统一跑最终 full tests/race/vet/build，避免重复负载；你应运行证明修复所需的针对性回归，若发现全局检查必要请自行运行并报告实际命令/结果。

已有并发回归：pool 3账号/6槽/8等待/12立即拒绝；server 两 handler 共享 2账号/2槽/2pending，24超量 HTTP 快速429，health<300ms，取消 drain 后公平复用；429 sleep wave 1+2 tickets 后20请求拒绝；真实 HTTP 半截请求体预算返回504；持续165ms流跨 startup50/idle75ms；取消不轮转/误罚；失败重入不释放其他 owner。

请现在开始复核，最终用中文报告：已修问题及证据（文件/测试）、执行的验证、仍有的具体风险或无阻断发现。不要停在建议或能力说明。主协调会通过 herdr agent get/read 观察进展。
