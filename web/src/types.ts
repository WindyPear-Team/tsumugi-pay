export type View = "overview" | "bills" | "refunds" | "channels" | "audit" | "developers" | "users" | "profile" | "settings"
export type Account = {
  id: string
  name: string
  merchant_no: string
  status: string
  created_at?: string
}
export type Bill = {
  id: string
  platform_order_no: string
  merchant_order_no: string
  subject: string
  amount: string
  provider: string
  scene: string
  status: string
  notify_url: string
  created_at: string
  paid_at?: string
}
export type Channel = {
  id: string
  provider: string
  display_name: string
  priority: number
  weight: number
  enabled: boolean
  configured: boolean
  config?: {
    alipay?: {
      pid?: string
      mode?: "face_to_face" | "website"
      app_id?: string
      alipay_public_key_pem?: string
      gateway_url?: string
      return_url?: string
      app_private_key_configured?: boolean
    }
    wechat?: {
      mch_id?: string
      app_id?: string
      merchant_serial_no?: string
      platform_public_key_pem?: string
      platform_serial_no?: string
      merchant_private_key_configured?: boolean
      api_v3_key_configured?: boolean
    }
  }
  webhook_url: string
  updated_at: string
}
export type Dashboard = {
  total_bills: number
  pending_bills: number
  paid_bills: number
  refunded_bills: number
  paid_volume: string
}
export type AccountUser = {
  id: string
  account_id?: string
  email: string
  username: string
  display_name: string
  role: string
  is_active: boolean
  created_at: string
}
export type ManagedUser = AccountUser & {
  account?: { name: string; merchant_no: string }
}
export type Refund = {
  id: string
  bill_id: string
  refund_order_no: string
  amount: string
  reason: string
  status: string
  created_at: string
}
export type AuditLog = {
  id: string
  action: string
  target_type: string
  target_id: string
  request_id: string
  created_at: string
  detail?: Record<string, unknown>
}
export type PublicSiteConfig = {
  site_name: string
  allow_password_login: boolean
  allow_password_registration: boolean
  email_whitelist: string[]
  terms_url: string
  privacy_policy_url: string
  favicon_url: string
  theme_color: string
  oidc_enabled: boolean
  oidc_login_label: string
}
export type SiteSettings = {
  site: PublicSiteConfig
  email: {
    host: string
    port: number
    username: string
    from: string
    password_configured: boolean
  }
  hcaptcha: {
    enabled: boolean
    site_key: string
    secret_key_configured: boolean
  }
  oidc: {
    enabled: boolean
    issuer_url: string
    client_id: string
    redirect_url: string
    authorization_endpoint: string
    token_endpoint: string
    jwks_uri: string
    login_label: string
    client_secret_configured: boolean
  }
}
export type ApiRequest = (path: string, options?: RequestInit) => Promise<any>
