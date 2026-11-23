import { Workspace } from "@/components/workspace"
import type { ReactNode } from "react"
export default function Layout({ children }: { children: ReactNode }) {
  return <Workspace admin>{children}</Workspace>
}
