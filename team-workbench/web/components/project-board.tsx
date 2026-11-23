"use client"
import { useEffect, useState } from "react"
import Link from "next/link"
import { ProjectActions } from "@/components/project-actions"
import {
  AlertTriangle,
  CalendarDays,
  ChevronLeft,
  Clock3,
  FolderKanban,
} from "lucide-react"
import { Busy, ErrorNotice, Heading } from "@/components/workspace"
import {
  api,
  field,
  type Deliveries,
  type Issue,
  type Project,
} from "@/lib/api"

export function ProjectBoard({ id }: { id?: string }) {
  const [data, setData] = useState<Deliveries>()
  const [detail, setDetail] = useState<{
    project: Project
    issues: Issue[]
    source: string
  }>()
  const [error, setError] = useState("")
  const [selected, setSelected] = useState("")
  const [owner, setOwner] = useState("")
  useEffect(() => {
    let live = true
    const fail = (e: Error) => {
      if (live) setError(e.message)
    }
    if (id)
      api<typeof detail>(`/api/projects/${encodeURIComponent(id)}`)
        .then((d) => {
          if (live) setDetail(d)
        })
        .catch(fail)
    else
      api<Deliveries>("/api/projects")
        .then((d) => {
          if (live) setData(d)
        })
        .catch(fail)
    return () => {
      live = false
    }
  }, [id])
  const issues = (detail?.issues ?? data?.deliveries ?? []).filter(
    (i) =>
      (!selected || i.projectId === selected) &&
      i.owner.toLowerCase().includes(owner.toLowerCase())
  )
  const overdue = (data?.overdue ?? []).filter(
    (i) =>
      (!selected || i.projectId === selected) &&
      i.owner.toLowerCase().includes(owner.toLowerCase())
  )
  const projects = detail ? [detail.project] : (data?.projects ?? [])
  return (
    <>
      <Heading
        eyebrow="DELIVERY OVERVIEW"
        title={detail?.project.name ?? "项目看板"}
        description={
          detail?.project.summary ?? "本周到期事项、负责人和阻塞集中查看。"
        }
      />
      {id && (
        <Link
          href="/projects"
          className="flex items-center gap-1 text-sm text-emerald-700"
        >
          <ChevronLeft className="size-4" />
          返回本周交付
        </Link>
      )}
      {error ? (
        <ErrorNotice message={error} />
      ) : !detail && !data ? (
        <Busy />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
            {(detail?.source ?? data?.source) === "demo" ? (
              <span className="rounded-full bg-amber-100 px-2.5 py-1 text-xs font-semibold text-amber-900">
                演示数据 · 不写入 Linear
              </span>
            ) : (
              <span className="rounded-full bg-emerald-100 px-2.5 py-1 text-xs text-emerald-900">
                Linear
              </span>
            )}
            {data && <span>本周起始 {data.week}（北京时间）</span>}
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            {[
              {
                label: id ? "项目事项数" : "本周交付数",
                n: issues.length,
                icon: CalendarDays,
                color: "text-emerald-700",
              },
              {
                label: "阻塞中",
                n: issues.filter((x) => x.blocker && !x.done).length,
                icon: AlertTriangle,
                color: "text-amber-700",
              },
              {
                label: "此前逾期",
                n: overdue.length,
                icon: Clock3,
                color: "text-red-700",
              },
            ].map(({ label, n, icon: Icon, color }) => (
              <div
                key={label}
                className="flex items-start justify-between rounded-2xl border bg-card p-4 shadow-xs sm:p-5"
              >
                <div>
                  <p className="text-sm text-muted-foreground">{label}</p>
                  <div className="mt-2 text-3xl font-semibold tabular-nums">
                    {n}
                  </div>
                </div>
                <Icon className={`size-5 ${color}`} aria-hidden />
              </div>
            ))}
          </div>
          <section className="min-w-0 rounded-2xl border bg-card p-4 sm:p-5">
            <div className="mb-4 flex flex-wrap items-center gap-3">
              <h2 className="mr-auto text-lg font-semibold">
                {id ? "项目事项" : "本周交付"}
              </h2>
              {!id && (
                <label className="text-xs">
                  按项目
                  <select
                    className={`${field} mt-1`}
                    value={selected}
                    onChange={(e) => setSelected(e.target.value)}
                  >
                    <option value="">全部项目</option>
                    {projects.map((p) => (
                      <option value={p.id} key={p.id}>
                        {p.name}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              <label className="text-xs">
                搜索负责人
                <input
                  className={`${field} mt-1`}
                  value={owner}
                  onChange={(e) => setOwner(e.target.value)}
                />
              </label>
            </div>
            <IssueList issues={issues} />
          </section>
          <ProjectActions key={id ?? "overview"} id={id} />
          {!!overdue.length && (
            <section className="min-w-0 rounded-2xl border bg-card p-4 sm:p-5">
              <h2 className="mb-3 text-lg font-semibold">此前逾期未完成</h2>
              <IssueList issues={overdue} />
            </section>
          )}
          {!id && (
            <section className="grid gap-2 sm:grid-cols-2">
              {projects.map((p) => (
                <Link
                  key={p.id}
                  href={`/projects/${p.id}`}
                  className="flex min-w-0 items-center gap-3 rounded-xl border bg-card px-4 py-3 text-sm hover:bg-muted"
                >
                  <FolderKanban className="size-4 shrink-0 text-emerald-700" />
                  <span className="min-w-0 break-words">{p.name}</span>
                </Link>
              ))}
            </section>
          )}
        </>
      )}
    </>
  )
}

function IssueList({ issues }: { issues: Issue[] }) {
  if (!issues.length)
    return (
      <p className="rounded-xl bg-muted/60 p-4 text-sm text-muted-foreground">
        暂无符合条件的事项。
      </p>
    )
  return (
    <ul className="divide-y">
      <li className="hidden grid-cols-[minmax(0,2fr)_minmax(0,.8fr)_minmax(0,.9fr)_minmax(0,1.25fr)] gap-4 px-2 py-3 text-xs font-medium text-muted-foreground md:grid">
        <span>交付事项</span>
        <span>负责人</span>
        <span>截止日期</span>
        <span>阻塞原因</span>
      </li>
      {issues.map((i) => (
        <li
          key={`${i.projectId}/${i.id}`}
          className="grid grid-cols-2 gap-2 px-2 py-3 text-sm md:grid-cols-[minmax(0,2fr)_minmax(0,.8fr)_minmax(0,.9fr)_minmax(0,1.25fr)] md:gap-4"
        >
          <div className="col-span-2 min-w-0 break-words md:col-span-1">
            <p className="text-xs text-muted-foreground">
              {i.id} · {i.state}
            </p>
            {i.linearUrl ? (
              <a className="font-medium hover:underline" href={i.linearUrl}>
                {i.title}
              </a>
            ) : (
              <span className="font-medium">{i.title}</span>
            )}
          </div>
          <p className="min-w-0 break-words">
            <span className="mr-1 text-xs text-muted-foreground md:hidden">
              负责人
            </span>
            <span>{i.owner || "未指派"}</span>
          </p>
          <p className="tabular-nums">
            <span className="mr-1 text-xs text-muted-foreground md:hidden">
              截止
            </span>
            <time>{i.dueDate || "未设置"}</time>
          </p>
          <p
            className={`col-span-2 break-words md:col-span-1 ${i.blocker ? "text-amber-800 dark:text-amber-300" : "text-muted-foreground"}`}
          >
            {i.blocker || "—"}
          </p>
        </li>
      ))}
    </ul>
  )
}
