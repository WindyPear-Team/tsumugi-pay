import { useEffect, useState } from "react"
import { Search } from "lucide-react"

import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Detail, Empty, formatDate, roleName } from "@/components/page-ui"
import type { ApiRequest, AuditLog, Refund, SiteSettings, Account } from "@/types"

export function RefundsPage({ data }: { data: Refund[] }) { return <div className="content"><section className="page-actions"><h2>退款记录</h2></section><section className="panel table-panel"><table><thead><tr><th>退款单号</th><th>金额</th><th>原因</th><th>状态</th><th>创建时间</th></tr></thead><tbody>{data.map((refund) => <tr key={refund.id}><td><b>{refund.refund_order_no}</b><small>{refund.bill_id}</small></td><td>¥ {refund.amount}</td><td>{refund.reason || "—"}</td><td>{refund.status}</td><td>{formatDate(refund.created_at)}</td></tr>)}</tbody></table>{!data.length && <Empty icon="↩" title="暂无退款记录" copy="从已支付账单发起退款后，记录会显示在这里。" />}</section></div> }
export function AuditPage({ data }: { data: AuditLog[] }) { return <div className="content"><section className="page-actions"><h2>操作审计</h2></section><section className="panel table-panel"><table><thead><tr><th>操作</th><th>对象</th><th>请求 ID</th><th>发生时间</th></tr></thead><tbody>{data.map((entry) => <tr key={entry.id}><td>{entry.action}</td><td>{entry.target_type}<small>{entry.target_id}</small></td><td className="mono">{entry.request_id}</td><td>{formatDate(entry.created_at)}</td></tr>)}</tbody></table>{!data.length && <Empty icon="☷" title="暂无审计记录" copy="重要操作会在这里留下可追溯记录。" />}</section></div> }
export function DevelopersPage({ account }: { account?: Account }) { const merchant = account?.merchant_no || "{merchant_no}"; const gateway = window.location.origin; return <div className="content"><section className="developer-hero"><p className="eyebrow">EPAY COMPATIBILITY</p><h2>易支付接入参数</h2><p>使用标准易支付字段；支付结果以异步通知或订单查询为准。</p></section><section className="settings-grid"><article className="panel"><h3>账户配置</h3><Detail label="网关地址" value={gateway}/><Detail label="商户号（pid）" value={merchant}/><Detail label="商户密钥（key）" value="创建账户时保存的 API 请求密钥"/><Detail label="签名算法" value="HMAC-SHA256（兼容 MD5）"/></article><article className="panel"><h3>易支付端点</h3><Detail label="提交下单" value={`${gateway}/submit.php`}/><Detail label="接口下单" value={`${gateway}/mapi.php`}/><Detail label="订单查询" value={`${gateway}/api.php?act=order`}/></article></section><section className="developer-grid"><article className="panel code-panel"><div className="panel-head"><h3>JSON 下单</h3></div><pre><code>{`POST ${gateway}/mapi.php\nContent-Type: application/json\n\n{\n  "merchant_id": "${merchant}",\n  "payment_method": "alipay",\n  "merchant_order_no": "ORDER-001",\n  "subject": "示例订单",\n  "amount": "9.90",\n  "notify_url": "https://merchant.example/notify",\n  "sign_type": "HMAC-SHA256",\n  "sign": "使用 key 对非空字段排序后签名"\n}`}</code></pre></article><article className="panel code-panel"><div className="panel-head"><h3>易支付表单与查询</h3></div><pre><code>{`表单地址\nPOST ${gateway}/submit.php\n\n表单字段\npid=${merchant}\ntype=alipay\nout_trade_no=ORDER-001\nname=示例订单\nmoney=9.90\nnotify_url=https://merchant.example/notify\nsign_type=HMAC-SHA256\nsign=使用 key 签名\n\n查询地址\nGET ${gateway}/api.php?act=order&pid=${merchant}&out_trade_no=ORDER-001&sign=...`}</code></pre></article></section></div> }

