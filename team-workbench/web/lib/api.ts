export class ApiError extends Error {
  constructor(
    public status: number,
    message: string
  ) {
    super(message)
  }
}
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const r = await fetch(path, {
    cache: "no-store",
    ...init,
    headers: {
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      ...init.headers,
    },
  })
  if (!r.ok) {
    const j = await r.json().catch(() => null)
    throw new ApiError(
      r.status,
      j?.error?.message ?? "暂时无法加载，请稍后重试"
    )
  }
  return r.status === 204 ? (undefined as T) : ((await r.json()) as T)
}
export type User = {
  id: string
  username: string
  name: string
  role: "admin" | "member"
  projects: Record<string, "manager" | "member">
}
export type Project = {
  id: string
  name: string
  summary: string
  owner: string
  url: string
}
export type Issue = {
  id: string
  title: string
  projectId: string
  owner: string
  dueDate: string
  state: string
  blocker: string
  linearUrl: string
  done: boolean
}
export type Deliveries = {
  projects: Project[]
  deliveries: Issue[]
  overdue: Issue[]
  source: string
  week: string
  asOf: string
}
export type Item = {
  id: string
  projectId: string
  title: string
  state: string
  feedbackCount: number
  planCount: number
  sources: Record<string, number>
  generation: string
}
export type Detail = {
  item: Item
  feedback: {
    id: string
    body: string
    source: string
    originalItemId: string
    createdAt: string
  }[]
  plans: { version: number; source: string; createdAt: string }[]
  modelReady: boolean
}
export const field =
  "min-w-0 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus-visible:outline-2 focus-visible:outline-ring"
export const states: Record<string, string> = {
  triage: "待分流",
  evaluating: "待评估",
  confirmed: "已确定",
  delivered: "已交付",
  declined: "暂不采纳",
}
