# Tsumugi Pay

Tsumugi Pay 是一个面向 SaaS 场景的**多用户支付运营系统**。每个登录账户就是独立商户主体，拥有自己的账单和支付通道；不存在租户或成员关联模型。项目由 Go 服务端和 React/Vite 管理后台组成，兼容 Open Payment Specification（OPS）的发现、易支付表单下单、接口下单、查询与异步通知语义，并为支付宝和微信支付保留官方直连接入路径。[1]

> 本项目实现的是支付系统软件与官方 API 适配层，不提供收单资质、商户主体、应用签约或生产密钥。接入生产环境前，运营方必须使用自己的支付宝与微信支付商户资质，并完成各服务商要求的准入和验收。

## 项目结构

| 路径 | 内容 |
| --- | --- |
| `cmd/`、`internal/` | 根目录 Go HTTP 服务、支付适配器与 OPS 兼容端点。 |
| `web/` | 使用指定 shadcn 初始化命令生成的 React、Vite 管理后台；其 `dist/` 会被 Go `embed` 到二进制文件。 |
| `internal/app/models.go` | GORM 数据模型与关联定义；服务启动时通过 `AutoMigrate` 自动同步跨库表结构和索引。 |
| `migrations/001_init.sql` | 旧版 PostgreSQL 原生建表参考；新部署不需要手动执行。 |

## 本地启动

服务支持 PostgreSQL、MySQL 8+ 与 SQLite。启动时会使用 GORM `AutoMigrate` 创建缺失的表、索引和关联；旧版单渠道唯一约束会被移除，以支持同一账户添加多个支付宝或微信支付通道。新部署不需要手动执行 SQL 迁移。以下示例使用开发配置；生产环境必须替换 JWT 密钥与 32 字节加密密钥。

```powershell
# 1. 设置服务配置（PostgreSQL）
$env:DATABASE_DRIVER = "postgres"
$env:DATABASE_URL = "postgres://tsumugi:tsumugi@localhost:5432/tsumugi_pay?sslmode=disable"
$env:PUBLIC_BASE_URL = "http://localhost:8080"
$env:JWT_SECRET = "replace-with-a-long-random-secret"

# MySQL 示例：
# $env:DATABASE_DRIVER = "mysql"
# $env:DATABASE_URL = "tsumugi:tsumugi@tcp(127.0.0.1:3306)/tsumugi_pay?parseTime=true&charset=utf8mb4"

# SQLite 示例（适合本地开发和单机部署）：
# $env:DATABASE_DRIVER = "sqlite"
# $env:DATABASE_URL = "./data/tsumugi-pay.db"

# 2. 构建前端，并将 web/dist 嵌入 Go 二进制同源托管
Set-Location .\web
yarn build
Set-Location ..
go run .\cmd\server

# 开发前端页面时，也可以使用独立 Vite 服务：
Set-Location .\web
Copy-Item .env.example .env
# .env 中的 VITE_API_BASE_URL 默认是 http://localhost:8080
yarn dev
```

开发环境首次启动会创建演示账户和登录账号，方便验证后台界面。账号为 `admin@tsumugi.local`，密码为 `ChangeMe123!`。**仅限本地开发**：部署生产系统时设置 `BOOTSTRAP_DEMO=false`，并在首次可访问时立即删除演示数据或轮换全部密钥。

## 首次启动向导（OOBE）

当数据库中尚无用户时，访问管理后台会自动进入首次启动向导，创建第一位**用户**及其独立支付工作区。向导完成后该入口会永久关闭；用户可在“支付通道”自行添加支付宝或微信支付。该流程适用于 `BOOTSTRAP_DEMO=false` 的生产首次部署；若启用了演示数据，则不会显示向导。

