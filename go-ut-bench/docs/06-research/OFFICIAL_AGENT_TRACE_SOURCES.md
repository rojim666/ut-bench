# 三类 Agent 官方 Trace 能力调研

本文档记录 UT-Bench 当前接入的三类 CLI Agent 在官方文档中可用于采集完整执行轨迹的信息来源，并说明本仓库的适配策略。这里的重点不是排名指标，而是回答一个更底层的问题：我们能否拿到足够完整的 step-by-step trajectory，用于后续 AI 诊断和 skill/agent 优化。

## 1. 结论摘要

| Agent | 官方可用能力 | 最推荐的原始信息来源 | UT-Bench 适配方式 | 完整性判断 |
| --- | --- | --- | --- | --- |
| Claude Code | `-p/--print`、`--output-format stream-json`、`--verbose`、`--include-partial-messages` | `stream-json` stdout，必要时加 verbose/partial 事件 | 保存完整 stdout/stderr，解析 JSONL 为统一 trajectory | 较好，可以拿到 turn/tool/result 级事件 |
| CodeBuddy | `-p/--print`、`--output-format stream-json`、`--input-format stream-json`、`--verbose` | `stream-json` stdout | 与 Claude Code 共用 stream-json parser，并保留 raw event | 较好，官方说明每条消息都是独立 JSON 对象 |
| OpenCode | `opencode run --format json`、`opencode export [sessionID]`、`opencode session list --format json` | session export JSON | 优先导出 session JSON，再解析 message/tool_use/tool_result | 较好，但依赖 session id 捕获和 export 成功 |

短期判断：旧版 `AgentTrace` 只能算摘要，不够完整；新版必须同时保留 raw stdout/stderr、raw trace JSONL 和统一 `trajectory.json`。

## 2. Claude Code 官方信息

官方文档入口：

