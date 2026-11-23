"use client"
import { useEffect, useState, type ReactNode } from "react"
import { usePathname, useRouter } from "next/navigation"
import Link from "next/link"
import {
  ArrowLeft,
  Blocks,
  Boxes,
  ClipboardList,
  LayoutGrid,
  LogOut,
  Menu,
  Route,
  ShieldCheck,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { api, ApiError, type User } from "@/lib/api"

export function Brand({ admin = false }: { admin?: boolean }) {
  return (
    <div className="flex items-center gap-3">
      <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-emerald-400 text-slate-950">
        <Blocks className="size-5" />
      </span>
      <div className="leading-tight">
        <strong className="block text-[15px]">团队工作台</strong>
        <span className="text-[11px] text-slate-400">
          {admin ? "ADMIN CONSOLE" : "TEAM WORKBENCH"}
        </span>
      </div>
    </div>
  )
}
export function ErrorNotice({ message }: { message: string }) {
  return (
    <p
      role="alert"
      className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
    >
      {message}
    </p>
  )
}
export function Busy() {
  return <p className="p-5 text-sm text-muted-foreground">正在加载…</p>
}
export function Heading({
  eyebrow,
  title,
  description,
}: {
  eyebrow: string
  title: string
  description: string
}) {
  return (
    <header className="space-y-2">
      <p className="text-xs font-semibold tracking-[.18em] text-emerald-700 dark:text-emerald-400">
        {eyebrow}
      </p>
      <h1 className="text-3xl font-bold tracking-tight sm:text-4xl">{title}</h1>
      <p className="text-sm text-muted-foreground">{description}</p>
    </header>
  )
}

export function Workspace({
  children,
  admin = false,
  manager = false,
}: {
  children: ReactNode
  admin?: boolean
  manager?: boolean
}) {
  const [u, setUser] = useState<User>()
  const [problem, setProblem] = useState("")
  const [open, setOpen] = useState(false)
  const path = usePathname()
  const router = useRouter()
  useEffect(() => {
    let dead = false
    api<User>("/api/auth/me")
      .then((q) => {
        if (!dead) setUser(q)
      })
      .catch((e) => {
        if (dead) return
        if (e instanceof ApiError && e.status === 401)
          router.replace(`/login?next=${encodeURIComponent(path)}`)
        else setProblem(e.message)
      })
    return () => {
      dead = true
    }
  }, [path, router])
  const canManage =
    u?.role === "admin" || Object.values(u?.projects ?? {}).includes("manager")
  if (problem)
    return (
      <main className="mx-auto max-w-xl p-5">
        <ErrorNotice message={problem} />
      </main>
    )
  if (!u)
    return (
      <main>
        <Busy />
      </main>
    )
  const denied =
    (admin && !manager && u.role !== "admin") || (manager && !canManage)
  const links = admin
    ? [
        ...(u.role === "admin"
          ? [
              { to: "/admin/users", label: "账号与权限", icon: ShieldCheck },
              { to: "/admin/audit", label: "操作审计", icon: ClipboardList },
              {
                to: "/admin/proposals",
                label: "立项申请",
                icon: ClipboardList,
              },
            ]
          : []),
        { to: "/roadmap/manage", label: "内部评估与计划", icon: Route },
      ]
    : [
        { to: "/projects", label: "项目看板", icon: LayoutGrid },
        { to: "/roadmap", label: "公开 Roadmap", icon: Route },
        ...(canManage
          ? [
              {
                to: u.role === "admin" ? "/admin/users" : "/roadmap/manage",
                label: "管理后台",
                icon: ShieldCheck,
              },
            ]
          : []),
      ]
  async function logout() {
    try {
      await api("/api/auth/logout", { method: "POST" })
      router.replace("/login")
      router.refresh()
    } catch (e) {
      setProblem((e as Error).message)
    }
  }
  return (
    <div className="min-h-screen bg-slate-50/80 lg:grid lg:grid-cols-[232px_minmax(0,1fr)] dark:bg-background">
      <aside className="flex flex-wrap items-center justify-between gap-3 bg-[#101c2c] px-4 py-3 text-slate-100 lg:sticky lg:top-0 lg:h-screen lg:flex-col lg:flex-nowrap lg:items-stretch lg:justify-start lg:p-5">
        <Brand admin={admin} />
        <Button
          className="lg:hidden"
          variant="ghost"
          aria-expanded={open}
          aria-label="展开导航"
          onClick={() => setOpen(!open)}
        >
          <Menu />
        </Button>
        <nav
          className={`${open ? "flex" : "hidden"} w-full flex-col gap-1 lg:mt-7 lg:flex`}
          aria-label={admin ? "管理后台" : "工作台"}
        >
          {admin && (
            <Link
              className="mb-2 flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-slate-300 hover:bg-white/10"
              href="/projects"
            >
              <ArrowLeft className="size-4" />
              返回工作台
            </Link>
          )}
          {links.map(({ to, label, icon: Icon }) => (
            <Link
              onClick={() => setOpen(false)}
              aria-current={
                path === to || path.startsWith(to + "/") ? "page" : undefined
              }
              key={to}
              href={to}
              className={`flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm ${path === to || path.startsWith(to + "/") ? "bg-white/12 text-white" : "text-slate-300 hover:bg-white/8"}`}
            >
              <Icon className="size-4" />
              {label}
            </Link>
          ))}
        </nav>
        <div className="hidden flex-1 lg:block" />
        <div
          className={`${open ? "flex" : "hidden"} w-full items-center gap-2 border-t border-white/10 pt-3 lg:flex`}
        >
          <span className="grid size-8 shrink-0 place-items-center rounded-full bg-white/10">
            <Boxes className="size-4" />
          </span>
          <span className="min-w-0 flex-1 truncate text-sm">{u.name}</span>
          <Button variant="ghost" aria-label="退出登录" onClick={logout}>
            <LogOut />
          </Button>
        </div>
      </aside>
      <div className="min-w-0">
        <div className="hidden h-14 items-center justify-between border-b bg-background px-7 text-sm text-muted-foreground sm:flex">
          <span>{admin ? "系统管理 / 工作台" : "研发协作 / 交付视图"}</span>
          <span>
            Asia/Shanghai · {u.role === "admin" ? "系统管理员" : "团队成员"}
          </span>
        </div>
        <main className="mx-auto grid w-full max-w-[1400px] min-w-0 gap-6 p-4 sm:p-8">
          {denied ? <ErrorNotice message="无管理权限" /> : children}
        </main>
      </div>
    </div>
  )
}
