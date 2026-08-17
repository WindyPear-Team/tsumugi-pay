import { useState } from "react"

import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Detail, Empty, Modal, formatDate } from "@/components/page-ui"
import { providerName, statusName } from "@/lib/api"
import type { ApiRequest, Bill } from "@/types"

export function BillsPage({ data, canManage, request, onRefresh }: { data: Bill[]; canManage: boolean; request: ApiRequest; onRefresh: () => void }) {
  const [filter, setFilter] = useState("all")
  const [query, setQuery] = useState("")
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<Bill | null>(null)
  const [refund, setRefund] = useState<Bill | null>(null)
  const [reconcile, setReconcile] = useState<Bill | null>(null)
  const filtered = (filter === "all" ? data : data.filter((bill) => bill.status === filter)).filter((bill) => `${bill.subject} ${bill.merchant_order_no} ${bill.platform_order_no}`.toLowerCase().includes(query.toLowerCase()))
  const pageCount = Math.max(1, Math.ceil(filtered.length / 10))
  const current = Math.min(page, pageCount)
  const visible = filtered.slice((current - 1) * 10, current * 10)
  async function close(bill: Bill) {
    if (!confirm(`确认关闭订单 ${bill.merchant_order_no}？`)) return
    await request(`/api/v1/admin/bills/${bill.id}/close`, { method: "POST" })
    onRefresh()
  }
  async function retryCallback(bill: Bill) {
    if (!confirm(`确认向 ${bill.notify_url} 重发支付成功通知？`)) return
    await request(`/api/v1/admin/bills/${bill.id}/notify`, { method: "POST" })
    onRefresh()
  }
  return (
    <div className="content">
      <section className="page-actions">
        <div className="segmented">
          {[
            ["all", "全部"],
            ["pending", "待支付"],
            ["paid", "已支付"],
            ["refunded", "已退款"],
          ].map(([key, label]) => (
            <button
              key={key}
              className={filter === key ? "selected" : ""}
              onClick={() => {
                setFilter(key)
                setPage(1)
              }}
            >
              {label}
            </button>
          ))}
        </div>
        <button className="secondary-button" onClick={onRefresh}>
          ↻ 刷新
        </button>
      </section>
      <section className="panel table-panel">
        <div className="table-tools">
          <Input
            value={query}
            placeholder="搜索订单号或商品名称"
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
                <th>账单信息</th>
                <th>通道</th>
                <th>金额</th>
                <th>状态</th>
                <th>创建时间</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {visible.map((bill) => (
                <tr key={bill.id}>
                  <td>
                    <b>{bill.subject}</b>
                    <small>
                      {bill.merchant_order_no}
                      <br />
                      {bill.platform_order_no}
                    </small>
                  </td>
                  <td>
                    {providerName[bill.provider]}
                    <small>{bill.scene}</small>
                  </td>
                  <td className="amount">¥ {bill.amount}</td>
                  <td>
                    <span className={`status ${bill.status}`}>{statusName[bill.status] || bill.status}</span>
                  </td>
                  <td>{formatDate(bill.created_at)}</td>
                  <td className="row-actions">
                    <button onClick={() => setSelected(bill)}>详情</button>
                    {canManage && bill.status === "pending" && (
                      <>
                        <button onClick={() => setReconcile(bill)}>补单</button>
                        <button onClick={() => close(bill)}>关闭</button>
                      </>
                    )}
                    {canManage && bill.status === "paid" && (
                      <>
                        <button onClick={() => retryCallback(bill)}>重发回调</button>
                        <button onClick={() => setRefund(bill)}>退款</button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!visible.length && <Empty icon="▤" title="暂无账单" copy="切换筛选条件或通过开放支付接口创建新订单。" />}
        </div>
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
      {selected && (
        <Modal title="账单详情" onClose={() => setSelected(null)}>
          <Detail label="平台订单号" value={selected.platform_order_no} />
          <Detail label="商户订单号" value={selected.merchant_order_no} />
          <Detail label="金额" value={`¥ ${selected.amount}`} />
          <Detail label="状态" value={statusName[selected.status]} />
        </Modal>
      )}
      {reconcile && (
        <ReconcileModal
          bill={reconcile}
          request={request}
          onClose={() => setReconcile(null)}
          onSaved={() => {
            setReconcile(null)
            onRefresh()
          }}
        />
      )}{" "}
      {refund && (
        <RefundModal
          bill={refund}
          request={request}
          onClose={() => setRefund(null)}
          onSaved={() => {
            setRefund(null)
            onRefresh()
          }}
        />
      )}
    </div>
  )
}

function ReconcileModal({ bill, request, onClose, onSaved }: { bill: Bill; request: ApiRequest; onClose: () => void; onSaved: () => void }) {
  const [providerTransactionID, setProviderTransactionID] = useState("")
  const [error, setError] = useState("")
  async function save() {
    try {
      setError("")
      await request(`/api/v1/admin/bills/${bill.id}/reconcile`, {
        method: "POST",
        body: JSON.stringify({
          provider_transaction_id: providerTransactionID,
        }),
      })
      onSaved()
    } catch (err: any) {
      setError(err.message)
    }
  }
  return (
    <Modal title="补单确认" onClose={onClose}>
      <p className="modal-intro">确认渠道已收款后补记为已支付，并按原异步通知地址补发支付成功通知。</p>
      <Detail label="商户订单号" value={bill.merchant_order_no} />
      <Detail label="补单金额" value={`¥ ${bill.amount}`} />
      <Label className="form-label">
        渠道交易号
        <Input value={providerTransactionID} placeholder="支付宝或微信支付交易号" onChange={(event) => setProviderTransactionID(event.target.value)} />
      </Label>
      {error && <p className="form-error">{error}</p>}
      <button className="primary-button full" disabled={!providerTransactionID.trim()} onClick={save}>
        确认补单
      </button>
    </Modal>
  )
}

function RefundModal({ bill, request, onClose, onSaved }: { bill: Bill; request: ApiRequest; onClose: () => void; onSaved: () => void }) {
  const [amount, setAmount] = useState(bill.amount)
  const [refundOrderNo, setRefundOrderNo] = useState(`RF${Date.now()}`)
  const [reason, setReason] = useState("后台发起退款")
  const [error, setError] = useState("")
  async function save() {
    try {
      await request(`/api/v1/admin/bills/${bill.id}/refunds`, {
        method: "POST",
        body: JSON.stringify({
          refund_order_no: refundOrderNo,
          amount,
          reason,
        }),
      })
      onSaved()
    } catch (err: any) {
      setError(err.message)
    }
  }
  return (
    <Modal title="发起退款" onClose={onClose}>
      <Label className="form-label">
        退款金额
        <Input value={amount} onChange={(event) => setAmount(event.target.value)} />
      </Label>
      <Label className="form-label">
        退款单号
        <Input value={refundOrderNo} onChange={(event) => setRefundOrderNo(event.target.value)} />
      </Label>
      <Label className="form-label">
        退款原因
        <Input value={reason} onChange={(event) => setReason(event.target.value)} />
      </Label>
      {error && <p className="form-error">{error}</p>}
      <button className="primary-button full" onClick={save}>
        确认退款
      </button>
    </Modal>
  )
}
