"use client"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { api, field, type Deliveries, type User } from "@/lib/api"
import { Busy, ErrorNotice, Heading } from "@/components/workspace"

type Account = User & { active: boolean }
export function Users() {
  const [users, setUsers] = useState<Account[]>()
  const [projects, setProjects] = useState<Deliveries["projects"]>([])
  const [error, setError] = useState("")
  const [status, setStatus] = useState("")
  const [busy, setBusy] = useState(false)
  const refresh = useCallback(
    () =>
      api<Account[]>("/api/admin/users")
        .then(setUsers)
        .catch((e) => setError(e.message)),
    []
  )
  useEffect(() => {
    void refresh()
    api<Deliveries>("/api/projects")
      .then((x) => setProjects(x.projects))
      .catch(() => undefined)
  }, [refresh])
  async function change(fn: () => Promise<unknown>, label: string) {
    setBusy(true)
    setError("")
    setStatus("")
    try {
      await fn()
      setStatus(label)
      await refresh()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  function create(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const data = Object.fromEntries(new FormData(form))
    void change(async () => {
      await api("/api/admin/users", {
        method: "POST",
        body: JSON.stringify(data),
      })
      form.reset()
    }, "账号已创建。")
  }
  function grant(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const v = new FormData(e.currentTarget)
    void change(
      () =>
        api(`/api/admin/users/${v.get("user")}/projects/${v.get("project")}`, {
          method: "PUT",
          body: JSON.stringify({ role: v.get("role") }),
        }),
      "项目授权已更新。"
    )
  }
  return (
    <>
      <Heading
        eyebrow="ACCESS CONTROL"
        title="账号与权限"
        description="管理系统账号、项目成员与项目负责人。角色规则由后端 Casbin 校验。"
      />
      {error && <ErrorNotice message={error} />}
      <p
        role="status"
        className={
          status
            ? "rounded-lg bg-emerald-100 p-3 text-sm text-emerald-900"
            : "sr-only"
        }
      >
        {status}
      </p>
      <div className="grid min-w-0 gap-4 xl:grid-cols-2">
        <form
          onSubmit={create}
          className="grid gap-3 rounded-2xl border bg-card p-4 sm:p-5"
        >
          <h2 className="font-semibold">创建账号</h2>
          <label className="grid gap-1 text-sm">
            用户名
            <input
              name="username"
              className={field}
              required
              minLength={3}
              maxLength={64}
            />
          </label>
          <label className="grid gap-1 text-sm">
            显示名
            <input name="name" className={field} required maxLength={60} />
          </label>
          <label className="grid gap-1 text-sm">
            初始密码
            <input
              name="password"
              type="password"
              autoComplete="new-password"
              className={field}
              required
              minLength={12}
              maxLength={100}
            />
          </label>
          <label className="grid gap-1 text-sm">
            系统角色
            <select name="role" className={field}>
              <option value="member">成员</option>
              <option value="admin">系统管理员</option>
            </select>
          </label>
          <Button disabled={busy} className="justify-self-start">
            创建账号
          </Button>
        </form>
        <form
          onSubmit={grant}
          className="grid content-start gap-3 rounded-2xl border bg-card p-4 sm:p-5"
        >
          <h2 className="font-semibold">按项目授权</h2>
          <label className="grid gap-1 text-sm">
            账号
            <select name="user" className={field} required>
              <option value="">请选择</option>
              {users
                ?.filter((u) => u.active)
                .map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.name} · {u.username}
                  </option>
                ))}
            </select>
          </label>
          <label className="grid gap-1 text-sm">
            项目
            <select name="project" className={field} required>
              <option value="">请选择</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <label className="grid gap-1 text-sm">
            角色
            <select name="role" className={field}>
              <option value="member">项目成员</option>
              <option value="manager">项目负责人</option>
              <option value="">撤销项目权限</option>
            </select>
          </label>
          <Button
            disabled={busy || !projects.length}
            className="justify-self-start"
          >
            保存项目权限
          </Button>
        </form>
      </div>
      {!users ? (
        <Busy />
      ) : (
        <ul className="divide-y rounded-2xl border bg-card p-4">
          {users.map((u) => (
            <li className="flex flex-wrap items-center gap-3 py-3" key={u.id}>
              <div className="min-w-0 flex-1 break-words">
                <p className="font-medium">
                  {u.name}
                  {!u.active && "（停用）"}
                </p>
                <p className="text-xs text-muted-foreground">
                  {u.username} · {Object.keys(u.projects ?? {}).length}{" "}
                  个项目授权
                </p>
              </div>
              <select
                className={`${field} w-auto`}
                value={u.role}
                aria-label={`${u.name} 系统角色`}
                disabled={busy}
                onChange={(e) =>
                  void change(
                    () =>
                      api(`/api/admin/users/${u.id}`, {
                        method: "PATCH",
                        body: JSON.stringify({
                          role: e.target.value,
                          active: u.active,
                        }),
                      }),
                    "系统角色已更新。"
                  )
                }
              >
                <option value="member">成员</option>
                <option value="admin">系统管理员</option>
              </select>
              <Button
                disabled={busy}
                variant="outline"
                onClick={() =>
                  void change(
                    () =>
                      api(`/api/admin/users/${u.id}`, {
                        method: "PATCH",
                        body: JSON.stringify({
                          role: u.role,
                          active: !u.active,
                        }),
                      }),
                    "账号状态已更新。"
                  )
                }
              >
                {u.active ? "停用" : "启用"}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}

export function Audit() {
  const [items, setItems] =
    useState<{ actor: string; action: string; target: string; at: string }[]>()
  const [error, setError] = useState("")
  useEffect(() => {
    api<NonNullable<typeof items>>("/api/admin/audit")
      .then(setItems)
      .catch((e) => setError(e.message))
  }, [])
  return (
    <>
      <Heading
        eyebrow="SYSTEM AUDIT"
        title="操作审计"
        description="最近的账号、授权、反馈与 Roadmap 操作。"
      />
      {error ? (
        <ErrorNotice message={error} />
      ) : !items ? (
        <Busy />
      ) : (
        <ul className="divide-y rounded-2xl border bg-card p-4">
          {items.map((i, n) => (
            <li
              key={n}
              className="grid gap-1 py-3 text-sm break-all sm:grid-cols-3"
            >
              <strong>{i.action}</strong>
              <span>{i.target}</span>
              <time>
                {new Date(i.at).toLocaleString("zh-CN", {
                  timeZone: "Asia/Shanghai",
                })}
              </time>
              <span className="text-xs text-muted-foreground">
                {i.actor || "initial bootstrap"}
              </span>
            </li>
          ))}
          {!items.length && <li>暂无操作记录。</li>}
        </ul>
      )}
    </>
  )
}
