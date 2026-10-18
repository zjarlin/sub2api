# 我的账号

恢复历史 `26404bd0a`、`7e5352a9a` 的用户账号管理。
创建/编辑弹窗以 `adapter-scope="user"` 调用内置适配器（WorkBuddy / TRAE Work / 豆包 / VibeX 等）
的登录与辅助接口，走 `/user/builtin-adapters/*`、`/user/accounts/*`；管理员面板仍走 `/admin/*`。页面复用现有账号表格操作与状态组件，列表、搜索、分页和测试由页面负责；凭证与模型配置由编辑弹窗负责。服务端固定 JWT 用户归属并验证分组权限；用户账号默认不参与公共调度，显式共享后才按绑定分组参与调度。敏感凭证不回显，编辑留空时保留。
`容量`列在并发/额度徽章下方复用 `BuiltinCheckinCell`（`scope="user"`）显示 WorkBuddy / TRAE Work 账号的剩余积分与签到明细，只读、只查当前绑定账号，走 `/user/accounts/:id/sidecar/checkins`；弹窗内「签到」按钮走 `/user/accounts/:id/sidecar/checkin`（POST）立即签到并回读最新积分，未绑定账号时拒绝；关联账号池已有登录、以及「全部签到」（整池）仍是管理端能力，用户侧弹窗不提供。
