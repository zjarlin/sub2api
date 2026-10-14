# Edge Mail

边缘计算邮件收发服务，接入 Sub2API 统一鉴权与计费：

- `POST /mail/folders`：列出邮箱文件夹
- `POST /mail/messages`：按条件列出邮件
- `POST /mail/message`：读取单封邮件正文与附件清单
- `POST /mail/attachment`：下载指定附件
- `POST /mail/message/flags`：标记已读/未读、移动、删除
- `POST /mail/send`：发信（支持抄送、密送、HTML 正文与附件）
- `GET /mail/providers`：列出支持的服务商预设

## 定位

- 独立于 Sub2API 主网关的边缘计算服务，部署在 252 Docker 主机上
- 支持接入 **Outlook / Google / QQ**（另附网易 163/126 可选预设）
- 只依赖 Python 标准库的 `imaplib` / `smtplib` / `email`，运行时不引入额外系统库
- **不持久化任何邮箱账号与邮件内容**：每次调用由请求体携带邮箱地址与凭据
- 容器只加入 `sub2api_sub2api-network`，不暴露宿主端口；公网统一走 `18080 /mail/*`

## 账号接入方式（重要）

三家服务商都**不使用邮箱登录密码**，必须使用各自的专用凭据：

| 服务商 | 凭据 | 前置条件 | IMAP | SMTP |
|---|---|---|---|---|
| Google / Gmail | **应用专用密码** | 账号开启两步验证后生成 16 位应用密码 | `imap.gmail.com:993` SSL | `smtp.gmail.com:465` SSL |
| Microsoft Outlook | **应用密码**（或 OAuth2） | 个人账号可直接用应用密码；企业租户若禁用基本认证需改用 OAuth2 | `outlook.office365.com:993` SSL | `smtp.office365.com:587` STARTTLS |
| QQ 邮箱 | **IMAP/SMTP 授权码** | 在「设置 → 账户」开启 IMAP/SMTP 服务并生成授权码 | `imap.qq.com:993` SSL | `smtp.qq.com:465` SSL |

调用时只需给出 `email` 与 `credential`，服务会按邮箱域名自动选择服务商；
自建/企业邮箱可显式传 `provider`，并用 `imap_host` / `smtp_host` 等字段覆盖主机与端口。

## 请求示例

读取收件箱最近 10 封未读邮件：

```bash
curl -fsS -X POST http://192.168.31.252:18080/mail/messages \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@qq.com","credential":"授权码","unread_only":true,"limit":10}'
```

读取指定邮件（含正文与附件清单）：

```bash
curl -fsS -X POST http://192.168.31.252:18080/mail/message \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@gmail.com","credential":"应用专用密码","uid":"12345"}'
```

发信：

```bash
curl -fsS -X POST http://192.168.31.252:18080/mail/send \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "email":"you@outlook.com",
    "credential":"应用密码",
    "to":["someone@example.com"],
    "subject":"周报",
    "body":"见正文",
    "attachments":[{"filename":"report.pdf","content_type":"application/pdf","content_base64":"JVBERi0xLjQK"}]
  }'
```

## 配置

- `MAIL_SEND_ENABLED`：是否开放发信，默认 `true`；置 `false` 只保留收件能力
- `MAIL_MAX_FETCH`：单次拉取邮件数上限，默认 50（硬上限 500）
- `MAIL_MAX_ATTACHMENT_BYTES`：单个附件下载上限，默认 25 MiB
- `MAIL_ALLOWED_PROVIDERS`：逗号分隔的服务商白名单，留空表示全部允许
- `MAIL_ALLOW_EXTERNAL_RECIPIENTS`：是否允许向任意地址发信，默认 `true`；
  置 `false` 时只允许发给自己，可用于「只做通知」的收敛配置
- `MAIL_CONNECT_TIMEOUT_SECONDS` / `MAIL_OPERATION_TIMEOUT_SECONDS`：建连与操作超时
- `MAIL_DEFAULT_FROM_NAME`：缺省发件人显示名

## 验证

```bash
python3 -m pytest -q edge-mail/tests
docker build -f edge-mail/docker/Dockerfile edge-mail
```

测试全部离线运行，用内存版 IMAP/SMTP 假服务器驱动协议交互，不需要真实邮箱账号。

## 计费

与 `/vision/*`、`/media/*` 一致：请求先经 Sub2API 网关鉴权与计费资格检查，
成功后按分组账号写入用量。启用前需明确运营计费口径，不能宣称已按次计费。
