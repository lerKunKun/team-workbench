import { Login } from "@/components/login"
export default async function Page({
  searchParams,
}: {
  searchParams: Promise<{ entry?: string; next?: string; error?: string }>
}) {
  const q = await searchParams
  return (
    <Login
      next={q.next}
      inside={q.entry === "dingtalk"}
      failed={Boolean(q.error)}
    />
  )
}
