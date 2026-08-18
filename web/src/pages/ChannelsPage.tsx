import { useState } from "react"
import { Settings, Trash2 } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Empty, Modal } from "@/components/page-ui"
import { providerName } from "@/lib/api"
import type { ApiRequest, Channel, Account } from "@/types"

export function ChannelsPage({ data, account, canManage, request, onRefresh }: { data: Channel[]; account?: Account; canManage: boolean; request: ApiRequest; onRefresh: () => void }) {
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Channel | null>(null)
  const [query, setQuery] = useState("")
  const [page, setPage] = useState(1)
  const filtered = data.filter((channel) => `${channel.display_name} ${channel.provider} ${channel.priority} ${channel.weight}`.toLowerCase().includes(query.toLowerCase()))
  const pageCount = Math.max(1, Math.ceil(filtered.length / 10))
  const current = Math.min(page, pageCount)
  const visible = filtered.slice((current - 1) * 10, current * 10)
  async function remove(channel: Channel) {
    if (!confirm(`确认删除支付通道“${channel.display_name}”？`)) return
    await request(`/api/v1/admin/channels/${channel.id}`, { method: "DELETE" })
    onRefresh()
  }
  if (!account)
    return (
      <div className="content">
        <Empty icon="◈" title="用户不可用" copy="请重新登录后再配置支付通道。" />
      </div>
    )
  return (
    <div className="content">
      <section className="account-banner">
        <div>
          <p className="eyebrow">我的账户</p>
          <h3>{account.name}</h3>
          <span>商户号 {account.merchant_no}</span>
        </div>
        {canManage && (
          <button className="primary-button" onClick={() => setAdding(true)}>
            新增通道
          </button>
        )}
      </section>
      <section className="panel table-panel">
        <div className="table-tools">
          <Input
            value={query}
            placeholder="搜索通道名称或服务商"
            onChange={(event) => {
              setQuery(event.target.value)
              setPage(1)
            }}
          />
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>通道</th>
                <th>服务商</th>
                <th>调度</th>
                <th>凭据</th>
                <th>状态</th>
                <th aria-label="操作" />
              </tr>
            </thead>
            <tbody>
              {visible.map((channel) => (
                <tr key={channel.id}>
                  <td>
                    <b>{channel.display_name}</b>
                    <small>{channel.webhook_url}</small>
                  </td>
                  <td>{providerName[channel.provider]}</td>
                  <td>
                    优先级 {channel.priority}
                    <small>权重 {channel.weight}</small>
                  </td>
                  <td>{channel.configured ? "已配置" : "未配置"}</td>
                  <td>
                    <span className={`status ${channel.enabled ? "paid" : "pending"}`}>{channel.enabled ? "已启用" : "未启用"}</span>
                  </td>
                  <td className="row-actions">
                    {canManage && (
                      <>
                        <button className="icon-button" title="配置通道" aria-label="配置通道" onClick={() => setEditing(channel)}>
                          <Settings size={15} />
                        </button>
                        <button className="icon-button destructive" title="删除通道" aria-label="删除通道" onClick={() => remove(channel)}>
                          <Trash2 size={15} />
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!visible.length && <Empty icon="◈" title="尚未添加支付通道" copy="可添加多个支付宝或微信支付通道，并为同一优先级的通道设置分流权重。" />}
        {pageCount > 1 && (
          <div className="table-pages">
            <button className="secondary-button" disabled={current === 1} onClick={() => setPage(current - 1)}>
              上一页
            </button>
            <span>
              {current} / {pageCount}
            </span>
            <button className="secondary-button" disabled={current === pageCount} onClick={() => setPage(current + 1)}>
              下一页
            </button>
          </div>
        )}
      </section>
      {adding && (
        <AddChannel
          onClose={() => setAdding(false)}
          onSaved={() => {
            setAdding(false)
            onRefresh()
          }}
          request={request}
        />
      )}{" "}
      {editing && (
        <ChannelConfig
          channel={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            onRefresh()
          }}
          request={request}
        />
      )}
    </div>
  )
}
function AddChannel({ request, onClose, onSaved }: { request: ApiRequest; onClose: () => void; onSaved: () => void }) {
  const [provider, setProvider] = useState("alipay")
  const [displayName, setDisplayName] = useState("支付宝")
  const [priority, setPriority] = useState(100)
  const [weight, setWeight] = useState(100)
  const [error, setError] = useState("")
  async function save() {
    try {
      await request("/api/v1/admin/channels", {
        method: "POST",
        body: JSON.stringify({
          provider,
          display_name: displayName,
          priority,
          weight,
        }),
      })
      onSaved()
    } catch (err: any) {
      setError(err.message)
    }
  }
  return (
    <Modal title="新增支付通道" onClose={onClose}>
      <Label className="form-label">
        支付服务商
        <Select
          value={provider}
          onValueChange={(value) => {
            if (value) {
              setProvider(value)
              setDisplayName(value === "alipay" ? "支付宝" : "微信支付")
            }
          }}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="alipay">支付宝</SelectItem>
            <SelectItem value="wechat">微信支付</SelectItem>
          </SelectContent>
        </Select>
      </Label>
      <Label className="form-label">
        展示名称
        <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} />
      </Label>
      <Label className="form-label">
        优先级（数值越小越优先）
        <Input type="number" min="0" value={priority} onChange={(event) => setPriority(Number(event.target.value))} />
      </Label>
      <Label className="form-label">
        分流权重（同优先级生效）
        <Input type="number" min="1" value={weight} onChange={(event) => setWeight(Number(event.target.value))} />
      </Label>
      {error && <p className="form-error">{error}</p>}
      <button className="primary-button full" onClick={save}>
        添加并配置
      </button>
    </Modal>
  )
}
function ChannelConfig({ channel, request, onClose, onSaved }: { channel: Channel; request: ApiRequest; onClose: () => void; onSaved: () => void }) {
  const alipay = channel.provider === "alipay"
  const publicValues = alipay
    ? channel.config?.alipay || {}
    : channel.config?.wechat || {}
  const [name, setName] = useState(channel.display_name)
  const [priority, setPriority] = useState(channel.priority)
  const [weight, setWeight] = useState(channel.weight)
  const [enabled, setEnabled] = useState(channel.enabled)
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(Object.entries(publicValues).filter(([, value]) => typeof value === "string")) as Record<string, string>,
  )
  const [configTouched, setConfigTouched] = useState(false)
  const [error, setError] = useState("")
  const set = (key: string, value: string) => {
    setConfigTouched(true)
    setValues((current) => ({ ...current, [key]: value }))
  }
  async function save() {
    const config = alipay
      ? {
          alipay: {
            pid: values.pid || "",
            mode: values.mode || "face_to_face",
            app_id: values.app_id || "",
            app_private_key_pem: values.app_private_key_pem || "",
            alipay_public_key_pem: values.alipay_public_key_pem || "",
            gateway_url: values.gateway_url || "",
            return_url: values.return_url || "",
          },
        }
      : {
          wechat: {
            mch_id: values.mch_id || "",
            app_id: values.app_id || "",
            merchant_serial_no: values.merchant_serial_no || "",
            merchant_private_key_pem: values.merchant_private_key_pem || "",
            api_v3_key: values.api_v3_key || "",
            platform_public_key_pem: values.platform_public_key_pem || "",
            platform_serial_no: values.platform_serial_no || "",
          },
        }
    try {
      await request(`/api/v1/admin/channels/${channel.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          display_name: name,
          priority,
          weight,
          enabled,
          ...(configTouched ? { config } : {}),
        }),
      })
      onSaved()
    } catch (err: any) {
      setError(err.message)
    }
  }
  const field = (label: string, key: string, secret = false, configured = false) => (
    <Label className="form-label" key={key}>
      {label}{configured ? "（已保存，留空不修改）" : ""}
      <Input type={secret ? "password" : "text"} value={values[key] || ""} placeholder={configured ? "已保存；输入新值替换" : undefined} onChange={(event) => set(key, event.target.value)} />
    </Label>
  )
  const pem = (label: string, key: string, configured = false) => (
    <Label className="form-label" key={key}>
      {label}{configured ? "（已保存，留空不修改）" : ""}
      <Textarea rows={4} value={values[key] || ""} placeholder={configured ? "已保存；输入新值替换" : undefined} onChange={(event) => set(key, event.target.value)} />
    </Label>
  )
  return (
    <Modal title={`配置${providerName[channel.provider]}`} onClose={onClose}>
      <p className="modal-intro">同一优先级的通道按权重分流；较小优先级会先被选择。</p>
      <Label className="form-label">
        展示名称
        <Input value={name} onChange={(event) => setName(event.target.value)} />
      </Label>
      <Label className="form-label">
        优先级
        <Input type="number" min="0" value={priority} onChange={(event) => setPriority(Number(event.target.value))} />
      </Label>
      <Label className="form-label">
        分流权重
        <Input type="number" min="1" value={weight} onChange={(event) => setWeight(Number(event.target.value))} />
      </Label>
      {alipay ? (
        <>
          <Label className="form-label">
            支付方式
            <Select value={values.mode || "face_to_face"} onValueChange={(value) => value && set("mode", value)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="face_to_face">当面付（二维码）</SelectItem>
                <SelectItem value="website">网站支付（网页收银台）</SelectItem>
                <SelectItem value="bill_query">账单查询（二维码）</SelectItem>
              </SelectContent>
            </Select>
          </Label>
          {field("支付宝 PID", "pid")}
          {field("App ID", "app_id")}
          {pem("应用私钥（PEM 或 Base64）", "app_private_key_pem", channel.config?.alipay?.app_private_key_configured)}
          {pem("支付宝公钥（PEM 或 Base64）", "alipay_public_key_pem")}
          {field("网关地址（可选）", "gateway_url")}
          {field("同步跳转地址（可选）", "return_url")}
        </>
      ) : (
        <>
          {field("微信支付商户号", "mch_id")}
          {field("应用 AppID", "app_id")}
          {field("商户证书序列号", "merchant_serial_no")}
          {pem("商户私钥（PEM）", "merchant_private_key_pem", channel.config?.wechat?.merchant_private_key_configured)}
          {field("API v3 密钥", "api_v3_key", true, channel.config?.wechat?.api_v3_key_configured)}
          {pem("微信支付公钥（PEM）", "platform_public_key_pem")}
          {field("微信支付公钥 ID（可选）", "platform_serial_no")}
          <p className="modal-intro">公钥 ID 未提供时可留空，系统会直接用 PEM 验签。使用平台证书的旧商户可将两项留空，系统会自动获取证书。</p>
        </>
      )}
      <div className="webhook-copy">
        <b>回调地址</b>
        <code>{channel.webhook_url}</code>
      </div>
      <div className="switch">
        <Switch checked={enabled} onCheckedChange={setEnabled} />
        <span />
        完成配置后启用
      </div>
      {error && <p className="form-error">{error}</p>}
      <button className="primary-button full" onClick={save}>
        保存通道配置
      </button>
    </Modal>
  )
}
