"use client"
import { useEffect, useState } from "react"
import { api } from "@/lib/api"
import { ErrorNotice, Heading } from "@/components/workspace"
import { Button } from "@/components/ui/button"
type Item = {
  id: string
  state: string
  input: { title: string; body: string }
}
export function Proposals() {
  const [items, set] = useState<Item[]>()
  const [err, setErr] = useState("")
  const [busy, setBusy] = useState("")
  useEffect(() => {
    api<Item[]>("/api/admin/project-proposals")
      .then(set)
      .catch((e) => setErr(e.message))
  }, [])
  async function approve(id: string) {
    setBusy(id)
    try {
      await api(`/api/project-proposals/${id}/approve`, { method: "POST" })
      set(await api<Item[]>("/api/admin/project-proposals"))
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy("")
    }
  }
  return (
    <>
      <Heading
        eyebrow="PROJECT INTAKE"
        title="立项申请"
        description="审批只记录团队决定，不会自动创建外部项目。"
      />
      {err && <ErrorNotice message={err} />}
      <ul className="divide-y rounded-2xl border bg-card p-4">
        {items?.map((i) => (
          <li className="grid gap-2 py-3 break-words" key={i.id}>
            <h2 className="font-medium">{i.input.title}</h2>
            <p className="text-sm text-muted-foreground">{i.input.body}</p>
            {i.state === "approved" ? (
              <p className="text-sm">已批准</p>
            ) : (
              <Button
                className="justify-self-start"
                disabled={!!busy}
                onClick={() => void approve(i.id)}
              >
                批准申请
              </Button>
            )}
          </li>
        ))}
        {items?.length === 0 && <li>暂无申请。</li>}
      </ul>
    </>
  )
}
