# 原生设备工具桥（实验性，未线上验收）

2026-10-13：已从 MacBook 安装的 Doubao 2.31.4 的 `biz.pak` 提取后台实际实现。
`native_tools.py` 支持原生设备帧到 Responses function_call 的转换及结果载荷构造。
`native_client.py` 已接入 `server.py`，在显式原生会话中支持设备 SSE → Responses function_call → 原生结果上传 → 同一生成的最终回答。此链路已通过断网容器中的合成上游与 HTTP 测试，尚未部署或通过豆包线上验收。一次先前的受控在线验证确认了自定义工具定义对模型可见，但设备流未收到调用，回传与生成续接未通过。

## 新证据

原始包位于 MacBook 的
`/Applications/doubao.app/Contents/Helpers/Doubao Browser.app/Contents/Frameworks/Doubao Browser Framework.framework/Versions/147.0.7727.149/Resources/local_webcontents/biz/biz.pak`。
离线副本及解压资源位于 `/root/.codex/tmp/doubao-re/artifacts/`。

| 材料 | SHA-256 |
| --- | --- |
| biz.pak | 1ccc272bd69ab0cb0b9f465cb96a34206e4a6763b6f8f87fdf9ae00c62a3c677 |
| pak_selected/9034.js | 45fa62247f1a8e2fb8c4f1f99f83d4bdf4fdb4867f108cbe4e636afb156e2bee |
| pak_selected/7035.js | 9dc81c270321a712df356aaa38249694dbfb5b5d16c36e7d0c0e142aa6f1615a |

9034.js 的字符偏移（Python 解码后索引）：

- 410350：设备事件类型 1=TOOL_CALL、2=COMMAND、3=HEARTBEAT。
- 415324 附近：POST SSE，body 为 device_id、client_attrs；有路由 sandbox 时 client_attrs.sandbox_id 被设置。
- 749000 附近：JSON 帧解析，tool_call.id/name/input/option；input 是 JSON 字符串时解码，原始 input 另行保留。
- 404419：实际调用 AGWUploadToolCallResultV1；7035.js 将它映射到 `/alice/office/tool_local/upload_tool_call_result`，不能仅凭接口名称选 v2。
- 405670 附近：结果包含 id/name/content/isError，另有可选 structured_result、workspace_tool_result 和错误字段。
- 410800 附近：signature 被转成 signedEnvelope，包含设备、用户、key_id、时间戳、工具 ID、名称和原始输入。此证据支持签名传递，不能推出“整个设备事件必须解密”。

COT/UI 展示事件与此通道分离，不用 COT_EVENT_TOOL_CALL_ARGS 模拟设备协议。
pak_selected/8728.js 可见旧任务 client_tool_key 被放入消息 content，但尚未找到并验证它与当前生成入口的闭环。

## 已实现与限制

`NativeToolExchange` 按设备/sandbox 隔离，保留上游 call_id 和参数字符串；重复相同帧不再次派发，冲突帧、未知工具、跨 sandbox、签名设备不匹配和未知结果 ID 会拒绝。
结果可重复构造以供未来受控传输使用，冲突结果拒绝；构造结果不等于上游确认成功。

这是严格的协议子集：只接受数字事件枚举及对象函数参数。支持 localConnectors 工具定义编码、connector.call 包装解码及原始 MCP 响应载荷。`NativeSandboxCommands` 单独解析 sandbox connect/disconnect/release 指令，按调用方显式提供的操作结果构造回执；它不执行生命周期操作，也不自动确认成功。`native_tools.py` 本身不连接网络；新增传输与 HTTP 入口在 `native_client.py` / `server.py`。没有原生密码学验签、自动重试或本地工具执行器。
允许名称表只是本地过滤，不意味着这些名称已注册到云端。签名设备检查也不等于密码学验签。

`connector_task_input_from_catalog` 接受调用方取得的本机运行态和 MCP 目录快照，要求连接器为 READY、enabled，且唯一出现在目录中；仅使用实际发现的工具定义，保留 annotations 与 meta。它不验证快照来源或时效，也不能替代连接器注册与设备通道就绪检查。低层 `connector_task_input` 仍供合成测试使用，不能把成功编码作为发起生成的就绪依据。

测试使用合成帧，不使用 Cookie、真实设备流或线上请求：

```sh
python3 -m unittest discover -s tools/desktop_session -p test_native_tools.py -v
```

## 当前 MCP 路径的补充证据

