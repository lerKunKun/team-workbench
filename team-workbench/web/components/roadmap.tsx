"use client"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { FileDown, Move, Sparkles } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Busy, ErrorNotice, Heading } from "@/components/workspace"
import { api, field, states, type Detail, type Item } from "@/lib/api"

export function Roadmap() {
  const [items, setItems] = useState<Item[]>()
  const [error, setError] = useState("")
  const [status, setStatus] = useState("")
  const [details, setDetails] = useState<Detail>()
  const [body, setBody] = useState("")
  const [saving, setSaving] = useState(false)
  const refresh = useCallback(async () => {
    try {
      setItems(await api<Item[]>("/api/roadmap?mode=manage"))
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])
  useEffect(() => {
    const abort = new AbortController()
    const fail = (e: Error) => {
      if (!abort.signal.aborted) setError(e.message)
    }
    api<Item[]>("/api/roadmap?mode=manage", {
      signal: abort.signal,
    })
      .then(setItems)
      .catch(fail)
    return () => abort.abort()
  }, [])
  async function open(id: string) {
    try {
      setDetails(await api<Detail>(`/api/roadmap/${id}`))
      setBody("")
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function move(id: string, state: string) {
    try {
      setError("")
      await api(`/api/roadmap/${id}/status`, {
        method: "PATCH",
        body: JSON.stringify({ state }),
      })
      setStatus(`已更新为「${states[state]}」`)
      await refresh()
      if (details?.item.id === id) await open(id)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  async function plan(generate: boolean) {
    if (!details) return
    setSaving(true)
    try {
      setError("")
      const id = details.item.id
      await api(`/api/roadmap/${id}/plans`, {
        method: "POST",
        body: JSON.stringify({ body: generate ? "" : body, generate }),
      })
      setStatus(
        generate ? "已提交计划生成，请稍后刷新详情。" : "草稿版本已保存。"
      )
      await open(id)
      await refresh()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }
  async function merge(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!details) return
    const v = new FormData(e.currentTarget)
    try {
      setError("")
      const id = details.item.id
      await api(`/api/roadmap/${id}/merge-feedback`, {
        method: "POST",
        body: JSON.stringify({ sourceId: v.get("source") }),
      })
      setStatus("已归并反馈，保留来源记录。")
      await open(id)
      await refresh()
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <>
      <Heading
        eyebrow="INTERNAL PLANNING"
        title="内部评估与实施计划"
        description="公开反馈在 外部 Roadmap 收集；此处管理导入后的内部决策和 plan.md。"
      />
      <a
        className="w-fit text-sm text-emerald-700 underline"
        href="https://roadmap-admin.example.com/workbench/api/sso/start"
      >
        使用工作台身份进入公开 Roadmap 管理
      </a>
      {error && <ErrorNotice message={error} />}
      <p
        role="status"
        className={
          status
            ? "rounded-lg bg-emerald-100 px-3 py-2 text-sm text-emerald-900"
            : "sr-only"
        }
      >
        {status}
      </p>
      {!items ? (
        <Busy />
      ) : (
        <div className="grid min-w-0 gap-3 sm:grid-cols-2 xl:grid-cols-5">
          {Object.entries(states).map(([k, label]) => {
            const group = items.filter((i) => i.state === k)
            return (
              <section
                key={k}
                className="min-w-0 rounded-2xl border bg-muted/45 p-3"
                onDragOver={(e) => e.preventDefault()}
                onDrop={(e) => {
                  e.preventDefault()
                  const id = e.dataTransfer.getData("text/plain")
                  if (items.some((x) => x.id === id)) void move(id, k)
                }}
              >
                <h2 className="flex items-center justify-between px-1 py-1.5 text-sm font-semibold">
                  {label}
                  <span className="rounded-full bg-card px-2 py-0.5 text-xs text-muted-foreground">
                    {group.length}
                  </span>
                </h2>
                {group.map((i) => (
                  <article
                    key={i.id}
                    draggable
                    onDragStart={(e) =>
                      e.dataTransfer.setData("text/plain", i.id)
                    }
                    className="mt-2 grid min-w-0 gap-2 rounded-xl border bg-card p-3 shadow-xs"
                  >
                    <h3 className="text-sm font-medium break-words">
                      {i.title}
                    </h3>
                    <p className="text-xs text-muted-foreground">
                      {i.feedbackCount} 条反馈 · {i.planCount} 个计划
                      {` · 公开站 ${i.sources.external ?? 0} / 历史网页 ${i.sources.web ?? 0}`}
                    </p>
                    <form
                      className="flex min-w-0 items-center gap-1.5"
                      onSubmit={(e) => {
                        e.preventDefault()
                        const next = new FormData(e.currentTarget).get("state")
                        if (typeof next === "string" && next !== i.state)
                          void move(i.id, next)
                      }}
                    >
                      <Move className="size-3 shrink-0" />
                      <select
                        className={field}
                        aria-label={`移动 ${i.title}`}
                        name="state"
                        defaultValue={i.state}
                      >
                        {Object.entries(states).map(([x, y]) => (
                          <option key={x} value={x}>
                            {y}
                          </option>
                        ))}
                      </select>
                      <Button
                        size="sm"
                        type="submit"
                        aria-label={`应用状态 ${i.title}`}
                      >
                        移列
                      </Button>
                    </form>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => void open(i.id)}
                    >
                      查看详情
                    </Button>
                  </article>
                ))}
                {!group.length && (
                  <p className="px-1 py-4 text-xs text-muted-foreground">
                    暂无条目
                  </p>
                )}
              </section>
            )
          })}
        </div>
      )}
      {details && (
        <section className="grid gap-4 rounded-2xl border bg-card p-4 sm:p-5">
          <div className="flex flex-wrap justify-between gap-2">
            <h2 className="text-lg font-semibold">{details.item.title}</h2>
            <Button variant="ghost" onClick={() => setDetails(undefined)}>
              关闭详情
            </Button>
          </div>
          <ul className="divide-y text-sm">
            {details.feedback.map((f) => (
              <li key={f.id} className="py-2 break-words">
                <span className="mr-2 text-xs text-muted-foreground">
                  {f.source === "external"
                    ? "公开 Roadmap"
                    : f.source === "web"
                      ? "历史网页"
                      : "钉钉"}
                </span>
                {f.body}
              </li>
            ))}
          </ul>
          {items?.some(
            (x) =>
              x.id !== details.item.id &&
              x.projectId === details.item.projectId &&
              ["triage", "evaluating"].includes(x.state)
          ) && (
            <form className="flex flex-wrap items-end gap-2" onSubmit={merge}>
              <label className="min-w-40 flex-1 text-sm">
                将重复反馈归并到当前条目
                <select className={`${field} mt-1`} name="source" required>
                  <option value="">请选择来源条目</option>
                  {items
                    .filter(
                      (x) =>
                        x.id !== details.item.id &&
                        x.projectId === details.item.projectId &&
                        ["triage", "evaluating"].includes(x.state)
                    )
                    .map((x) => (
                      <option key={x.id} value={x.id}>
                        {x.title}
                      </option>
                    ))}
                </select>
              </label>
              <Button variant="outline">归并</Button>
            </form>
          )}
          <div className="grid gap-3 border-t pt-4">
            <h3 className="font-semibold">实施计划</h3>
            <p className="text-sm text-muted-foreground">
              {details.modelReady
                ? "模型可生成草稿，仍需人工审核。"
                : "AI 模型未启用，可先编辑人工草稿。"}
              {details.item.generation && ` · ${details.item.generation}`}
            </p>
            {details.plans.map((v) => (
              <a
                key={v.version}
                className="flex items-center gap-2 text-sm text-emerald-700 underline"
                href={`/api/roadmap/${details.item.id}/plans/${v.version}/download`}
              >
                <FileDown className="size-4" />
                下载 plan.md · v{v.version}
              </a>
            ))}
            {details.item.state === "confirmed" && (
              <>
                <textarea
                  aria-label="计划 Markdown"
                  placeholder="# 范围、实施步骤、依赖、风险和验收标准"
                  className={field}
                  rows={5}
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                />
                <div className="flex flex-wrap gap-2">
                  <Button
                    disabled={saving || !body.trim()}
                    onClick={() => void plan(false)}
                  >
                    保存 Markdown
                  </Button>
                  <Button
                    variant="outline"
                    disabled={saving || !details.modelReady}
                    onClick={() => void plan(true)}
                  >
                    <Sparkles />
                    生成 AI 草稿
                  </Button>
                </div>
              </>
            )}
          </div>
        </section>
      )}
    </>
  )
}