type EmailDraft = { host: string; port: number; username: string; password: string; from: string }
type HCaptchaDraft = { enabled: boolean; site_key: string; secret_key: string }
type OIDCDraft = { enabled: boolean; issuer_url: string; client_id: string; client_secret: string; redirect_url: string; authorization_endpoint: string; token_endpoint: string; jwks_uri: string }
type SettingsTab = "account" | "email" | "authentication"

export function SettingsPage({ user, account, settings, request, onSaved }: { user: any; account?: Account; settings: SiteSettings | null; request: ApiRequest; onSaved: (value: SiteSettings) => void }) {
  const [activeTab, setActiveTab] = useState<SettingsTab>("account")
  const [email, setEmail] = useState<EmailDraft>({ host: "", port: 587, username: "", password: "", from: "" })
  const [hcaptcha, setHCaptcha] = useState<HCaptchaDraft>({ enabled: false, site_key: "", secret_key: "" })
  const [oidc, setOIDC] = useState<OIDCDraft>({ enabled: false, issuer_url: "", client_id: "", client_secret: "", redirect_url: "", authorization_endpoint: "", token_endpoint: "", jwks_uri: "" })
  const [error, setError] = useState("")
  const [discovering, setDiscovering] = useState(false)

  useEffect(() => {
    if (!settings) return
    setEmail({ host: settings.email.host, port: settings.email.port || 587, username: settings.email.username, password: "", from: settings.email.from })
    setHCaptcha({ enabled: settings.hcaptcha.enabled, site_key: settings.hcaptcha.site_key, secret_key: "" })
    setOIDC({ enabled: settings.oidc.enabled, issuer_url: settings.oidc.issuer_url, client_id: settings.oidc.client_id, client_secret: "", redirect_url: settings.oidc.redirect_url, authorization_endpoint: settings.oidc.authorization_endpoint, token_endpoint: settings.oidc.token_endpoint, jwks_uri: settings.oidc.jwks_uri })
  }, [settings])

  async function save(key: "email" | "hcaptcha" | "oidc") {
    try {
      setError("")
      onSaved(await request("/api/v1/admin/site-settings", { method: "PATCH", body: JSON.stringify({ [key]: key === "email" ? email : key === "hcaptcha" ? hcaptcha : oidc }) }))
    } catch (err: any) { setError(err.message) }
  }

  async function discoverOIDC() {
    try {
      setError("")
      setDiscovering(true)
      const discovered = await request("/api/v1/admin/site-settings/oidc-discovery", { method: "POST", body: JSON.stringify({ issuer_url: oidc.issuer_url }) })
      setOIDC({ ...oidc, issuer_url: discovered.issuer_url, authorization_endpoint: discovered.authorization_endpoint, token_endpoint: discovered.token_endpoint, jwks_uri: discovered.jwks_uri })
    } catch (err: any) { setError(err.message) } finally { setDiscovering(false) }
  }

  const tabs: { id: SettingsTab; label: string }[] = [{ id: "account", label: "账户" }, { id: "email", label: "邮件服务" }, { id: "authentication", label: "登录认证" }]
  return <div className="content settings-content">
    <div className="settings-tabs" role="tablist" aria-label="站点设置">
      {tabs.map((tab) => <button key={tab.id} type="button" role="tab" aria-selected={activeTab === tab.id} className={activeTab === tab.id ? "selected" : ""} onClick={() => setActiveTab(tab.id)}>{tab.label}</button>)}
    </div>
    {activeTab === "account" && <section className="settings-grid settings-tab-panel" role="tabpanel"><article className="panel"><p className="eyebrow">CURRENT IDENTITY</p><h3>登录身份</h3><Detail label="邮箱" value={user?.email}/><Detail label="权限" value={roleName(user?.role || "")}/><Detail label="当前账户" value={account?.name}/><Detail label="商户号" value={account?.merchant_no}/></article></section>}
    {activeTab === "email" && <section className="settings-tab-panel" role="tabpanel"><ServiceCard title="邮件服务（SMTP）" save={() => save("email")}><Field label="SMTP 主机" value={email.host} set={(value) => setEmail({ ...email, host: value })}/><Field label="端口" value={String(email.port)} set={(value) => setEmail({ ...email, port: Number(value) || 0 })}/><Field label="用户名" value={email.username} set={(value) => setEmail({ ...email, username: value })}/><Field label="密码" value={email.password} secret placeholder={settings?.email.password_configured ? "已配置；留空保持不变" : ""} set={(value) => setEmail({ ...email, password: value })}/><Field label="发件人地址" value={email.from} set={(value) => setEmail({ ...email, from: value })}/></ServiceCard></section>}
    {activeTab === "authentication" && <section className="settings-auth-grid settings-tab-panel" role="tabpanel"><ServiceCard title="hCaptcha" save={() => save("hcaptcha")}><Toggle label="启用 hCaptcha" description="在登录表单中要求完成验证。" checked={hcaptcha.enabled} onCheckedChange={(enabled) => setHCaptcha({ ...hcaptcha, enabled })}/><Field label="Site Key" value={hcaptcha.site_key} set={(value) => setHCaptcha({ ...hcaptcha, site_key: value })}/><Field label="Secret Key" value={hcaptcha.secret_key} secret placeholder={settings?.hcaptcha.secret_key_configured ? "已配置；留空保持不变" : ""} set={(value) => setHCaptcha({ ...hcaptcha, secret_key: value })}/></ServiceCard><ServiceCard title="OIDC 认证服务" save={() => save("oidc")}><Toggle label="启用 OIDC 登录" description="使用外部身份提供方进行登录。" checked={oidc.enabled} onCheckedChange={(enabled) => setOIDC({ ...oidc, enabled })}/><Label className="form-label">Issuer URL<div className="discovery-input"><Input value={oidc.issuer_url} placeholder="https://id.example.com" onChange={(event) => setOIDC({ ...oidc, issuer_url: event.target.value })}/><button type="button" className="secondary-button discovery-button" onClick={discoverOIDC} disabled={discovering || !oidc.issuer_url.trim()}><Search size={14}/>{discovering ? "发现中" : "自动发现"}</button></div></Label><Field label="Client ID" value={oidc.client_id} set={(value) => setOIDC({ ...oidc, client_id: value })}/><Field label="Client Secret" value={oidc.client_secret} secret placeholder={settings?.oidc.client_secret_configured ? "已配置；留空保持不变" : ""} set={(value) => setOIDC({ ...oidc, client_secret: value })}/><Field label="回调地址" value={oidc.redirect_url} set={(value) => setOIDC({ ...oidc, redirect_url: value })}/><DiscoveredEndpoints oidc={oidc}/></ServiceCard></section>}
    {error && <p className="form-error">{error}</p>}
  </div>
}