- [Claude Code CLI reference](https://docs.claude.com/en/docs/claude-code/cli-reference)

官方文档中与 trace 采集相关的能力：

- `claude -p "query"`：以 print/headless 方式执行一次查询，适合自动化。
- `--output-format`：print 模式输出格式，支持 `text`、`json`、`stream-json`。
- `--input-format`：输入格式，支持 `text`、`stream-json`。
- `--include-partial-messages`：在 `--output-format=stream-json` 时包含部分流式事件。
- `--verbose`：启用详细日志，官方说明可显示完整 turn-by-turn output。
- `--max-turns`：限制非交互模式中的 agentic turns。
- `--resume` / `--continue`：恢复或继续已有会话。

建议命令形态：

```bash
claude -p "<prompt>" \
  --output-format stream-json \
  --verbose \
  --include-partial-messages
```

UT-Bench 适配要求：

- stdout/stderr 必须完整保存为 `*.stdout.log` 和 `*.stderr.log`。
- stdout/stderr 中的 JSON 行按行包装为 `*.raw_trace.jsonl`。
- 从 JSON event 中解析：
  - assistant/user message
  - `tool_use`
  - `tool_result`
  - result/usage/session 相关事件
- 不能识别的事件保留为统一 trajectory 的 `raw_event`，避免 Claude Code 后续升级字段时丢信息。

## 3. CodeBuddy 官方信息

官方文档入口：

- [CodeBuddy Headless Mode](https://www.codebuddy.ai/docs/cli/headless)
- [CodeBuddy CLI Reference](https://www.codebuddy.ai/docs/cli/reference)

官方文档中与 trace 采集相关的能力：

- `codebuddy -p "query"` / `cbc -p "query"`：非交互执行。
- `--output-format`：支持 `text`、`json`、`stream-json`。
- `--input-format stream-json`：支持 JSONL 多轮输入。
- `--verbose`：启用详细日志。
- `--resume` / `--continue`：恢复或继续会话。
- 官方 headless 文档说明 stream-json 会在每条消息到达时输出；会话从 `init` system message 开始，随后是 user/assistant messages，最后以包含统计信息的 `result` system message 结束。
- 非交互执行需要注意 `-y` 或 `--dangerously-skip-permissions`，否则文件读写、命令执行、网络请求等需要授权的操作可能被阻断。

建议命令形态：

```bash
codebuddy -p "<prompt>" \
  --output-format stream-json \
  --verbose \
  --dangerously-skip-permissions
```

UT-Bench 适配要求：

- 与 Claude Code 共用 stream-json parser。
- 完整保存 stdout/stderr，不只保留摘要。
- 解析 message、tool_call、tool_result、result/usage。
- CodeBuddy 官方 `result` 中的统计信息应保留在 raw event 中；已知字段再提升为 token/cost/session 等结构化字段。

## 4. OpenCode 官方信息

官方文档入口：

- [OpenCode CLI](https://opencode.ai/docs/cli/)

官方文档中与 trace 采集相关的能力：

- `opencode run [message..]`：非交互执行 prompt，适合脚本和自动化。
- `opencode run --format json`：run 命令支持 raw JSON events 输出。
- `opencode run --session <sessionID>` / `--continue`：继续指定或最近会话。
- `opencode session list --format json`：列出 session。
- `opencode export [sessionID]`：导出 session data as JSON。
- `opencode export --sanitize`：导出时脱敏 transcript/file data。
- `opencode serve`：启动 headless server，提供 HTTP API。

建议命令形态：

```bash
opencode run "<prompt>" \
  --format json \
  --dangerously-skip-permissions
```

执行结束后，如果已捕获 session id：

```bash
opencode export <sessionID>
```

UT-Bench 适配要求：

- 优先从 stdout/stderr 捕获 session id。
- 执行结束后尝试 `opencode export <sessionID>`，保存 session JSON。
- 统一 trajectory 优先从 session export 解析。
- 如果 session export 失败，再退回解析 stdout/stderr 文本日志。
- `trajectory.json` 中必须保留 `session_export_path` 和失败原因，方便后续人工排查。

## 5. 仓库统一产物约定

每个 agent 样本执行后，trace 目录下应尽量保存：

```text
agent_traces/<subject>/<language>/<sample>.trace.jsonl
agent_traces/<subject>/<language>/<sample>.raw_trace.jsonl
agent_traces/<subject>/<language>/<sample>.stdout.log
agent_traces/<subject>/<language>/<sample>.stderr.log
agent_traces/<subject>/<language>/<sample>.trajectory.json
agent_traces/<subject>/<language>/<sample>.diff.json
```

各文件定位：

- `trace.jsonl`：UT-Bench 摘要型 trace，便于报告快速读取。
- `raw_trace.jsonl`：stdout/stderr 按行包装后的原始事件流，尽量不丢官方字段。
- `stdout.log` / `stderr.log`：官方 CLI 原始输出，便于二次解析。
- `trajectory.json`：统一 step-by-step trajectory，供 analyzer、Web UI 和 LLM 诊断使用。
- `diff.json`：工作区变更，用于判断 agent 是否修改了不该改的文件。

统一 trajectory 的第一版 step 类型：

| kind | 含义 |
| --- | --- |
| `message` | 用户、assistant 或系统消息 |
| `tool_call` | 工具调用，例如 Read/Edit/Bash 等 |
| `tool_result` | 工具返回结果或 observation |
| `command` | 从日志中解析出的 shell 命令 |
| `raw_event` | parser 暂时不能理解但需要完整保留的官方事件 |

## 6. 后续适配检查清单

- Claude Code / CodeBuddy 的启动命令是否真实启用了 `stream-json`。
- Claude Code 是否启用了 `--verbose`，必要时是否启用 `--include-partial-messages`。
- CodeBuddy 非交互执行是否设置了权限参数，避免工具调用被阻断。
- OpenCode 是否能稳定拿到 session id。
- OpenCode session export 是否保存到 trace 目录，并写入 `session_export_path`。
- manifest 和 evaluation 是否都包含 `raw_trace_path`、`trajectory_path`。
- analyzer 后续只读 `trajectory.json` 做跨 agent 对比，必要时再回看 raw 文件。
