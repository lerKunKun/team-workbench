import { Workspace } from "@/components/workspace"
import { ProjectBoard } from "@/components/project-board"
export default async function Page({
  params,
}: {
  params: Promise<{ id: string }>
}) {
  return (
    <Workspace>
      <ProjectBoard id={(await params).id} />
    </Workspace>
  )
}
