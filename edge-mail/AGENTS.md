# Edge Mail - 边缘计算邮件服务

## 定位

- 独立于 Sub2API 主网关的边缘计算服务，部署在 252 Docker 主机上
- 提供 IMAP 收件 + SMTP 发件的统一接口，支持接入 Outlook / Google / QQ 等邮箱
- **不持久化任何账号与邮件**：账号信息随请求体传入，服务不做落盘或缓存
- 只依赖标准库 `imaplib`/`smtplib`/`email`，运行时不引入额外系统库或大模型
- 容器只加入 `sub2api_sub2api-network`，不暴露宿主端口；公网统一走 `18080 /mail/*`

## 边界

- 服务只做协议交互与内容解析，不做邮件索引、搜索缓存、规则引擎或长期存储
- 凭据不落盘、不写日志；错误信息只回传错误类型与人类可读提示，绝不含密码/授权码
- 收件默认只读打开邮箱（`readonly=True`），避免读取动作意外改动邮件状态
- 发件是否允许由 `MAIL_SEND_ENABLED` 控制；需要收敛时用 `MAIL_ALLOW_EXTERNAL_RECIPIENTS=0`

## 关键约定

- 三家服务商凭据都不是登录密码：Gmail 用应用专用密码、Outlook 仅支持仍允许基本认证的邮箱（OAuth2 尚未实现）、QQ 用 IMAP/SMTP 授权码。见 README 的接入方式表。
- 服务商解析优先级：显式 `provider`（含别名） > 邮箱域名推断（支持
  `*.onmicrosoft.com` 这类企业子域后缀匹配）。
- 中文/日文邮箱名必须走 IMAP modified UTF-7（`app/imap_utf7.py`），
  `imaplib.list()` 返回的是**去掉 `* LIST` 标签**的 untagged response，
  `_parse_list_line` 必须同时接受带标签与不带标签两种形态。
- 异常类在导入期绑定为 `_IMAP_ERROR` / `_SMTP_AUTH_ERROR`，不要在 `except` 里写
  `imaplib.IMAP4.error`，否则属性查找会在异常处理路径上二次失败。

## 验证

```bash
python3 -m pytest -q edge-mail/tests
docker build -f edge-mail/docker/Dockerfile edge-mail
```

测试全部离线运行：用 `tests/fake_servers.py` 的内存版 IMAP/SMTP 驱动协议交互，
不需要真实邮箱账号，也不访问网络。新增行为必须补对应测试。

- `proxy` 是可选的 HTTP CONNECT / SOCKS5 出站代理，IMAP 与 SMTP 共用；不允许代理失败时静默直连。
- 保存账号由主网关管理，edge-mail 仍只消费请求中的凭据；代理认证信息必须按 URL 规则解码。
- HTTP CONNECT 响应读取不能吞掉紧随其后的 SMTP greeting；隧道上的 TLS 必须校验服务端证书。
- 当前只实现密码认证，不能声称 Outlook OAuth2 已支持。