- `native_sources/9533.js`（SHA-256 `89930b295418b2f74ded50d487d469f2e806a2b2a27ee0751dc602f96189d527`）模块 691459/697347：`general_task_param.task_input_json` 中的 `schema_version=1`、`localConnectors[].connectorId/mcpToolsList`；工具包括 name、description、JSON 字符串 inputSchema。生成请求把 general_task_param 放在 option 内（task_sources/7745.js）。
- `native_sources/9729.js`（SHA-256 `cb751f374e2df0d6d22488555c1a37fc011192196d2b64e0f0a34969c8479a02`）：相同工具快照也用于工作任务输入。
- `pak_selected/9034.js` 字符偏移 610894：实际执行器名称是 `connector.call`，input 包含 `protocol_version=1`、`connector_id`、`remote_tool_name`、字符串 `raw_arguments`。Responses 返回远端函数名及参数，保留上游 call_id；不能把 connector.call 当成用户定义函数名。
- 此执行器的回传使用 `tool_result.raw_result`（原生 mcpResponseJson 原文）及 `tool_input`，外加上游 id/name。`connector_result_payload` 保留这些内容，不自行生成 JSON-RPC ID。
- 连接器工具列表注入与 `/alice/office/skills/manage/connector/create_personal` 是不同操作。任意 connectorId 出现在工具列表中不证明它已成为可用连接器。

## 2026-10-13 受控在线验证

用户允许一轮无副作用验证。只尝试一次生成，不重试，不执行文件、Shell 或桌面工具。

1. 使用既有适配器登录快照及当前 2.31.4 桌面请求元数据，模型目录返回 HTTP 200 / code 0。尝试读取最新 Mac Cookie 被钥匙串拒绝；没有修改或解锁钥匙串。
2. 新建 sandbox/connector 标识，使用现有合法设备身份请求带 sandbox 路由参数的 SSE；这一参数不证明设备通道已隔离。没有创建云端个人连接器实体。测试函数仅应在收到调用后生成随机字符串。
3. `/chat/completion` 返回 HTTP 200、SSE_ACK 和助手消息；设备 `/alice/office/tool_local/chunk_stream` 在收到响应头前超时，没有收到任何工具事件。生成流也超时；两者均未重试。
4. 只读检查这次测试会话：云端出现相应 `mcp__sub2api_marker_...__lookup_mar` 工具及 `key=native-proof` 参数，最终答案为“工具调用失败：连接器暂时不可用，未返回任何标记。请稍后重试。”助手 status/content_status 为 0，带 finish_time_ms，相关块全部 is_finish=true。任务已结束，无需再发送取消命令。
5. 这证明定义注入对模型有效，不能证明适配器收到设备派发、连接器可用或原生回传成功。测试执行器没有调用结果上传请求，随机标记未生成，工具执行次数为 0。

本轮请求合计 4 次：目录、设备流、一次生成、只读核对测试会话；自动重试 0 次。没有更改 probe 开关、启动客户端或部署服务。

脱敏结论见 native_validation.json；原始本机证据在 `/root/.codex/tmp/doubao-re/live-20261013-roundtrip.json` 与 `live-20261013-test-conversation.json`，不提交登录快照及原始会话。

## 同日离线补查：连接器与设备路由

本次补查只读取已提取资源和 Mac 现有日志；新增豆包 HTTP 请求、生成、工具执行与部署均为 0。
沿用 REA 分析记录 `ev_9e2618e219815b9e4ead0ebb67113c6e0342064b1607181434ab6119a0371976`；下面新增结论来自包内源码的直接静态读取与已有日志，不冒充 REA 的新运行时观测。

**已观测：测试指令到达了官方客户端。** Mac 的 `saman_2026.1013.2.log` 第 76657 行，时间 `2026-10-13 16:49:20.940283`，来源 `local-file-local-tools-sse-wiring.ts`，记录 `event=command`、`name=sandbox`、`need_ack=false`、`status=received`，sandbox 与测试请求完全相同。我们的独立监听没有收到该事件。此次检查在该日志 16:48–16:50 时间窗口未发现 `toolcall` 记录；这不是对全部原生运行态日志的穷尽检查。

所以“随机 sandbox + 复用已有设备 ID”没有建立已验证的独立执行通道。日志未保留该条指令的 input，不能判断它是 connect、disconnect 还是 release，也不能断言它就是工具失败的唯一原因。没有重放此指令、重连设备或重试模型。客户端将多个 sandbox 的指令分派给本地执行上下文，不能假定 `sand_box_id` 查询参数就是独占订阅边界。

**已确认的官方连接器流程：**

