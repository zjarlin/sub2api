# 原生设备工具桥（离线实现）

2026-10-13：已从 MacBook 安装的 Doubao 2.31.4 的 `biz.pak` 提取后台实际实现。
`native_tools.py` 支持原生设备帧到 Responses function_call 的转换及结果载荷构造。
尚未接入 server.py。一次受控在线验证已确认自定义工具定义对模型可见；设备流未收到调用，回传与生成续接尚未通过。

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

这是严格的协议子集：只接受数字事件枚举及对象函数参数。支持 localConnectors 工具定义编码、connector.call 包装解码及原始 MCP 响应载荷。COMMAND 需另建生命周期处理；没有验签、网络传输、自动重试、工具执行或 HTTP 服务入口。
允许名称表只是本地过滤，不意味着这些名称已注册到云端。签名设备检查也不等于密码学验签。

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
2. 新建独立 sandbox/connector 标识，使用现有合法设备身份建立限定 sandbox 的 SSE。没有创建云端个人连接器实体。测试函数仅应在收到调用后生成随机字符串。
3. `/chat/completion` 返回 HTTP 200、SSE_ACK 和助手消息；设备 `/alice/office/tool_local/chunk_stream` 在收到响应头前超时，没有收到任何工具事件。生成流也超时；两者均未重试。
4. 只读检查这次测试会话：云端出现相应 `mcp__sub2api_marker_...__lookup_mar` 工具及 `key=native-proof` 参数，最终答案为“工具调用失败：连接器暂时不可用，未返回任何标记。请稍后重试。”助手 status/content_status 为 0，带 finish_time_ms，相关块全部 is_finish=true。任务已结束，无需再发送取消命令。
5. 这证明定义注入对模型有效，不能证明设备派发、连接器可用或原生回传成功。没有调用结果上传请求，随机标记未生成，本地工具执行次数为 0。

本轮请求合计 4 次：目录、设备流、一次生成、只读核对测试会话；自动重试 0 次。没有更改 probe 开关、启动客户端或部署服务。

脱敏结论见 native_validation.json；原始本机证据在 `/root/.codex/tmp/doubao-re/live-20261013-roundtrip.json` 与 `live-20261013-test-conversation.json`，不提交登录快照及原始会话。

下一接入门槛：确认个人连接器实体注册与设备运行态要求，解决设备流无响应；按确认的流实现生命周期，验证结果上传后继续同一生成。设备超时与“连接器暂时不可用”尚不足以定位单一原因，不把它归因于风控或账号失效。`native_custom_tool_roundtrip_verified` 保持 false。