| 配置项 | 开发默认值 | 生产要求 |
| --- | --- | --- |
| `DATABASE_DRIVER` | `postgres` | `postgres`、`mysql` 或 `sqlite`。 |
| `DATABASE_URL` | PostgreSQL 开发连接串 | 传入对应驱动的 DSN；MySQL 使用 `parseTime=true`。 |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | 支付回调可访问的 HTTPS 域名。 |
| `JWT_SECRET` | 仅用于开发的占位值 | 强随机、私密并定期轮换。 |
| `APP_ENCRYPTION_KEY` | 由开发 JWT 密钥派生 | 必填；Base64 编码的 32 字节密钥。 |
| `APP_ENV` | `development` | 设置为 `production`。 |
| `BOOTSTRAP_DEMO` | `true` | 设置为 `false`。 |

## 后台管理端点

后台端点通过 `POST /api/v1/auth/login` 获取 JWT，并在后续请求中使用 `Authorization: Bearer <token>`。所有资源均固定归属当前登录账户；不支持通过请求头切换到其他账户。

| 方法 | 端点 | 用途 |
| --- | --- | --- |
| `POST` | `/api/v1/auth/login` | 后台用户登录。 |
| `POST` | `/api/v1/auth/register` | 开启密码注册且邮箱命中白名单时，创建独立用户账户。 |
| `GET` | `/api/v1/site-config` | 获取公开的站点名称、登录注册开关与协议链接。 |
| `GET` | `/api/v1/setup/status` | 查询是否需要首次初始化。仅暴露是否需要初始化。 |
| `POST` | `/api/v1/setup/initialize` | 系统无用户时创建首位用户及其支付工作区；一次性入口。 |
| `GET` | `/api/v1/admin/me` | 当前会话身份与角色。 |
| `GET` / `POST` | `/api/v1/admin/users` | 平台管理员查询或创建用户；新用户拥有独立账户和自动递增的数字商户号。 |
| `PATCH` | `/api/v1/admin/users/:id` | 平台管理员启用、停用或更名用户。 |
| `GET` | `/api/v1/admin/dashboard` | 账单与成功金额汇总。 |
| `GET` / `POST` | `/api/v1/admin/channels` | 查询或添加自己的支付宝、微信支付通道；同一服务商可添加多个。 |
| `PATCH` | `/api/v1/admin/channels/:id` | 保存官方凭据、修改名称、优先级、权重及启停状态。 |
| `GET` | `/api/v1/admin/bills` | 账单查询，支持 `?status=` 筛选。 |
| `GET` | `/api/v1/admin/bills/:id` | 单笔账单详情。 |
| `POST` | `/api/v1/admin/bills/:id/close` | 关闭待支付账单。 |
| `POST` | `/api/v1/admin/bills/:id/refunds` | 使用退款单号发起退款。 |
| `GET` | `/api/v1/admin/refunds` | 退款列表。 |
| `GET` | `/api/v1/admin/audit-logs` | 管理和资金操作审计记录。 |
| `GET` / `PATCH` | `/api/v1/admin/site-settings` | 仅平台管理员可管理站点名称、密码登录/注册、邮箱白名单、协议链接及 SMTP、hCaptcha、OIDC 配置；敏感密钥不会回显。 |

## OPS 与易支付兼容端点

服务通过固定路径公开机器可读能力清单，保留规范要求的拼写 `openpayment-configuation`，并提供规范拼写别名。[1]

| 方法 | 端点 | 用途 |
| --- | --- | --- |
| `GET` | `/.well-known/openpayment-configuation` | OPS 发现配置。 |
| `GET` | `/.well-known/openpayment-configuration` | 发现配置别名。 |
| `POST` | `/submit.php` | 传统表单下单，成功后跳转收银台。 |
| `POST` | `/mapi.php` | JSON 接口下单。 |
| `GET` / `POST` | `/api.php?act=order` | 订单查询。 |
| `POST` | `/api/v1/webhooks/alipay/:token` | 支付宝异步通知接收与 RSA2 验签。 |
| `POST` | `/api/v1/webhooks/wechat/:token` | 微信支付 API v3 通知验签与 AES-GCM 解密。 |

`mapi.php` 默认支持 `HMAC-SHA256`，同时兼容 OPS 基础层的 `MD5`。待签名字段会移除 `sign`、忽略空值、按 ASCII 字段名排序；金额按十进制字符串处理，避免浮点参与签名。[2]

