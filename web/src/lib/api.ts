import type { ApiRequest } from "@/types"

export const API = import.meta.env.VITE_API_BASE_URL || ""
export const statusName: Record<string, string> = { pending: "待支付", paid: "已支付", closed: "已关闭", failed: "失败", refunding: "退款中", refunded: "已退款", active: "正常", suspended: "已暂停" }
export const providerName: Record<string, string> = { alipay: "支付宝", wechat: "微信支付" }
export type { ApiRequest }
