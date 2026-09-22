# ZCode 内置适配器维护

- 集成入口见 `README.sub2api.md`；上游来源和固定版本记录在该文件。保留 MIT 许可。
- 模块只依赖 Go 标准库；构建入口 `./cmd/server`，Docker 上下文为仓库 `tools/`。
- `config.Load` 读取可选 JSON 后应用 `Z2A_*` 环境变量；共享密钥和上游凭据必须独立配置，禁止提交真实凭据。
- `server.New(...).Handler()` 暴露 `/v1/models`、`/v1/chat/completions`，均要求共享密钥。请求示例：`{"model":"glm-5.3","messages":[{"role":"user","content":"hi"}]}`。
- 上游使用 Anthropic Messages；`convert` 负责文本、思考、工具调用和流式转换。客户端请求与响应的模型 ID 必须一致。
- `/livez` 仅表示进程存活；`/healthz` 检查凭据是否存在，不证明上游接受凭据。缺凭据或上游失败必须返回明确错误。
- 进程接受 SIGTERM/SIGINT 并执行 HTTP 优雅退出；流式请求通过 context 取消，思考重放缓存在内存中。
- 修改后运行 `go test ./...`；集成测试覆盖显式服务器凭据、桌面配置回退、工具与流式转换。真实调用须另外验证，不能用静态模型目录或探活代替。
