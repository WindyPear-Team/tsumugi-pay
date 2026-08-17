import type { ReactNode } from "react"

export function Detail({ label, value }: { label: string; value: any }) { return <div className="detail"><span>{label}</span><b>{value || "—"}</b></div> }
export function Modal({ title, children, onClose }: { title: string; children: ReactNode; onClose: () => void }) { return <div className="modal-layer" onMouseDown={onClose}><section className="modal" onMouseDown={(event) => event.stopPropagation()}><header><h3>{title}</h3><button onClick={onClose}>×</button></header>{children}</section></div> }
export function Empty({ icon, title, copy }: { icon: string; title: string; copy: string }) { return <div className="empty"><span>{icon}</span><h3>{title}</h3><p>{copy}</p></div> }
export function formatDate(value?: string) { return value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—" }
export function roleName(role: string) { return ({ user: "用户", platform_admin: "用户", account_admin: "用户", account_operator: "用户", account_viewer: "用户" })[role] || "用户" }
