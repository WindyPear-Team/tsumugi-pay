import { useEffect, useState } from "react"
import type { FormEvent } from "react"
import { Copy, Eye, EyeOff, Search } from "lucide-react"

import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Detail, Empty, formatDate, Modal, roleName } from "@/components/page-ui"
import type { ApiRequest, AuditLog, Refund, SiteSettings, Account, ManagedUser } from "@/types"

export function RefundsPage({ data }: { data: Refund[] }) {
  const [query, setQuery] = useState("")
  const [page, setPage] = useState(1)
  const filtered = data.filter((refund) => `${refund.refund_order_no} ${refund.reason} ${refund.status}`.toLowerCase().includes(query.toLowerCase()))
  const pageCount = Math.max(1, Math.ceil(filtered.length / 10))
  const current = Math.min(page, pageCount)
  const visible = filtered.slice((current - 1) * 10, current * 10)
  return (
    <div className="content">
      <section className="page-actions">
        <h2>退款记录</h2>
      </section>
      <section className="panel table-panel">
        <TableTools
          query={query}
          setQuery={(value) => {
            setQuery(value)
            setPage(1)
          }}
          placeholder="搜索退款单号、原因或状态"
        />
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>退款单号</th>
                <th>金额</th>
                <th>原因</th>
                <th>状态</th>
                <th>创建时间</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((refund) => (
                <tr key={refund.id}>
                  <td>
                    <b>{refund.refund_order_no}</b>
                    <small>{refund.bill_id}</small>
                  </td>
                  <td>¥ {refund.amount}</td>
                  <td>{refund.reason || "—"}</td>
                  <td>{refund.status}</td>
                  <td>{formatDate(refund.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!visible.length && <Empty icon="↩" title="暂无退款记录" copy="从已支付账单发起退款后，记录会显示在这里。" />}
        <TablePages page={current} pageCount={pageCount} setPage={setPage} />
      </section>
    </div>
  )
}
export function AuditPage({ data }: { data: AuditLog[] }) {
  const [query, setQuery] = useState("")
  const [page, setPage] = useState(1)
  const filtered = data.filter((entry) => `${entry.action} ${entry.target_type} ${entry.target_id} ${entry.request_id}`.toLowerCase().includes(query.toLowerCase()))
  const pageCount = Math.max(1, Math.ceil(filtered.length / 10))
  const current = Math.min(page, pageCount)
  const visible = filtered.slice((current - 1) * 10, current * 10)
  return (
    <div className="content">
      <section className="page-actions">
        <h2>操作审计</h2>
      </section>
      <section className="panel table-panel">
        <TableTools
          query={query}
          setQuery={(value) => {
            setQuery(value)
            setPage(1)
          }}
          placeholder="搜索操作、对象或请求 ID"
        />
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>操作</th>
                <th>对象</th>
                <th>结果</th>
                <th>请求 ID</th>
                <th>发生时间</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((entry) => (
                <tr key={entry.id}>
                  <td>{entry.action}</td>
                  <td>
                    {entry.target_type}
                    <small>{entry.target_id}</small>
                  </td>
                  <td className="mono">{entry.detail ? JSON.stringify(entry.detail) : "—"}</td>
                  <td className="mono">{entry.request_id}</td>
                  <td>{formatDate(entry.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!visible.length && <Empty icon="☷" title="暂无审计记录" copy="重要操作会在这里留下可追溯记录。" />}
        <TablePages page={current} pageCount={pageCount} setPage={setPage} />
      </section>
    </div>
  )
}
export function DevelopersPage({ account, request }: { account?: Account; request: ApiRequest }) {
  const merchant = account?.merchant_no || "{merchant_no}"
  const gateway = window.location.origin
  const [apiSecret, setAPISecret] = useState("")
  const [loadingSecret, setLoadingSecret] = useState(false)
  const [secretError, setSecretError] = useState("")
  async function revealSecret() {
    setSecretError("")
    setLoadingSecret(true)
    try {
      const result = await request("/api/v1/admin/developer-credentials")
      setAPISecret(result.api_secret || "")
    } catch (err: any) {
      setSecretError(err.message)
    } finally {
      setLoadingSecret(false)
    }
  }
  async function copySecret() {
    if (apiSecret) await navigator.clipboard.writeText(apiSecret)
  }
  return (
    <div className="content">
      <section className="developer-hero">
        <p className="eyebrow">EPAY COMPATIBILITY</p>
        <h2>易支付接入参数</h2>
        <p>使用标准易支付字段；支付结果以异步通知或订单查询为准。</p>
      </section>
      <section className="settings-grid">
        <article className="panel">
          <h3>账户配置</h3>
          <Detail label="网关地址" value={gateway} />
          <Detail label="商户号（pid）" value={merchant} />
          <Detail label="商户密钥（key）" value={apiSecret || "已隐藏"} />
          <Detail label="签名算法" value="HMAC-SHA256（兼容 MD5）" />
          <div className="row-actions developer-key-actions">
            <button className="icon-button" title={apiSecret ? "隐藏商户密钥" : "显示商户密钥"} aria-label={apiSecret ? "隐藏商户密钥" : "显示商户密钥"} onClick={() => (apiSecret ? setAPISecret("") : revealSecret())} disabled={loadingSecret}>
              {apiSecret ? <EyeOff size={15} /> : <Eye size={15} />}
            </button>
            <button className="icon-button" title="复制商户密钥" aria-label="复制商户密钥" onClick={copySecret} disabled={!apiSecret}>
              <Copy size={15} />
            </button>
          </div>
          {secretError && <p className="form-error">{secretError}</p>}
        </article>
        <article className="panel">
          <h3>易支付端点</h3>
          <Detail label="提交下单（GET / POST）" value={`${gateway}/submit.php`} />
          <Detail label="接口下单" value={`${gateway}/mapi.php`} />
          <Detail label="订单查询" value={`${gateway}/api.php?act=order`} />
        </article>
      </section>
      <section className="developer-grid">
        <article className="panel code-panel">
          <div className="panel-head">
            <h3>JSON 下单</h3>
          </div>
          <pre>
            <code>{`POST ${gateway}/mapi.php\nContent-Type: application/json\n\n{\n  "merchant_id": "${merchant}",\n  "payment_method": "alipay",\n  "merchant_order_no": "ORDER-001",\n  "subject": "示例订单",\n  "amount": "9.90",\n  "notify_url": "https://merchant.example/notify",\n  "sign_type": "HMAC-SHA256",\n  "sign": "使用 key 对非空字段排序后签名"\n}`}</code>
          </pre>
        </article>
        <article className="panel code-panel">
          <div className="panel-head">
            <h3>易支付表单与查询</h3>
          </div>
          <pre>
            <code>{`表单地址\nGET 或 POST ${gateway}/submit.php\n\n表单字段\npid=${merchant}\ntype=alipay\nout_trade_no=ORDER-001\nname=示例订单\nmoney=9.90\nnotify_url=https://merchant.example/notify\nsign_type=MD5\nsign=非空字段按字段名排序并以 & 连接，末尾追加 key 后取 MD5\n\n查询地址\nGET ${gateway}/api.php?act=order&pid=${merchant}&out_trade_no=ORDER-001&sign=...`}</code>
          </pre>
        </article>
      </section>
    </div>
  )
}

export function UsersPage({ data, request, onRefresh }: { data: ManagedUser[]; request: ApiRequest; onRefresh: () => void }) {
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<ManagedUser | null>(null)
  const [accountName, setAccountName] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [username, setUsername] = useState("")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [query, setQuery] = useState("")
  const [page, setPage] = useState(1)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const filtered = data.filter((user) => `${user.display_name} ${user.username} ${user.email} ${user.account?.merchant_no || ""}`.toLowerCase().includes(query.toLowerCase()))
  const pageCount = Math.max(1, Math.ceil(filtered.length / 10))
  const current = Math.min(page, pageCount)
  const visible = filtered.slice((current - 1) * 10, current * 10)
  async function create(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError("")
    try {
      await request("/api/v1/admin/users", {
        method: "POST",
        body: JSON.stringify({
          account_name: accountName,
          display_name: displayName,
          username,
          email,
          password,
        }),
      })
      setCreating(false)
      setAccountName("")
      setDisplayName("")
      setUsername("")
      setEmail("")
      setPassword("")
      onRefresh()
    } catch (err: any) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }
  async function edit(event: FormEvent) {
    event.preventDefault()
    if (!editing) return
    setBusy(true)
    setError("")
    try {
      await request(`/api/v1/admin/users/${editing.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          display_name: displayName,
          username,
          ...(password ? { password } : {}),
        }),
      })
      setEditing(null)
      setPassword("")
      onRefresh()
    } catch (err: any) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }
  async function setActive(user: ManagedUser, is_active: boolean) {
    try {
      setError("")
      await request(`/api/v1/admin/users/${user.id}`, {
        method: "PATCH",
        body: JSON.stringify({ is_active }),
      })
      onRefresh()
    } catch (err: any) {
      setError(err.message)
    }
  }
  function openEdit(user: ManagedUser) {
    setEditing(user)
    setDisplayName(user.display_name)
    setUsername(user.username)
    setPassword("")
  }
  return (
    <div className="content">
      <section className="page-actions">
        <div>
          <p className="eyebrow">PLATFORM USERS</p>
          <h2>用户管理</h2>
        </div>
        <button className="primary-button" onClick={() => setCreating(true)}>
          新增用户
        </button>
      </section>
      {error && <p className="form-error">{error}</p>}
      <section className="panel table-panel">
        <TableTools
          query={query}
          setQuery={(value) => {
            setQuery(value)
            setPage(1)
          }}
          placeholder="搜索用户名、邮箱或商户号"
        />
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>用户</th>
                <th>账户</th>
                <th>商户号</th>
                <th>状态</th>
                <th>创建时间</th>
                <th aria-label="操作" />
              </tr>
            </thead>
            <tbody>
              {visible.map((user) => (
                <tr key={user.id}>
                  <td>
                    <b>{user.display_name}</b>
                    <small>
                      {user.username} · {user.email}
                    </small>
                  </td>
                  <td>{user.account?.name || "—"}</td>
                  <td className="mono">{user.account?.merchant_no || "—"}</td>
                  <td>
                    <span className={user.is_active ? "status status-paid" : "status status-closed"}>{user.is_active ? "启用" : "停用"}</span>
                  </td>
                  <td>{formatDate(user.created_at)}</td>
                  <td>
                    <button className="secondary-button table-action" onClick={() => openEdit(user)}>
                      编辑
                    </button>
                    <button className="secondary-button table-action" onClick={() => setActive(user, !user.is_active)}>
                      {user.is_active ? "停用" : "启用"}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!visible.length && <Empty icon="♙" title="暂无用户" copy="创建用户后，每位用户都会拥有独立的账户和商户号。" />}
        <TablePages page={current} pageCount={pageCount} setPage={setPage} />
      </section>
      {creating && (
        <Modal title="新增用户" onClose={() => !busy && setCreating(false)}>
          <form className="modal-form" onSubmit={create}>
            <Label>
              账户名称
              <Input value={accountName} onChange={(event) => setAccountName(event.target.value)} required />
            </Label>
            <Label>
              姓名
              <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} required />
            </Label>
            <Label>
              用户名
              <Input value={username} onChange={(event) => setUsername(event.target.value)} required />
            </Label>
            <Label>
              邮箱
              <Input type="email" value={email} onChange={(event) => setEmail(event.target.value)} required />
            </Label>
            <Label>
              初始密码
              <Input type="password" minLength={10} value={password} onChange={(event) => setPassword(event.target.value)} required />
            </Label>
            <p className="form-note">将自动分配从 1000 开始递增的数字商户号。</p>
            <div className="form-actions">
              <button className="secondary-button" type="button" onClick={() => setCreating(false)}>
                取消
              </button>
              <button className="primary-button" disabled={busy}>
                {busy ? "创建中…" : "创建用户"}
              </button>
            </div>
          </form>
        </Modal>
      )}
      {editing && (
        <Modal title="编辑用户" onClose={() => !busy && setEditing(null)}>
          <form className="modal-form" onSubmit={edit}>
            <Label>
              姓名
              <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} required />
            </Label>
            <Label>
              用户名
              <Input value={username} onChange={(event) => setUsername(event.target.value)} required />
            </Label>
            <Label>
              新密码
              <Input type="password" minLength={10} placeholder="留空则不修改" value={password} onChange={(event) => setPassword(event.target.value)} />
            </Label>
            <div className="form-actions">
              <button className="secondary-button" type="button" onClick={() => setEditing(null)}>
                取消
              </button>
              <button className="primary-button" disabled={busy}>
                {busy ? "保存中…" : "保存修改"}
              </button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  )
}

export function ProfilePage({ user, request, onSaved }: { user: any; request: ApiRequest; onSaved: (user: any) => void }) {
  const [displayName, setDisplayName] = useState(user?.display_name || "")
  const [username, setUsername] = useState(user?.username || "")
  const [password, setPassword] = useState("")
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")
  useEffect(() => {
    setDisplayName(user?.display_name || "")
    setUsername(user?.username || "")
  }, [user])
  async function save(event: FormEvent) {
    event.preventDefault()
    try {
      setError("")
      await request("/api/v1/admin/me", {
        method: "PATCH",
        body: JSON.stringify({
          display_name: displayName,
          username,
          ...(password ? { password } : {}),
        }),
      })
      const updated = await request("/api/v1/admin/me")
      onSaved(updated)
      setPassword("")
      setMessage("个人资料已保存")
    } catch (err: any) {
      setError(err.message)
    }
  }
  return (
    <div className="content">
      <section className="settings-tab-panel">
        <article className="panel settings-service-card">
          <p className="eyebrow">MY PROFILE</p>
          <h3>个人资料</h3>
          <form onSubmit={save}>
            <Field label="姓名" value={displayName} set={setDisplayName} />
            <Field label="用户名" value={username} set={setUsername} />
            <Field label="邮箱" value={user?.email || ""} set={() => {}} disabled />
            <Field label="新密码" value={password} secret placeholder="留空则不修改" set={setPassword} />
            {error && <p className="form-error">{error}</p>}
            {message && <p className="form-success">{message}</p>}
            <button className="primary-button">保存个人资料</button>
          </form>
        </article>
      </section>
    </div>
  )
}

function TableTools({ query, setQuery, placeholder }: { query: string; setQuery: (value: string) => void; placeholder: string }) {
  return (
    <div className="table-tools">
      <Input value={query} placeholder={placeholder} onChange={(event) => setQuery(event.target.value)} />
    </div>
  )
}
function TablePages({ page, pageCount, setPage }: { page: number; pageCount: number; setPage: (page: number) => void }) {
  if (pageCount < 2) return null
  return (
    <div className="table-pages">
      <button className="secondary-button" disabled={page === 1} onClick={() => setPage(page - 1)}>
        上一页
      </button>
      <span>
        {page} / {pageCount}
      </span>
      <button className="secondary-button" disabled={page === pageCount} onClick={() => setPage(page + 1)}>
        下一页
      </button>
    </div>
  )
}

type EmailDraft = {
  host: string
  port: number
  username: string
  password: string
  from: string
}
type HCaptchaDraft = { enabled: boolean; site_key: string; secret_key: string }
type OIDCDraft = {
  enabled: boolean
  issuer_url: string
  client_id: string
  client_secret: string
  redirect_url: string
  authorization_endpoint: string
  token_endpoint: string
  jwks_uri: string
  login_label: string
}
type SettingsTab = "site" | "account" | "email" | "authentication"

export function SettingsPage({ user, account, settings, request, onSaved }: { user: any; account?: Account; settings: SiteSettings | null; request: ApiRequest; onSaved: (value: SiteSettings) => void }) {
  const [activeTab, setActiveTab] = useState<SettingsTab>("account")
  const [email, setEmail] = useState<EmailDraft>({
    host: "",
    port: 587,
    username: "",
    password: "",
    from: "",
  })
  const [hcaptcha, setHCaptcha] = useState<HCaptchaDraft>({
    enabled: false,
    site_key: "",
    secret_key: "",
  })
  const [oidc, setOIDC] = useState<OIDCDraft>({
    enabled: false,
    issuer_url: "",
    client_id: "",
    client_secret: "",
    redirect_url: "",
    authorization_endpoint: "",
    token_endpoint: "",
    jwks_uri: "",
    login_label: "",
  })
  const [site, setSite] = useState<SiteSettings["site"]>({
    site_name: "Tsumugi Pay",
    theme_color: "#2f9c84",
    favicon_url: "",
    allow_password_login: true,
    allow_password_registration: false,
    email_whitelist: [],
    terms_url: "",
    privacy_policy_url: "",
    oidc_enabled: false,
    oidc_login_label: "",
  })
  const [error, setError] = useState("")
  const [discovering, setDiscovering] = useState(false)

  useEffect(() => {
    if (!settings) return
    setSite(settings.site)
    setEmail({
      host: settings.email.host,
      port: settings.email.port || 587,
      username: settings.email.username,
      password: "",
      from: settings.email.from,
    })
    setHCaptcha({
      enabled: settings.hcaptcha.enabled,
      site_key: settings.hcaptcha.site_key,
      secret_key: "",
    })
    setOIDC({
      enabled: settings.oidc.enabled,
      issuer_url: settings.oidc.issuer_url,
      client_id: settings.oidc.client_id,
      client_secret: "",
      redirect_url: settings.oidc.redirect_url,
      authorization_endpoint: settings.oidc.authorization_endpoint,
      token_endpoint: settings.oidc.token_endpoint,
      jwks_uri: settings.oidc.jwks_uri,
      login_label: settings.oidc.login_label,
    })
  }, [settings])

  async function save(key: "site" | "email" | "hcaptcha" | "oidc") {
    try {
      setError("")
      onSaved(
        await request("/api/v1/admin/site-settings", {
          method: "PATCH",
          body: JSON.stringify({
            [key]: key === "site" ? site : key === "email" ? email : key === "hcaptcha" ? hcaptcha : oidc,
          }),
        })
      )
    } catch (err: any) {
      setError(err.message)
    }
  }

  async function discoverOIDC() {
    try {
      setError("")
      setDiscovering(true)
      const discovered = await request("/api/v1/admin/site-settings/oidc-discovery", {
        method: "POST",
        body: JSON.stringify({ issuer_url: oidc.issuer_url }),
      })
      setOIDC({
        ...oidc,
        issuer_url: discovered.issuer_url,
        authorization_endpoint: discovered.authorization_endpoint,
        token_endpoint: discovered.token_endpoint,
        jwks_uri: discovered.jwks_uri,
      })
    } catch (err: any) {
      setError(err.message)
    } finally {
      setDiscovering(false)
    }
  }

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: "site", label: "站点" },
    { id: "account", label: "账户" },
    { id: "email", label: "邮件服务" },
    { id: "authentication", label: "登录认证" },
  ]
  return (
    <div className="content settings-content">
      <div className="settings-tabs" role="tablist" aria-label="站点设置">
        {tabs.map((tab) => (
          <button key={tab.id} type="button" role="tab" aria-selected={activeTab === tab.id} className={activeTab === tab.id ? "selected" : ""} onClick={() => setActiveTab(tab.id)}>
            {tab.label}
          </button>
        ))}
      </div>
      {activeTab === "site" && (
        <section className="settings-tab-panel" role="tabpanel">
          <ServiceCard title="站点与访问控制" save={() => save("site")}>
            <Field label="站点名称" value={site.site_name} set={(site_name) => setSite({ ...site, site_name })} />
            <Field label="站点图标 URL" value={site.favicon_url} placeholder="https://example.com/favicon.png 或 /favicon.ico" set={(favicon_url) => setSite({ ...site, favicon_url })} />
            <Label className="form-label">
              主题色
              <Input type="color" value={site.theme_color} onChange={(event) => setSite({ ...site, theme_color: event.target.value })} />
            </Label>
            <Toggle label="允许账号密码登录" description="仅关闭账号密码登录；不会影响 OIDC 登录。" checked={site.allow_password_login} onCheckedChange={(allow_password_login) => setSite({ ...site, allow_password_login })} />
            <Toggle label="允许账号密码注册" description="仅关闭账号密码注册；不会影响 OIDC 首次登录开户。" checked={site.allow_password_registration} onCheckedChange={(allow_password_registration) => setSite({ ...site, allow_password_registration })} />
            <Field label="用户协议链接" value={site.terms_url} set={(terms_url) => setSite({ ...site, terms_url })} />
            <Field label="隐私政策链接" value={site.privacy_policy_url} set={(privacy_policy_url) => setSite({ ...site, privacy_policy_url })} />
            <Label className="form-label">
              邮箱后缀白名单
              <Textarea
                rows={5}
                value={site.email_whitelist.join("\n")}
                placeholder="@example.com\n@company.cn"
                onChange={(event) =>
                  setSite({
                    ...site,
                    email_whitelist: event.target.value
                      .split("\n")
                      .map((item) => item.trim())
                      .filter(Boolean),
                  })
                }
              />
            </Label>
          </ServiceCard>
        </section>
      )}
      {activeTab === "account" && (
        <section className="settings-grid settings-tab-panel" role="tabpanel">
          <article className="panel">
            <p className="eyebrow">CURRENT IDENTITY</p>
            <h3>登录身份</h3>
            <Detail label="邮箱" value={user?.email} />
            <Detail label="权限" value={roleName(user?.role || "")} />
            <Detail label="当前账户" value={account?.name} />
            <Detail label="商户号" value={account?.merchant_no} />
          </article>
        </section>
      )}
      {activeTab === "email" && (
        <section className="settings-tab-panel" role="tabpanel">
          <ServiceCard title="邮件服务（SMTP）" save={() => save("email")}>
            <Field label="SMTP 主机" value={email.host} set={(value) => setEmail({ ...email, host: value })} />
            <Field label="端口" value={String(email.port)} set={(value) => setEmail({ ...email, port: Number(value) || 0 })} />
            <Field label="用户名" value={email.username} set={(value) => setEmail({ ...email, username: value })} />
            <Field label="密码" value={email.password} secret placeholder={settings?.email.password_configured ? "已配置；留空保持不变" : ""} set={(value) => setEmail({ ...email, password: value })} />
            <Field label="发件人地址" value={email.from} set={(value) => setEmail({ ...email, from: value })} />
          </ServiceCard>
        </section>
      )}
      {activeTab === "authentication" && (
        <section className="settings-auth-grid settings-tab-panel" role="tabpanel">
          <ServiceCard title="hCaptcha" save={() => save("hcaptcha")}>
            <Toggle label="启用 hCaptcha" description="在登录表单中要求完成验证。" checked={hcaptcha.enabled} onCheckedChange={(enabled) => setHCaptcha({ ...hcaptcha, enabled })} />
            <Field label="Site Key" value={hcaptcha.site_key} set={(value) => setHCaptcha({ ...hcaptcha, site_key: value })} />
            <Field label="Secret Key" value={hcaptcha.secret_key} secret placeholder={settings?.hcaptcha.secret_key_configured ? "已配置；留空保持不变" : ""} set={(value) => setHCaptcha({ ...hcaptcha, secret_key: value })} />
          </ServiceCard>
          <ServiceCard title="OIDC 认证服务" save={() => save("oidc")}>
            <Toggle label="启用 OIDC 登录" description="关闭账号密码方式不会影响此登录和首次开户。" checked={oidc.enabled} onCheckedChange={(enabled) => setOIDC({ ...oidc, enabled })} />
            <Field label="登录提示" value={oidc.login_label} placeholder="例如：使用 XX 通行证登录" set={(login_label) => setOIDC({ ...oidc, login_label })} />
            <Label className="form-label">
              Issuer URL
              <div className="discovery-input">
                <Input value={oidc.issuer_url} placeholder="https://id.example.com" onChange={(event) => setOIDC({ ...oidc, issuer_url: event.target.value })} />
                <button type="button" className="secondary-button discovery-button" onClick={discoverOIDC} disabled={discovering || !oidc.issuer_url.trim()}>
                  <Search size={14} />
                  {discovering ? "发现中" : "自动发现"}
                </button>
              </div>
            </Label>
            <Field label="Client ID" value={oidc.client_id} set={(value) => setOIDC({ ...oidc, client_id: value })} />
            <Field label="Client Secret" value={oidc.client_secret} secret placeholder={settings?.oidc.client_secret_configured ? "已配置；留空保持不变" : ""} set={(value) => setOIDC({ ...oidc, client_secret: value })} />
            <Field label="回调地址" value={oidc.redirect_url} set={(value) => setOIDC({ ...oidc, redirect_url: value })} />
            <DiscoveredEndpoints oidc={oidc} />
          </ServiceCard>
        </section>
      )}
      {error && <p className="form-error">{error}</p>}
    </div>
  )
}

function Field({ label, value, set, secret, placeholder, disabled }: { label: string; value: string; set: (value: string) => void; secret?: boolean; placeholder?: string; disabled?: boolean }) {
  return (
    <Label className="form-label">
      {label}
      <Input type={secret ? "password" : "text"} value={value} placeholder={placeholder} disabled={disabled} onChange={(event) => set(event.target.value)} />
    </Label>
  )
}
function Toggle({ label, description, checked, onCheckedChange }: { label: string; description: string; checked: boolean; onCheckedChange: (checked: boolean) => void }) {
  return (
    <div className="settings-toggle">
      <div>
        <b>{label}</b>
        <p>{description}</p>
      </div>
      <Switch checked={checked} onCheckedChange={onCheckedChange} aria-label={label} />
    </div>
  )
}
function DiscoveredEndpoints({ oidc }: { oidc: OIDCDraft }) {
  if (!oidc.authorization_endpoint && !oidc.token_endpoint && !oidc.jwks_uri) return null
  return (
    <div className="discovery-result">
      <p className="eyebrow">DISCOVERED ENDPOINTS</p>
      <Detail label="授权端点" value={oidc.authorization_endpoint || "未提供"} />
      <Detail label="令牌端点" value={oidc.token_endpoint || "未提供"} />
      <Detail label="JWKS 地址" value={oidc.jwks_uri || "未提供"} />
    </div>
  )
}
function ServiceCard({ title, children, save }: { title: string; children: React.ReactNode; save: () => void }) {
  return (
    <article className="panel settings-service-card">
      <p className="eyebrow">SITE SERVICE</p>
      <h3>{title}</h3>
      {children}
      <button className="primary-button" onClick={save}>
        保存配置
      </button>
    </article>
  )
}
