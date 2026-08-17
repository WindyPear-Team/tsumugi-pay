import { useEffect, useState } from "react"
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom"

import "./index.css"
import { Sidebar, SidebarContent, SidebarFooter, SidebarMenu, SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { API } from "@/lib/api"
import { AuditPage, DevelopersPage, RefundsPage, SettingsPage, UsersPage } from "@/pages/ManagementPages"
import { LoginPage, SetupPage } from "@/pages/AuthPages"
import { DashboardPage } from "@/pages/DashboardPage"
import { BillsPage } from "@/pages/BillsPage"
import { ChannelsPage } from "@/pages/ChannelsPage"
import type { ApiRequest, AuditLog, Bill, Channel, Dashboard, Refund, SiteSettings, PublicSiteConfig, ManagedUser, Account, View } from "@/types"

const viewPaths: Record<View, string> = { overview: "/", bills: "/bills", refunds: "/refunds", channels: "/channels", audit: "/audit-logs", developers: "/developers", users: "/users", settings: "/settings" }
const titles: Record<View, string> = { overview: "工作台", bills: "账单中心", refunds: "退款管理", channels: "支付通道", audit: "审计日志", developers: "开发者接入", users: "用户管理", settings: "站点设置" }
function viewFor(pathname: string): View { return (Object.entries(viewPaths).find(([, path]) => path === pathname)?.[0] as View | undefined) || "overview" }

export default function App() {
  const location = useLocation()
  const navigate = useNavigate()
  const [token, setToken] = useState(localStorage.getItem("tsumugi_access_token") || "")
  const [setupRequired, setSetupRequired] = useState<boolean | null>(null)
  const [setupEmail, setSetupEmail] = useState("")
  const [user, setUser] = useState<any>(null)
  const [account, setAccount] = useState<Account | null>(null)
  const [dashboard, setDashboard] = useState<Dashboard | null>(null)
  const [bills, setBills] = useState<Bill[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [refunds, setRefunds] = useState<Refund[]>([])
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([])
  const [siteSettings, setSiteSettings] = useState<SiteSettings | null>(null)
  const [siteConfig, setSiteConfig] = useState<PublicSiteConfig | null>(null)
  const [managedUsers, setManagedUsers] = useState<ManagedUser[]>([])
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const view = viewFor(location.pathname)
  const pageTitle = !token ? location.pathname === "/setup" ? "初始化" : "登录" : titles[view]
  const activeAccount = account || undefined
  const canManagePayments = user?.role === "user"
  const isPlatformAdmin = user?.role === "platform_admin"
  const request: ApiRequest = async (path, options = {}) => {
    const headers = new Headers(options.headers)
    headers.set("Content-Type", "application/json")
    if (token) headers.set("Authorization", `Bearer ${token}`)
    const response = await fetch(`${API}${path}`, { ...options, headers })
    const body = response.status === 204 ? null : await response.json().catch(() => ({}))
    if (!response.ok) throw new Error(body.message || "请求失败，请稍后重试")
    return body
  }
  function logout() { localStorage.removeItem("tsumugi_access_token"); setToken(""); setUser(null); setAccount(null) }
  async function loadInitial() { try { const me = await request("/api/v1/admin/me"); setUser(me); setAccount(me.account || null) } catch (err: any) { setError(err.message); logout() } }
  async function loadPage() {
    if (!token) return
    setLoading(true); setError("")
    try {
      if (view === "overview") { const [summary, list] = await Promise.all([request("/api/v1/admin/dashboard"), request("/api/v1/admin/bills")]); setDashboard(summary); setBills(list.items || []) }
      if (view === "bills") setBills((await request("/api/v1/admin/bills")).items || [])
      if (view === "refunds") setRefunds((await request("/api/v1/admin/refunds")).items || [])
      if (view === "channels") setChannels((await request("/api/v1/admin/channels")).items || [])
      if (view === "audit") setAuditLogs((await request("/api/v1/admin/audit-logs")).items || [])
      if (view === "users" && isPlatformAdmin) setManagedUsers((await request("/api/v1/admin/users")).items || [])
      if (view === "settings" && isPlatformAdmin) setSiteSettings(await request("/api/v1/admin/site-settings"))
    } catch (err: any) { setError(err.message) } finally { setLoading(false) }
  }
  useEffect(() => { fetch(`${API}/api/v1/site-config`).then((response) => response.ok ? response.json() : null).then(setSiteConfig).catch(() => setSiteConfig(null)) }, [])
  useEffect(() => { if (siteConfig) document.title = `${pageTitle} - ${siteConfig.site_name}` }, [pageTitle, siteConfig])
  useEffect(() => { if (token) loadInitial(); else fetch(`${API}/api/v1/setup/status`).then((response) => response.ok ? response.json() : { required: false }).then((body) => setSetupRequired(Boolean(body.required))).catch(() => setSetupRequired(false)) }, [token])
  useEffect(() => { loadPage() }, [view, token])
  if (!token && setupRequired === null) return <div className="login-page"><div className="login-panel"><p className="demo-note">正在检查系统初始化状态…</p></div></div>
  if (!token && setupRequired && location.pathname !== "/setup") return <Navigate to="/setup" replace />
  if (!token && !setupRequired && location.pathname !== "/login") return <Navigate to="/login" replace />
  if (!token && location.pathname === "/setup") return <SetupPage onCompleted={(email) => { setSetupEmail(email); setSetupRequired(false); navigate("/login", { replace: true }) }} />
  if (!token) return <LoginPage initialEmail={setupEmail} site={siteConfig} onSuccess={(value) => { localStorage.setItem("tsumugi_access_token", value); setToken(value); navigate("/", { replace: true }) }} />
  if (location.pathname === "/login" || location.pathname === "/setup") return <Navigate to="/" replace />
  const nav: { key: View; label: string; icon: string }[] = [{ key: "overview", label: "工作台", icon: "⌘" }, { key: "bills", label: "账单中心", icon: "▤" }, { key: "refunds", label: "退款管理", icon: "↩" }, { key: "channels", label: "支付通道", icon: "◈" }, { key: "audit", label: "审计日志", icon: "☷" }, { key: "developers", label: "开发者接入", icon: "</>" }, ...(isPlatformAdmin ? [{ key: "users" as View, label: "用户管理", icon: "♙" }, { key: "settings" as View, label: "站点设置", icon: "⚙" }] : [])]
  return <SidebarProvider><Sidebar className="sidebar" collapsible="icon"><div className="brand"><span className="brand-mark">T</span><span>{siteConfig?.site_name || "Tsumugi Pay"}</span></div><div className="workspace-label">支付运营中心</div><SidebarContent><SidebarMenu>{nav.map((item) => <SidebarMenuItem key={item.key}><SidebarMenuButton className="nav-item" isActive={view === item.key} onClick={() => navigate(viewPaths[item.key])}><span className="nav-icon">{item.icon}</span>{item.label}</SidebarMenuButton></SidebarMenuItem>)}</SidebarMenu></SidebarContent><SidebarFooter><button className="profile" onClick={logout}><span className="avatar">{(user?.email || "A")[0].toUpperCase()}</span><span><b>{user?.email || "用户"}</b><small>退出登录</small></span></button></SidebarFooter></Sidebar><main className="main"><header className="topbar"><div><p className="eyebrow">{siteConfig?.site_name || "TSUMUGI PAY"} / {view.toUpperCase()}</p><h1>{titles[view]}</h1></div><div className="top-actions"><SidebarTrigger className="mobile-sidebar-trigger"/><button className="icon-button" onClick={loadPage} aria-label="刷新页面">↻</button></div></header>{error && <div className="alert"><b>操作提示：</b>{error}</div>}{loading && <div className="loading-line"/>}<Routes><Route path="/" element={<DashboardPage data={dashboard} bills={bills} navigate={(next) => navigate(viewPaths[next])}/>}/><Route path="/bills" element={<BillsPage data={bills} canManage={canManagePayments || isPlatformAdmin} request={request} onRefresh={loadPage}/>}/><Route path="/refunds" element={<RefundsPage data={refunds}/>}/><Route path="/channels" element={<ChannelsPage data={channels} account={activeAccount} canManage={canManagePayments || isPlatformAdmin} request={request} onRefresh={loadPage}/>}/><Route path="/audit-logs" element={<AuditPage data={auditLogs}/>}/><Route path="/developers" element={<DevelopersPage account={activeAccount}/>}/><Route path="/users" element={isPlatformAdmin ? <UsersPage data={managedUsers} request={request} onRefresh={loadPage}/> : <Navigate to="/" replace/>}/><Route path="/settings" element={isPlatformAdmin ? <SettingsPage user={user} account={activeAccount} settings={siteSettings} request={request} onSaved={(saved) => { setSiteSettings(saved); setSiteConfig(saved.site) }}/> : <Navigate to="/" replace/>}/><Route path="*" element={<Navigate to="/" replace/>}/></Routes></main></SidebarProvider>
}