```json
{
  "merchant_id": "1000",
  "payment_method": "alipay",
  "merchant_order_no": "ORDER-20260816-001",
  "subject": "月度订阅",
  "amount": "9.90",
  "notify_url": "https://merchant.example.com/pay/notify",
  "return_url": "https://merchant.example.com/pay/return",
  "metadata": "customer=42",
  "scene": "page",
  "sign_type": "HMAC-SHA256",
  "sign": "<canonical payload signature>"
}
```

## 官方支付通道配置

通道凭据仅可通过后台 `PATCH /api/v1/admin/channels/:id` 保存；服务端使用 AES-GCM 加密后写入数据库，接口不会回显密钥。启用通道前必须完成必填配置。构建时执行 `web/yarn build`，生成的 `web/dist` 会由 `web/embed.go` 编译进 Go 二进制，前端与 API 使用同一域名、端口托管。

### 支付宝

支付宝适配器会创建 `alipay.trade.page.pay`（`page`）或 `alipay.trade.wap.pay`（`wap`）请求，采用 RSA2 签名；退款使用 `alipay.trade.refund`，关闭交易使用 `alipay.trade.close`。支付宝通知会使用支付宝公钥验签，且只在验签、订单号、金额与状态校验后推进本地账单状态。[3]

```json
{
  "alipay": {
    "app_id": "支付宝应用 AppID",
    "app_private_key_pem": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
    "alipay_public_key_pem": "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----",
    "gateway_url": "https://openapi.alipay.com/gateway.do",
    "return_url": "https://merchant.example.com/payment/result"
  }
}
```

### 微信支付

微信支付适配器使用 API v3 商户签名请求；目前面向 OPS 的 `native` 与 `wap` 场景，Native 返回 `code_url` 供前端生成二维码。异步通知会验证 `Wechatpay-*` 签名头并使用 API v3 密钥解密资源对象。Native 支付适用于 PC 端扫码收款场景。[4]

```json
{
  "wechat": {
    "mch_id": "微信支付商户号",
    "app_id": "应用 AppID",
    "merchant_serial_no": "商户 API 证书序列号",
    "merchant_private_key_pem": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
    "api_v3_key": "32-byte-api-v3-key",
    "platform_public_key_pem": "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----",
    "platform_serial_no": "微信支付平台证书或公钥序列号"
  }
}
```

## 安全与生产上线检查

异步通知是最终支付结果来源；前台跳转只可用于支付结果展示，不应直接入账。商户通知处理还需要校验签名、商户订单号、金额、商户号和支付状态，并确保幂等。[2] 后端已实现回调事件去重、`pending → paid` 条件状态更新与账户隔离，但生产接入前仍应完成下列工作。

| 检查项 | 说明 |
| --- | --- |
| HTTPS 与公网可达回调 | 在生产环境强制使用 HTTPS，并在服务商后台配置与 `PUBLIC_BASE_URL` 一致的通知地址。 |
| 密钥治理 | 使用独立的密钥管理服务或受保护环境变量；定期轮换 JWT、数据加密和商户密钥。 |
| 权限最小化 | 仅授予通道配置、退款所需权限，并保留审计日志。 |
| 对账与补偿 | 将服务商账单下载、订单查询和退款查询纳入定时对账流程。 |
| 风险与合规 | 核实主体资质、PCI/数据保护义务、反洗钱与业务所在地监管要求。 |
| 压测与告警 | 针对回调重试、服务商超时、数据库故障和重复通知执行演练。 |

## 已执行的验证

后端已通过 `go test ./...` 编译检查；前端已通过 `yarn build` 生产构建检查。支付服务商的真实下单、退款和通知验签依赖各账户的有效沙箱或生产凭据，因此应在填入官方测试凭据后完成端到端联调。

## References

[1] [Open Payment Specification](https://spec.flweb.cn/open-payment/)

[2] [OPS 接口模型与安全兼容说明](https://spec.flweb.cn/open-payment/api-model.html)

[3] [支付宝电脑网站支付快速接入](https://opendocs.alipay.com/open/270/105899)

[4] [微信支付 Native 支付产品介绍](https://pay.weixin.qq.com/doc/v3/merchant/4012791874)
