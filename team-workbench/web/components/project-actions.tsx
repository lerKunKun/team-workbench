"use client"
import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { api, field, ApiError } from "@/lib/api"
import { ErrorNotice } from "@/components/workspace"

export function ProjectActions({ id }: { id?: string }) {
  const [ref, setRef] = useState<{ link?: string; text: string }>()
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [op, setOp] = useState(id ? "issue" : "propose")
  const [unknown, setUnknown] = useState(false)
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const d = Object.fromEntries(new FormData(form))
    const target = id
      ? `/api/projects/${id}/${op === "update" ? "updates" : "issues"}`
      : "/api/project-proposals"
    setBusy(true)
    setError("")
    setRef(undefined)
    try {
      const r = await api<{
        id: string
        identifier?: string
        url?: string
        demo: boolean
      }>(target, {
        method: "POST",
        headers: { "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify(d),
      })
      setRef({
        link: r.url,
        text:
          (r.demo ? "演示 · " : "") +
          (op === "propose"
            ? "立项申请已提交审批"
            : op === "update"
              ? "进度已记录"
              : `Issue 已创建 ${r.identifier ?? ""}`),
      })
      form.reset()
    } catch (e) {
      setError((e as Error).message)
      setUnknown(!(e instanceof ApiError) || e.status === 409)
    } finally {
      setBusy(false)
    }
  }
  return (
    <details className="rounded-2xl border bg-card p-4 sm:p-5">
      <summary className="cursor-pointer font-medium">
        {id ? "项目操作：提交 Issue / 更新进度" : "发起立项申请"}
      </summary>
      <form onSubmit={submit} className="mt-4 grid gap-3">
        {id && (
          <label className="grid gap-1 text-sm">
            操作
            <select
              value={op}
              onChange={(e) => setOp(e.target.value)}
              className={field}
            >
              <option value="issue">提交 Issue</option>
              <option value="update">记录进度</option>
            </select>
          </label>
        )}
        {op === "update" ? (
          <label className="grid gap-1 text-sm">
            健康状态
            <select name="health" className={field}>
              <option value="onTrack">正常</option>
              <option value="atRisk">有风险</option>
              <option value="offTrack">阻塞</option>
            </select>
          </label>
        ) : (
          <label className="grid gap-1 text-sm">
            标题
            <input required maxLength={100} name="title" className={field} />
          </label>
        )}
        <label className="grid gap-1 text-sm">
          说明
          <textarea
            required
            maxLength={2400}
            name="body"
            rows={3}
            className={field}
          />
        </label>
        {error && <ErrorNotice message={error} />}{" "}
        {ref && (
          <p role="status" className="text-sm text-emerald-700">
            {ref.text}{" "}
            {ref.link && (
              <a className="underline" href={ref.link}>
                在 Linear 查看
              </a>
            )}
          </p>
        )}
        <Button disabled={busy || unknown} className="justify-self-start">
          提交
        </Button>
      </form>
    </details>
  )
}
