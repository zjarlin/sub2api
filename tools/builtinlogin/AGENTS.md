# builtinlogin 开发约定

- 依赖仅有 Go 标准库；入口说明见 README.md。
- 公共 API：`New(Begin)`, `Handler.Register`, `Flow`, `Account`, `ErrPending`, `PublicError`。
- 最小用法：`New(begin).Register(mux, withAuth)`；必须由调用方启用非空密钥认证，可信后端填写 `X-Login-Owner`。
- 状态只在适配器进程内保存，重启后重新授权；不要把 token 或回调链接暴露到查询参数、日志或返回值。
- 凭证保存和账号池更新属于 provider 的完成函数；返回成功之前两者都必须完成。
- 修改后执行 `go test -race ./...`，并运行两个调用模块的相关测试。
