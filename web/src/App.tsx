import { useEffect, useMemo, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import "./index.css";

type View = "overview" | "bills" | "channels" | "tenants" | "developers" | "settings";
type Tenant = { id: string; name: string; merchant_no: string; status: string; created_at: string };
type Bill = { id: string; platform_order_no: string; merchant_order_no: string; subject: string; amount: string; provider: string; scene: string; status: string; created_at: string; paid_at?: string };
type Channel = { id: string; provider: string; display_name: string; enabled: boolean; configured: boolean; webhook_url: string; updated_at: string };
type Dashboard = { total_bills: number; pending_bills: number; paid_bills: number; refunded_bills: number; paid_volume: string };

const API = import.meta.env.VITE_API_BASE_URL || "";
const statusName: Record<string, string> = { pending: "待支付", paid: "已支付", closed: "已关闭", failed: "失败", refunding: "退款中", refunded: "已退款", active: "正常", suspended: "已暂停" };
const providerName: Record<string, string> = { alipay: "支付宝", wechat: "微信支付" };

export default function App() {
  const [token, setToken] = useState(localStorage.getItem("tsumugi_access_token") || "");
  const [view, setView] = useState<View>("overview");
  const [user, setUser] = useState<any>(null);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [tenantId, setTenantId] = useState(localStorage.getItem("tsumugi_tenant_id") || "");
  const [dashboard, setDashboard] = useState<Dashboard | null>(null);
  const [bills, setBills] = useState<Bill[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const activeTenant = useMemo(() => tenants.find((item) => item.id === tenantId), [tenants, tenantId]);
  const isPlatformAdmin = user?.role === "platform_admin";

  async function request(path: string, options: RequestInit = {}) {
    const headers = new Headers(options.headers);
    headers.set("Content-Type", "application/json");
    if (token) headers.set("Authorization", `Bearer ${token}`);
    if (tenantId) headers.set("X-Tenant-ID", tenantId);
    const response = await fetch(`${API}${path}`, { ...options, headers });
    const body = response.status === 204 ? null : await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.message || "请求失败，请稍后重试");
    return body;
  }

  async function loadInitial() {
    setLoading(true); setError("");
    try {
      const me = await request("/api/v1/admin/me");
      setUser(me);
      if (me.role === "platform_admin") {
        const data = await request("/api/v1/admin/tenants");
        setTenants(data.items || []);
        if (!tenantId && data.items?.[0]?.id) { setTenantId(data.items[0].id); localStorage.setItem("tsumugi_tenant_id", data.items[0].id); }
      } else if (me.tenant_id) {
        setTenantId(me.tenant_id);
      }
    } catch (e: any) { setError(e.message); if (String(e.message).includes("access token")) logout(); }
    finally { setLoading(false); }
  }

  async function loadContent() {
    if (!token) return;
    setLoading(true); setError("");
    try {
      if (view === "overview") setDashboard(await request("/api/v1/admin/dashboard"));
      if (view === "bills") setBills((await request("/api/v1/admin/bills")).items || []);
      if (view === "channels" && tenantId) setChannels((await request("/api/v1/admin/channels")).items || []);
      if (view === "tenants" && isPlatformAdmin) setTenants((await request("/api/v1/admin/tenants")).items || []);
    } catch (e: any) { setError(e.message); }
    finally { setLoading(false); }
  }

  useEffect(() => { if (token) loadInitial(); }, [token]);
  useEffect(() => { loadContent(); }, [view, tenantId, token]);

  function logout() { localStorage.removeItem("tsumugi_access_token"); localStorage.removeItem("tsumugi_tenant_id"); setToken(""); setUser(null); }
  function chooseTenant(id: string) { setTenantId(id); localStorage.setItem("tsumugi_tenant_id", id); }

  if (!token) return <Login onSuccess={(value) => { localStorage.setItem("tsumugi_access_token", value); setToken(value); }} />;

  const nav: { key: View; label: string; icon: string; admin?: boolean }[] = [
    { key: "overview", label: "工作台", icon: "⌘" },
    { key: "bills", label: "账单中心", icon: "▤" },
    { key: "channels", label: "支付通道", icon: "◈" },
    { key: "tenants", label: "租户管理", icon: "◫", admin: true },
    { key: "developers", label: "开发者接入", icon: "</>" },
    { key: "settings", label: "系统设置", icon: "⚙" },
  ];

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><span className="brand-mark">T</span><span>Tsumugi <b>Pay</b></span></div>
      <div className="workspace-label">支付运营中心</div>
      <nav>{nav.filter((item) => !item.admin || isPlatformAdmin).map((item) => <button key={item.key} className={`nav-item ${view === item.key ? "active" : ""}`} onClick={() => setView(item.key)}><span className="nav-icon">{item.icon}</span>{item.label}</button>)}</nav>
      <div className="sidebar-spacer" />
      <div className="security-card"><span className="status-dot" /> 官方通道直连<br/><small>密钥以加密方式存储</small></div>
      <button className="profile" onClick={logout}><span className="avatar">{(user?.email || "A").slice(0, 1).toUpperCase()}</span><span><b>{user?.email || "管理员"}</b><small>{user?.role === "platform_admin" ? "平台管理员" : "租户管理员"}</small></span><span className="chevron">⌄</span></button>
    </aside>
    <main className="main">
      <header className="topbar">
        <div><p className="eyebrow">TSUMUGI PAY / {view.toUpperCase()}</p><h1>{titleFor(view)}</h1></div>
        <div className="top-actions">
          {isPlatformAdmin && <select value={tenantId} onChange={(event) => chooseTenant(event.target.value)} className="tenant-select"><option value="">全部租户</option>{tenants.map((item) => <option value={item.id} key={item.id}>{item.name} · {item.merchant_no}</option>)}</select>}
          <button className="icon-button" title="刷新" onClick={loadContent}>↻</button>
        </div>
      </header>
      {error && <div className="alert"><b>操作提示：</b>{error}<button onClick={() => setError("")}>×</button></div>}
      {loading && <div className="loading-line" />}
      {view === "overview" && <Overview data={dashboard} bills={bills} setView={setView} />}
      {view === "bills" && <Bills data={bills} onRefresh={loadContent} request={request} />}
      {view === "channels" && <Channels data={channels} tenant={activeTenant} onRefresh={loadContent} request={request} />}
      {view === "tenants" && <Tenants data={tenants} onRefresh={loadContent} request={request} />}
      {view === "developers" && <Developers tenant={activeTenant} />}
      {view === "settings" && <Settings user={user} tenant={activeTenant} />}
    </main>
  </div>;
}

function Login({ onSuccess }: { onSuccess: (token: string) => void }) {
  const [email, setEmail] = useState("admin@tsumugi.local"); const [password, setPassword] = useState("ChangeMe123!"); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent) { event.preventDefault(); setBusy(true); setError(""); try { const response = await fetch(`${API}/api/v1/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email, password }) }); const result = await response.json(); if (!response.ok) throw new Error(result.message || "登录失败"); onSuccess(result.access_token); } catch (e: any) { setError(e.message); } finally { setBusy(false); } }
  return <div className="login-page"><div className="login-panel"><div className="brand login-brand"><span className="brand-mark">T</span><span>Tsumugi <b>Pay</b></span></div><div className="login-copy"><p className="eyebrow">OPEN PAYMENT OPERATIONS</p><h1>掌控每一笔<br/><em>可信交易。</em></h1><p>为多租户业务而设计的统一支付运营平台，连接支付宝与微信支付官方能力。</p></div><form onSubmit={submit}><label>邮箱<input value={email} onChange={(e) => setEmail(e.target.value)} type="email" autoComplete="email" /></label><label>密码<input value={password} onChange={(e) => setPassword(e.target.value)} type="password" autoComplete="current-password" /></label>{error && <p className="form-error">{error}</p>}<button className="primary-button full" disabled={busy}>{busy ? "正在验证…" : "进入支付运营中心"} <span>→</span></button></form><p className="demo-note">开发初始化账号已预填。首次进入生产环境前，请立即轮换默认凭据。</p></div><div className="login-art"><div className="orb orb-one"/><div className="orb orb-two"/><div className="transaction-card"><span>SETTLEMENT / LIVE</span><b>¥ 1,248,602.80</b><small>今日成功交易总额</small><div className="mini-bars"><i/><i/><i/><i/><i/><i/><i/></div></div><p>值得信赖的资金流，<br/>始于清晰的每一步。</p></div></div>;
}

function Overview({ data, bills, setView }: { data: Dashboard | null; bills: Bill[]; setView: (view: View) => void }) {
  const metrics = [{ label: "支付成功金额", value: `¥ ${data?.paid_volume || "0.00"}`, note: "已结算订单累计", accent: "mint" }, { label: "成功账单", value: data?.paid_bills ?? "—", note: "支付成功的订单数", accent: "blue" }, { label: "待支付账单", value: data?.pending_bills ?? "—", note: "等待用户完成付款", accent: "amber" }, { label: "退款账单", value: data?.refunded_bills ?? "—", note: "已完成退款的订单", accent: "violet" }];
  return <div className="content"><section className="hero"><div><p className="eyebrow">支付运营概览</p><h2>让每一笔交易<br/>都有迹可循。</h2><p>统一观察租户、支付通道与账单状态，及时处理异常与对账动作。</p><button className="primary-button" onClick={() => setView("bills")}>查看账单中心 <span>→</span></button></div><div className="hero-graphic"><div className="ring ring-outer"/><div className="ring ring-inner"/><div className="hero-center"><b>{data?.paid_bills || 0}</b><small>成功交易</small></div><span className="float-tag tag-one">支付宝 <b>已连接</b></span><span className="float-tag tag-two">微信支付 <b>待配置</b></span></div></section><section className="metric-grid">{metrics.map((item) => <article className={`metric-card ${item.accent}`} key={item.label}><div className="metric-head"><span>{item.label}</span><i>↗</i></div><strong>{item.value}</strong><small>{item.note}</small><div className="metric-rule" /></article>)}</section><section className="two-columns"><article className="panel chart-panel"><div className="panel-head"><div><p className="eyebrow">VOLUME</p><h3>收款趋势</h3></div><span className="period">最近 7 天</span></div><div className="chart"><div className="chart-labels"><span>¥ 1.5k</span><span>¥ 1.0k</span><span>¥ 0.5k</span><span>¥ 0</span></div><div className="chart-plot"><svg viewBox="0 0 560 220" preserveAspectRatio="none"><defs><linearGradient id="fill" x1="0" x2="0" y1="0" y2="1"><stop stopColor="#37d1b6" stopOpacity=".36"/><stop offset="1" stopColor="#37d1b6" stopOpacity="0"/></linearGradient></defs><path d="M0 180 C40 150, 56 168, 92 139 S154 162, 192 99 S259 135, 298 107 S359 127, 393 58 S450 92, 492 44 S529 76, 560 28 L560 220 L0 220Z" fill="url(#fill)"/><path d="M0 180 C40 150, 56 168, 92 139 S154 162, 192 99 S259 135, 298 107 S359 127, 393 58 S450 92, 492 44 S529 76, 560 28" fill="none" stroke="#2fae9a" strokeWidth="3"/></svg><div className="days"><span>周一</span><span>周二</span><span>周三</span><span>周四</span><span>周五</span><span>周六</span><span>周日</span></div></div></div></article><article className="panel recent-panel"><div className="panel-head"><div><p className="eyebrow">LATEST BILLS</p><h3>近期账单</h3></div><button className="text-button" onClick={() => setView("bills")}>全部账单 →</button></div>{bills.length ? <div className="compact-list">{bills.slice(0, 4).map((bill) => <div className="compact-row" key={bill.id}><span className={`provider-icon ${bill.provider}`}>{bill.provider === "alipay" ? "支" : "微"}</span><span><b>{bill.subject}</b><small>{bill.merchant_order_no}</small></span><span><b>¥ {bill.amount}</b><small className={`status ${bill.status}`}>{statusName[bill.status] || bill.status}</small></span></div>)}</div> : <Empty icon="▧" title="尚无可展示的账单" copy="完成通道配置并由业务系统创建订单后，交易将在这里出现。" />}</article></section></div>;
}

function Bills({ data, onRefresh, request }: { data: Bill[]; onRefresh: () => void; request: (path: string, options?: RequestInit) => Promise<any> }) {
  const [filter, setFilter] = useState("all"); const [selected, setSelected] = useState<Bill | null>(null); const visible = filter === "all" ? data : data.filter((item) => item.status === filter);
  async function close(bill: Bill) { if (!confirm(`确认关闭订单 ${bill.merchant_order_no}？`)) return; try { await request(`/api/v1/admin/bills/${bill.id}/close`, { method: "POST" }); onRefresh(); } catch (e: any) { alert(e.message); } }
  async function refund(bill: Bill) { const amount = prompt("退款金额（元）", bill.amount); if (!amount) return; const no = prompt("退款单号", `RF${Date.now()}`); if (!no) return; try { await request(`/api/v1/admin/bills/${bill.id}/refunds`, { method: "POST", body: JSON.stringify({ refund_order_no: no, amount, reason: "后台发起退款" }) }); onRefresh(); } catch (e: any) { alert(e.message); } }
  return <div className="content"><section className="page-actions"><div className="segmented">{[["all", "全部"], ["pending", "待支付"], ["paid", "已支付"], ["refunded", "已退款"], ["failed", "失败"]].map(([key,label]) => <button onClick={() => setFilter(key)} className={filter === key ? "selected" : ""} key={key}>{label}</button>)}</div><button className="secondary-button" onClick={onRefresh}>↻ 刷新数据</button></section><section className="panel table-panel"><div className="table-wrap"><table><thead><tr><th>账单信息</th><th>支付通道</th><th>金额</th><th>支付状态</th><th>创建时间</th><th /></tr></thead><tbody>{visible.map((bill) => <tr key={bill.id}><td><b>{bill.subject}</b><small>{bill.merchant_order_no}<br/>{bill.platform_order_no}</small></td><td><span className={`provider-pill ${bill.provider}`}>{providerName[bill.provider] || bill.provider}</span><small>{bill.scene}</small></td><td className="amount">¥ {bill.amount}</td><td><span className={`status ${bill.status}`}>{statusName[bill.status] || bill.status}</span></td><td>{formatDate(bill.created_at)}</td><td className="row-actions"><button onClick={() => setSelected(bill)}>详情</button>{bill.status === "pending" && <button onClick={() => close(bill)}>关闭</button>}{bill.status === "paid" && <button onClick={() => refund(bill)}>退款</button>}</td></tr>)}</tbody></table>{!visible.length && <Empty icon="▤" title="暂无符合条件的账单" copy="订单由开放支付接口创建，支持标准 OPS 字段和易支付兼容字段。" />}</div></section>{selected && <Modal title="账单详情" onClose={() => setSelected(null)}><Detail label="平台订单号" value={selected.platform_order_no}/><Detail label="商户订单号" value={selected.merchant_order_no}/><Detail label="交易金额" value={`¥ ${selected.amount}`}/><Detail label="支付方式" value={`${providerName[selected.provider]} / ${selected.scene}`}/><Detail label="状态" value={statusName[selected.status]}/><Detail label="创建时间" value={formatDate(selected.created_at)}/></Modal>}</div>;
}

function Channels({ data, tenant, onRefresh, request }: { data: Channel[]; tenant?: Tenant; onRefresh: () => void; request: (path: string, options?: RequestInit) => Promise<any> }) {
  const [editing, setEditing] = useState<Channel | null>(null);
  if (!tenant) return <div className="content"><Empty icon="◫" title="请先选择一个租户" copy="平台管理员可从页面顶部切换当前需要配置的租户。" /></div>;
  return <div className="content"><section className="tenant-banner"><div><p className="eyebrow">当前租户</p><h3>{tenant.name}</h3><span>商户号 {tenant.merchant_no}</span></div><div><span className={`status ${tenant.status}`}>{statusName[tenant.status]}</span><small>支付密钥不会在界面回显</small></div></section><section className="channel-grid">{data.map((channel) => <article className="channel-card" key={channel.id}><div className="channel-card-top"><span className={`large-provider ${channel.provider}`}>{channel.provider === "alipay" ? "支" : "微"}</span><span className={`status ${channel.enabled ? "paid" : "pending"}`}>{channel.enabled ? "已启用" : "未启用"}</span></div><h3>{channel.display_name}</h3><p>{channel.provider === "alipay" ? "网页支付与手机网站支付，使用 RSA2 签名和异步通知验签。" : "Native、H5 场景，使用微信支付 API v3 请求签名与通知解密。"}</p><div className="channel-meta"><span><i className={channel.configured ? "green" : "gray"}/> {channel.configured ? "已保存凭据" : "尚未配置"}</span><button className="text-button" onClick={() => setEditing(channel)}>配置通道 →</button></div></article>)}</section><section className="panel webhook-panel"><div><div><p className="eyebrow">WEBHOOK</p><h3>官方异步通知</h3><p>将每个通道对应的回调 URL 填入服务商控制台。验签、幂等处理和商户通知重试均由后端完成。</p></div><span className="webhook-lock">● 已启用签名验真</span></div></section>{editing && <ChannelModal channel={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); onRefresh(); }} request={request}/>}</div>;
}

function ChannelModal({ channel, onClose, onSaved, request }: { channel: Channel; onClose: () => void; onSaved: () => void; request: (path: string, options?: RequestInit) => Promise<any> }) {
  const [name, setName] = useState(channel.display_name); const [enabled, setEnabled] = useState(channel.enabled); const [config, setConfig] = useState(channel.provider === "alipay" ? '{\n  "alipay": {\n    "app_id": "",\n    "app_private_key_pem": "",\n    "alipay_public_key_pem": ""\n  }\n}' : '{\n  "wechat": {\n    "mch_id": "",\n    "app_id": "",\n    "merchant_serial_no": "",\n    "merchant_private_key_pem": "",\n    "api_v3_key": "",\n    "platform_public_key_pem": ""\n  }\n}'); const [error, setError] = useState(""); const [saving, setSaving] = useState(false);
  async function save() { setSaving(true); setError(""); try { const parsed = JSON.parse(config); await request(`/api/v1/admin/channels/${channel.id}`, { method: "PATCH", body: JSON.stringify({ display_name: name, enabled, config: parsed }) }); onSaved(); } catch (e: any) { setError(e.message); } finally { setSaving(false); } }
  return <Modal title={`配置${providerName[channel.provider]}`} onClose={onClose}><p className="modal-intro">通道密钥会先在浏览器提交，再以 AES-GCM 加密方式写入服务端。为安全起见，已保存值不会回显。</p><label className="form-label">展示名称<input value={name} onChange={(e) => setName(e.target.value)}/></label><label className="form-label config-label">官方凭据 JSON<textarea value={config} onChange={(e) => setConfig(e.target.value)} rows={13}/></label><div className="webhook-copy"><b>回调地址</b><code>{channel.webhook_url}</code></div><label className="switch"><input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)}/><span />完成配置后启用此通道</label>{error && <p className="form-error">{error}</p>}<button className="primary-button full" onClick={save} disabled={saving}>{saving ? "保存中…" : "安全保存通道配置"} <span>→</span></button></Modal>;
}

function Tenants({ data, onRefresh, request }: { data: Tenant[]; onRefresh: () => void; request: (path: string, options?: RequestInit) => Promise<any> }) {
  const [open, setOpen] = useState(false);
  return <div className="content"><section className="page-actions"><div><p className="eyebrow">MULTI-TENANT</p><h2>租户与商户隔离</h2></div><button className="primary-button" onClick={() => setOpen(true)}>创建租户 <span>＋</span></button></section><section className="panel table-panel"><table><thead><tr><th>租户名称</th><th>商户号</th><th>状态</th><th>创建时间</th><th /></tr></thead><tbody>{data.map((tenant) => <tr key={tenant.id}><td><b>{tenant.name}</b><small>{tenant.id}</small></td><td className="mono">{tenant.merchant_no}</td><td><span className={`status ${tenant.status}`}>{statusName[tenant.status]}</span></td><td>{formatDate(tenant.created_at)}</td><td className="row-actions"><button onClick={async () => { await request(`/api/v1/admin/tenants/${tenant.id}`, { method: "PATCH", body: JSON.stringify({ status: tenant.status === "active" ? "suspended" : "active" }) }); onRefresh(); }}>{tenant.status === "active" ? "暂停" : "恢复"}</button></td></tr>)}</tbody></table></section>{open && <TenantModal request={request} onClose={() => setOpen(false)} onSaved={() => { setOpen(false); onRefresh(); }}/>}</div>;
}
function TenantModal({ request, onClose, onSaved }: { request: (path: string, options?: RequestInit) => Promise<any>; onClose: () => void; onSaved: () => void }) { const [name,setName]=useState("");const [merchant,setMerchant]=useState("");const [api,setApi]=useState("");const [callback,setCallback]=useState("");const [error,setError]=useState("");async function save(){try{await request("/api/v1/admin/tenants",{method:"POST",body:JSON.stringify({name,merchant_no:merchant,api_secret:api,callback_secret:callback})});onSaved()}catch(e:any){setError(e.message)}}return <Modal title="创建租户" onClose={onClose}><p className="modal-intro">每个租户拥有独立商户号、开放支付签名密钥、回调签名密钥和支付通道配置。</p><label className="form-label">租户名称<input value={name} onChange={e=>setName(e.target.value)}/></label><label className="form-label">商户号<input value={merchant} onChange={e=>setMerchant(e.target.value)} placeholder="例如 100001"/></label><label className="form-label">请求签名密钥<input value={api} onChange={e=>setApi(e.target.value)} type="password"/></label><label className="form-label">回调签名密钥<input value={callback} onChange={e=>setCallback(e.target.value)} type="password"/></label>{error&&<p className="form-error">{error}</p>}<button className="primary-button full" onClick={save}>创建并初始化通道 <span>→</span></button></Modal> }

function Developers({ tenant }: { tenant?: Tenant }) { const merchant = tenant?.merchant_no || "{merchant_no}"; return <div className="content"><section className="developer-hero"><p className="eyebrow">OPEN PAYMENT SPECIFICATION 1.0</p><h2>兼容标准，也保留扩展。</h2><p>Tsumugi Pay 同时提供易支付兼容层与 JSON 管理 API。支付结果应只以异步通知或查询结果为准。</p></section><section className="developer-grid"><article className="panel code-panel"><div className="panel-head"><h3>1. 发现平台能力</h3><span>GET</span></div><pre><code>{`GET ${API}/.well-known/openpayment-configuation\n\n# 返回支付方式、端点、签名与字段别名`}</code></pre></article><article className="panel code-panel"><div className="panel-head"><h3>2. 创建支付订单</h3><span>POST</span></div><pre><code>{`POST ${API}/mapi.php\nContent-Type: application/json\n\n{\n  "merchant_id": "${merchant}",\n  "payment_method": "alipay",\n  "merchant_order_no": "ORDER-001",\n  "subject": "示例订单",\n  "amount": "9.90",\n  "notify_url": "https://merchant.example.com/notify",\n  "sign_type": "HMAC-SHA256",\n  "sign": "..."\n}`}</code></pre></article><article className="panel table-panel endpoint-table"><table><thead><tr><th>端点</th><th>用途</th><th>鉴权</th></tr></thead><tbody><tr><td><code>POST /api/v1/auth/login</code></td><td>后台登录</td><td>公开</td></tr><tr><td><code>GET /api/v1/admin/bills</code></td><td>账单列表</td><td>Bearer JWT</td></tr><tr><td><code>PATCH /api/v1/admin/channels/:id</code></td><td>官方通道密钥与启停</td><td>租户范围</td></tr><tr><td><code>POST /api/v1/admin/bills/:id/refunds</code></td><td>退款</td><td>租户范围</td></tr><tr><td><code>POST /api/v1/webhooks/:provider/:token</code></td><td>官方支付结果通知</td><td>官方签名</td></tr></tbody></table></article></section></div> }
function Settings({ user, tenant }: { user: any; tenant?: Tenant }) { return <div className="content"><section className="settings-grid"><article className="panel"><p className="eyebrow">CURRENT SESSION</p><h3>登录身份</h3><Detail label="邮箱" value={user?.email || "—"}/><Detail label="角色" value={user?.role === "platform_admin" ? "平台管理员" : user?.role}/><Detail label="操作租户" value={tenant?.name || "全局视角"}/></article><article className="panel"><p className="eyebrow">SECURITY BASELINE</p><h3>安全基线</h3><ul className="check-list"><li><i>✓</i> 通道密钥 AES-GCM 加密存储</li><li><i>✓</i> JWT 后台会话及租户作用域</li><li><i>✓</i> 支付回调官方签名校验</li><li><i>✓</i> 账单状态机及通知幂等</li><li><i>✓</i> 操作审计日志</li></ul></article></section></div> }
function Detail({ label, value }: { label: string; value: any }) { return <div className="detail"><span>{label}</span><b>{value || "—"}</b></div> }
function Modal({ title, children, onClose }: { title: string; children: ReactNode; onClose: () => void }) { return <div className="modal-layer" onMouseDown={onClose}><section className="modal" onMouseDown={(e) => e.stopPropagation()}><header><h3>{title}</h3><button onClick={onClose}>×</button></header>{children}</section></div> }
function Empty({ icon, title, copy }: { icon: string; title: string; copy: string }) { return <div className="empty"><span>{icon}</span><h3>{title}</h3><p>{copy}</p></div> }
function titleFor(view: View) { return ({ overview: "工作台", bills: "账单中心", channels: "支付通道", tenants: "租户管理", developers: "开发者接入", settings: "系统设置" })[view] }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—" }