1. `AGWManageCreatePersonalConnector` POST `/alice/office/skills/manage/connector/create_personal`，body 是 `name` 和 `mcp_config`；使用响应 `data.connector.connector_id`。更新接口额外带 `connector_id`。
2. `mcp_config` 的 Streamable HTTP 形式为 `{"transport_type":1,"endpoint":"https://example.invalid/mcp","headers":{}}`；stdio 为 `{"transport_type":3,"stdio_config":{"command":"...","params":[],"env":{}}}`。枚举 2 是旧 SSE HTTP，但当前创建表单编码器只生成 1 或 3。这里只给出协议结构，没有创建实体或启动进程。
3. 创建后，客户端检查 `neotix.taskMode.runtime.queryRuntimeInfo({env:true})` 是否提供环境，调用 `triggerUpdate()`，等待 `queryLocalConnectors()` 中对应连接器的本地状态更新至 READY。安装、启动、授权失败有独立终态；云端创建成功并不保证本地就绪。
4. 生成前再读取 `neotix.mcp.getAllTools()`，取 `data.connectors[].connectorId` 和 `toolsJson`。`toolsJson` 是工具数组的 JSON 字符串，工具的 inputSchema/annotations/_meta 原本是对象；发往生成入口时分别编码为 inputSchema/annotations/meta 字符串。这个目录与管理页的 `mcpToolListJson` 不是相同封装。
5. 模型选择后，经 `connector.call` 路由到 `neotix.mcp.callTool`，返回 `mcpResponseJson`，再走工具结果上传。端点与载荷详见上文。

静态依据（路径相对离线 artifacts）：

| 材料 | 位置 | SHA-256 |
| --- | --- | --- |
| native_sources/9384.js | 模块 469071 的 mcp_config 编码；模块 164828 的创建、环境检查、更新与等待 | c4813e1bcb1f86c9312b20f5ef9fcadf40f472357e8d34e51e600094b7287b6a |
| pak_selected/7695.js | 模块 749957 的就绪状态机、762425 的本地快照、168438 的 triggerUpdate | 6154c0bded61a63fd1d9f0a7d4f7892c571a9c80e7bb00909adb5b41437c18f8 |
| connector_sources/7275.js | 模块 383214 的传输枚举、478601 的原生运行态 API | 2acd02347f41ed688ce06e4d920a3da8f3c75effd9f0c63c0dc86c0b91c63d0b |
| native_sources/9533.js | 模块 691459/697347 的 getAllTools 与生成前目录注入 | 89930b295418b2f74ded50d487d469f2e806a2b2a27ee0751dc602f96189d527 |

**已确认的 sandbox 控制协议：** `9034.js` 字符偏移 925000–934000 的解析器接受 command.name=sandbox，input.type 的 1/2/3 分别对应 connect/disconnect/release；对应对象中的 sandbox_id 必须匹配外层，connect/disconnect 还携带 connection/agent/task 关联信息。控制指令与 function_call 必须分开处理，收到控制指令本身不意味着可以执行工具。

`need_ack=true` 时，调用方实际执行生命周期操作后才可 POST `/alice/office/tool_local/upload_command_result`，body 为 `{"device_id":"...","command_result":{"id":"...","code":0}}`，失败 code=1，可带 message；没有 tool_result，也不携带 sandbox_id。`need_ack=false` 不上传。依据为 `9034.js` 923984 的 `Cd` 与 `7035.js` 84364 的 `AGWUploadCommandResult`。离线代码只做解析、去重、结果编码，拒绝 workspace 等其他命令。

仍未解决：实际注册并启动用于适配器的 MCP 连接器、建立拥有正确执行上下文的设备路由、控制生命周期的真实执行，以及真实云端的工具结果续接。`server.py` 集成与传输代码已完成下述离线验证。现有在线服务仍使用原来的文本工具桥。下一次在线验收必须先具备运行态条件，不能靠换随机标识反复生成。`native_custom_tool_roundtrip_verified` 保持 false；现有证据不支持归因于风控或账号失效。

## Responses 集成与运行边界

显式会话快照中的 `native_tools` 对象选择原生模式；缺少此字段仍使用既有适配器。存在但无效的对象会报错，不回退成提示词工具桥。原生会话的 `desktop_version` 必须为 `2.31.4`，使用现有合法 Cookie 和数字 `params.device_id`；禁止覆盖公共应用参数或重放 `a_bogus`。

`native_tools` 的必填数据来自已准备的运行态，而不是示例或随机标识：

| 字段 | 要求 |
| --- | --- |
| `connector_id` | 真实连接器 ID |
| `connector_runtime` | `{connectorId, status: "READY", enabled: true}`，ID 必须匹配 |
| `mcp_catalog` | `neotix.mcp.getAllTools()` 的完整响应；目录中恰有一个对应连接器 |
| `client_option` | 当前执行上下文的 `client_env_id`、`sandbox_id`、`workspace`、`os`、布尔 `enable_sandbox` |
| `device_name` | 可选显示名 |

