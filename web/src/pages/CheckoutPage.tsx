import { useEffect, useState } from "react"
import { QRCodeSVG } from "qrcode.react"

import { API, providerName, statusName } from "@/lib/api"

type Checkout = {
  platform_order_no: string
  merchant_order_no: string
  subject: string
  amount: string
  currency: string
  provider: string
  status: string
  qrcode?: string
  pay_url?: string
  return_url?: string
  expires_at?: string
}

export function CheckoutPage({ orderNo, siteName }: { orderNo: string; siteName: string }) {
  const [checkout, setCheckout] = useState<Checkout | null>(null)
  const [error, setError] = useState("")
  const autoRedirect = new URLSearchParams(window.location.search).get("auto_redirect") !== "0"

  useEffect(() => {
    let cancelled = false
    let redirecting = false
    async function load() {
      try {
        const response = await fetch(`${API}/api/v1/payments/${encodeURIComponent(orderNo)}`)
        const body = await response.json().catch(() => ({}))
        if (!response.ok) throw new Error(body.message || "无法获取支付订单")
        if (cancelled) return
        setCheckout(body)
        const paymentTarget = body.pay_url || (autoRedirect ? body.qrcode : "")
        if (body.status === "pending" && autoRedirect && paymentTarget && !redirecting) {
          redirecting = true
          window.location.replace(paymentTarget)
        }
        if (body.status === "paid" && body.return_url && !redirecting) {
          redirecting = true
          window.location.replace(body.return_url)
        }
      } catch (err: any) {
        if (!cancelled) setError(err.message)
      }
    }
    void load()
    const timer = window.setInterval(load, 2500)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [autoRedirect, orderNo])

  return (
    <main className="checkout-page">
      <section className="checkout-shell" aria-live="polite">
        <div className="checkout-brand">{siteName}</div>
        {error ? (
          <div className="checkout-message error">{error}</div>
        ) : !checkout ? (
          <div className="checkout-message">正在加载支付订单...</div>
        ) : (
          <>
            <header className="checkout-head">
              <p>{providerName[checkout.provider] || checkout.provider}</p>
              <h1>{checkout.subject}</h1>
              <strong>{checkout.currency === "CNY" ? "¥" : `${checkout.currency} `}{checkout.amount}</strong>
            </header>
            {checkout.status === "pending" && checkout.qrcode && (
              <div className="checkout-qr">
                <QRCodeSVG value={checkout.qrcode} size={220} level="M" includeMargin />
                <p>{autoRedirect ? "正在打开支付地址..." : "请使用微信扫描二维码完成支付"}</p>
              </div>
            )}
            {checkout.status === "pending" && !checkout.qrcode && !checkout.pay_url && <div className="checkout-message">支付方式暂未返回可用收银台。</div>}
            {checkout.status !== "pending" && <div className={`checkout-message ${checkout.status === "paid" ? "success" : "error"}`}>{statusName[checkout.status] || checkout.status}</div>}
            <footer className="checkout-footer">
              <span>订单号 {checkout.merchant_order_no}</span>
              {checkout.expires_at && <span>有效期至 {new Date(checkout.expires_at).toLocaleString("zh-CN", { hour12: false })}</span>}
            </footer>
          </>
        )}
      </section>
    </main>
  )
}
