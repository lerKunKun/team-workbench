"use client"
import { useEffect, useState, type FormEvent } from "react"
import { useRouter } from "next/navigation"
import {
  ArrowRight,
  Layers3,
  LockKeyhole,
  QrCode,
  ScanLine,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { api, field, type User } from "@/lib/api"
import { Brand, ErrorNotice } from "@/components/workspace"

type Methods = {
  dingSso: boolean
  dingQr: boolean
  adminPortal?: boolean
  corpId?: string
  clientId?: string
}
export function Login({
  next,
  inside,
  failed,
}: {
  next?: string
  inside: boolean
  failed: boolean
}) {
  const [methods, setMethods] = useState<Methods>({
    dingSso: false,
    dingQr: false,
  })
  const [message, setMessage] = useState(
    failed ? "钉钉登录未完成，请重试或使用系统账号。" : ""
  )
  const [busy, setBusy] = useState(false)
  const router = useRouter()
  async function sso() {
    if (!methods.corpId) return
    setBusy(true)
    setMessage("")
    try {
      const { default: requestAuthCode } =
        await import("dingtalk-jsapi/api/runtime/permission/requestAuthCode")
      let timer: ReturnType<typeof setTimeout> | undefined
      try {
        const result = await Promise.race([
          requestAuthCode({ corpId: methods.corpId }),
          new Promise<never>((_, reject) => {
            timer = setTimeout(() => reject(new Error("钉钉授权超时")), 10000)
          }),
        ])
        await api<User>("/api/auth/dingtalk/sso", {
          method: "POST",
          body: JSON.stringify({ code: result.code }),
        })
        continueToTarget()
      } finally {
        clearTimeout(timer)
      }
    } catch {
      setMessage("钉钉免登未完成。请在钉钉客户端打开，或使用系统账号。")
    } finally {
      setBusy(false)
    }
  }
  const target =
    next &&
    !next.includes("\\") &&
    (methods.adminPortal
      ? /^\/(admin(\/|\?|$)|roadmap\/manage(\/|\?|$))/.test(next)
      : /^(\/(projects|roadmap|admin)(\/|\?|$)|\/api\/integrations\/roadmap\/sso\/authorize\?)/.test(
          next
        ))
      ? next
      : methods.adminPortal
        ? "/admin/users"
        : "/projects"
  function continueToTarget() {
    if (target.startsWith("/api/integrations/roadmap/sso/authorize?"))
      window.location.assign(target)
    else router.replace(target)
  }
  useEffect(() => {
    api<Methods>("/api/auth/options")
      .then(setMethods)
      .catch(() => undefined)
  }, [])
  async function onLogin(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setBusy(true)
    setMessage("")
    const v = new FormData(e.currentTarget)
    try {
      await api<User>("/api/auth/login", {
        method: "POST",
        body: JSON.stringify({
          username: v.get("username"),
          password: v.get("password"),
        }),
      })
      continueToTarget()
    } catch (e) {
      setMessage((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <main className="grid min-h-screen lg:grid-cols-2">
      <aside className="relative flex min-h-[210px] flex-col justify-between overflow-hidden bg-[#101c2c] p-6 text-white sm:p-10 lg:min-h-screen lg:p-14">
        <Brand admin={methods.adminPortal} />
        <div className="relative z-10 my-8 max-w-xl space-y-4 lg:my-auto">
          <p className="text-xs font-semibold tracking-[.26em] text-emerald-300">
            项目管理 · 交付协作
          </p>
          <h2 className="text-3xl font-semibold tracking-tight sm:text-5xl">
            让本周交付
            <br className="hidden lg:block" />
            清楚可见。
          </h2>
          <p className="max-w-sm text-sm leading-6 text-slate-300">
            集中查看项目进度、负责人和阻塞，让团队反馈进入下一步行动。
          </p>
        </div>
        <Layers3
          aria-hidden
          className="absolute -right-14 -bottom-16 size-[310px] text-emerald-400/10 lg:size-[440px]"
        />
        <span className="hidden text-xs text-slate-400 lg:block">
          TEAM ENGINEERING · INTERNAL WORKSPACE
        </span>
      </aside>
      <section className="grid place-items-center px-5 py-10 sm:p-12">
        <div className="grid w-full max-w-sm gap-6">
          <div>
            <p className="mb-2 text-xs font-semibold tracking-[.18em] text-emerald-700 dark:text-emerald-400">
              {methods.adminPortal ? "系统管理" : "团队工作台"}
            </p>
            <h1 className="text-3xl font-bold tracking-tight">
              {methods.adminPortal ? "登录管理后台" : "登录工作台"}
            </h1>
            <p className="mt-2 text-sm text-muted-foreground">
              {methods.adminPortal
                ? "请使用系统账号和密码。管理后台不共享工作台的登录会话。"
                : "使用管理员分配的账号或你的企业钉钉身份。"}
            </p>
          </div>
          {message ? <ErrorNotice message={message} /> : null}
          <form className="grid gap-4" onSubmit={onLogin}>
            <label className="grid gap-2 text-sm font-medium">
              用户名
              <input
                className={field}
                name="username"
                autoComplete="username"
                required
                minLength={3}
              />
            </label>
            <label className="grid gap-2 text-sm font-medium">
              密码
              <input
                className={field}
                name="password"
                type="password"
                autoComplete="current-password"
                required
              />
            </label>
            <Button
              type="submit"
              className="h-10 justify-between"
              disabled={busy}
            >
              <span className="flex items-center gap-2">
                <LockKeyhole />
                系统账号登录
              </span>
              <ArrowRight />
            </Button>
          </form>
          <div className="grid gap-2 border-t pt-4">
            {methods.adminPortal ? (
              <a
                className="flex min-h-10 items-center gap-2 rounded-lg border px-3 text-sm hover:bg-muted"
                href="https://workbench.example.com/login"
              >
                <QrCode className="size-4" />
                前往团队工作台使用钉钉扫码
              </a>
            ) : (
              <>
                {methods.dingQr ? (
                  <a
                    className="flex min-h-10 items-center gap-2 rounded-lg border px-3 text-sm hover:bg-muted"
                    href={`/api/auth/dingtalk/start?next=${encodeURIComponent(target)}`}
                  >
                    <QrCode className="size-4" />
                    钉钉扫码登录
                  </a>
                ) : (
                  <Button
                    variant="outline"
                    className="h-10 justify-start"
                    disabled
                  >
                    <QrCode />
                    钉钉扫码登录 · 未启用
                  </Button>
                )}
                <Button
                  variant="outline"
                  className="h-10 justify-start"
                  disabled={!methods.dingSso || !inside || busy}
                  onClick={() => void sso()}
                >
                  <ScanLine />
                  {methods.dingSso ? "钉钉端内免登" : "钉钉端内免登 · 未启用"}
                </Button>
              </>
            )}
          </div>
        </div>
      </section>
    </main>
  )
}