这些检查只验证快照的一致性，不能认证快照来源、证明新鲜度或证明独占设备路由。代码不会创建云端连接器、授权设备、创建桌面 sandbox，也不会将 Mac 现有运行目录的存在当作 READY。启用前仍需真实注册、目录发现和正确的设备执行上下文；不要把官方客户端当前使用的设备 ID 直接当成适配器专属通道。

已接入的行为：

- 只支持 `/v1/responses` 的 Office 模型和无 namespace 的 `function`。声明的函数名及参数 schema 必须与目录一致，工具定义通过 `general_task_param.task_input_json` 发送，保留目录的 annotations/meta。正文中不注入工具调用 JSON 协议，也不把回答文本解析为工具调用。
- 首先打开一次设备 SSE，实际收到心跳后才发起一次生成。心跳可以不带 connection_id，connect 指令也可以更新该 ID；依据为 `9034.js` 字符偏移约 752000 的客户端实现。合法生成 ACK 通过会话和模型校验后才向调用方返回设备工具事件。
- 保留上游 `call_id`、函数名、参数原文。调用方回传 `function_call_output`，适配器包装为 MCP 文本结果并上传到无 v2 后缀的原生接口，继续等待同一生成；不会把工具结果放入第二次生成的 prompt。
- 工具续接要求 `store=true` 和本进程返回的 `previous_response_id`。进程内最多保存当前生成的 64 个响应 ID；新生成清空旧生成的续接引用，超过生成期限后拒绝访问。没有持久化、跨进程恢复、完整历史形式的工具续接或完成后的新用户轮续聊。无工具的 `store=false` 请求不保存响应。与已部署文本桥的历史能力不同，当前不能直接替换依赖无状态完整历史续接的客户端。
- 续接按会话 scope、模型和原调用 ID 校验。省略的工具、instructions 和 text.format 继承正在进行的生成；显式修改会拒绝。相同结果重放复用已完成响应，冲突重放报错；未知、缺失和重复结果 ID 在上传前报 400。
- `need_ack=false` 的 sandbox connect 只记录路由，不宣称执行了生命周期。需要回执时只上传失败 `native_lifecycle_unavailable` 并终止；disconnect/release 使本地续接失败。此实现不能替代官方 sandbox 管理器。
- 一次设备连接、一次生成，不自动重连或重试上传。部分上传或回执不明时终止 run。空闲等待调用方结果也受总期限约束；超时、快照替换/删除和正常服务退出都会关闭旧流。同一适配器进程中的不同 profile 不能同时使用同一设备；这不约束官方 app 或其他进程。

`DESKTOP_NATIVE_CONNECT_TIMEOUT_SECONDS` 默认 20 秒；上游读取和总生成期限沿用 `DESKTOP_UPSTREAM_TIMEOUT_SECONDS`（默认 120）与 `DESKTOP_GENERATION_TIMEOUT_SECONDS`（默认 600）。本地关闭流不是云端取消 API，不保证撤销已接受的云端任务。Responses SSE 仍然是每轮缓冲输出，但能在上游等待工具时及时返回调用，不必等最终答案。

离线验证使用已存在的 Python 3.12 测试镜像，网络禁用，不替换线上容器：

```sh
docker run --rm --network none --read-only --tmpfs /tmp \
  -v /tmp/sub2api-doubao-native-lifecycle/tools/desktop_session:/app:ro \
  -e PYTHONDONTWRITEBYTECODE=1 --entrypoint python \
  sub2api-desktop:responses-20260929 -m unittest discover -p 'test_*.py'
```

`test_native_client.py` 通过真正的本地 HTTP 入口测试 Responses JSON/SSE、一个及多个原生调用、同一生成续接、生成先结束而上传回执后到、超时/断流、错误目录/ACK/agent、重复或冲突结果、profile 隔离与资源关闭。上游来自合成帧，不能把这些测试描述为云端原生闭环验收。

本轮额外 Mac 只读检查确认 `Default/connector_runtime` 存在连接器运行产物，`sandbox_envs_dir/envs` 中有 5 份环境元数据。只检查了路径、结构和相关状态键，未读取或修改加密 MCP 凭据。此发现只证明客户端曾准备环境，不证明用于适配器的连接器已经注册、就绪或拥有设备通道。本轮新增豆包云端请求、模型生成、真实工具执行和生产部署均为 0。