function Field({ label, value, set, secret, placeholder }: { label: string; value: string; set: (value: string) => void; secret?: boolean; placeholder?: string }) { return <Label className="form-label">{label}<Input type={secret ? "password" : "text"} value={value} placeholder={placeholder} onChange={(event) => set(event.target.value)} /></Label> }
function Toggle({ label, description, checked, onCheckedChange }: { label: string; description: string; checked: boolean; onCheckedChange: (checked: boolean) => void }) { return <div className="settings-toggle"><div><b>{label}</b><p>{description}</p></div><Switch checked={checked} onCheckedChange={onCheckedChange} aria-label={label}/></div> }
function DiscoveredEndpoints({ oidc }: { oidc: OIDCDraft }) { if (!oidc.authorization_endpoint && !oidc.token_endpoint && !oidc.jwks_uri) return null; return <div className="discovery-result"><p className="eyebrow">DISCOVERED ENDPOINTS</p><Detail label="授权端点" value={oidc.authorization_endpoint || "未提供"}/><Detail label="令牌端点" value={oidc.token_endpoint || "未提供"}/><Detail label="JWKS 地址" value={oidc.jwks_uri || "未提供"}/></div> }
function ServiceCard({ title, children, save }: { title: string; children: React.ReactNode; save: () => void }) { return <article className="panel settings-service-card"><p className="eyebrow">SITE SERVICE</p><h3>{title}</h3>{children}<button className="primary-button" onClick={save}>保存配置</button></article> }
