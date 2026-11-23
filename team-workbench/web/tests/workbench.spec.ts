import { expect, test, type Page } from "@playwright/test"

const p = "e98503aa-4127-4f1e-8996-a82239824aa9"
const project = { id: p, name: "演示项目", summary: "", owner: "苏宁", url: "" }
async function routes(page: Page, role = "admin", failProjects = false) {
  let state = "triage"
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === "/api/auth/me")
      return route.fulfill({
        json: {
          id: "u",
          username: "user",
          name: "测试用户",
          role,
          projects: { [p]: "member" },
        },
      })
    if (path === "/api/projects")
      return failProjects
        ? route.fulfill({
            status: 503,
            json: { error: { message: "项目数据暂不可用" } },
          })
        : route.fulfill({
            json: {
              projects: [project],
              week: "2026-11-23",
              source: "demo",
              overdue: [],
              deliveries: [
                {
                  id: "BIO-1",
                  title: "本周样例",
                  owner: "苏宁",
                  dueDate: "2026-11-24",
                  projectId: p,
                  state: "进行中",
                  blocker: "等待接口",
                  linearUrl: "",
                  done: false,
                },
              ],
            },
          })
    if (path.endsWith("/status")) {
      state = route.request().postDataJSON().state
      return route.fulfill({ json: { ok: true } })
    }
    if (path === "/api/roadmap" && role === "admin")
      return route.fulfill({
        json: [
          {
            id: "r",
            projectId: p,
            title: "优化反馈分流",
            state,
            feedbackCount: 1,
            planCount: 0,
            sources: { web: 1 },
            generation: "",
          },
        ],
      })
    if (path === "/api/admin/users")
      return route.fulfill({
        status: 403,
        json: { error: { message: "无管理权限" } },
      })
    return route.fulfill({ status: 404, json: {} })
  })
}

test("mobile login and deliveries fit the viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await routes(page)
  await page.goto("/login")
  await expect(page.getByRole("heading", { name: "登录工作台" })).toBeVisible()
  await page.goto("/projects")
  await expect(page.getByText("本周样例")).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(
    390
  )
})
test("admin login keeps password on this host and links DingTalk to the workbench", async ({
  page,
}) => {
  await page.route("**/api/auth/options", (route) =>
    route.fulfill({
      json: {
        password: true,
        adminPortal: true,
        dingQr: false,
        dingSso: false,
      },
    })
  )
  await page.route("**/api/auth/login", (route) =>
    route.fulfill({ json: { role: "admin" } })
  )
  await page.route("**/api/auth/me", (route) =>
    route.fulfill({ json: { role: "admin", name: "Example Admin", projects: {} } })
  )
  await page.route("**/api/admin/users", (route) => route.fulfill({ json: [] }))
  await page.route("**/api/projects", (route) =>
    route.fulfill({ json: { projects: [] } })
  )
  await page.goto("/login?next=/projects")
  await expect(
    page.getByRole("heading", { name: "登录管理后台" })
  ).toBeVisible()
  await expect(
    page.getByRole("link", { name: "前往团队工作台使用钉钉扫码" })
  ).toHaveAttribute("href", "https://workbench.example.com/login")
  await expect(page.getByRole("button", { name: /钉钉端内免登/ })).toHaveCount(
    0
  )
  await page.getByRole("textbox", { name: "用户名" }).fill("demo-admin")
  await page.getByLabel("密码", { exact: true }).fill("test-password-only")
  await page.getByRole("button", { name: "系统账号登录" }).click()
  await expect(page).toHaveURL(/\/admin\/users$/)
})

test("desktop shows ownership, due date and blocker", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await routes(page)
  await page.goto("/projects")
  await expect(
    page
      .locator("main")
      .getByText("负责人", { exact: true })
      .filter({ visible: true })
  ).toBeVisible()
  await expect(page.getByText("苏宁", { exact: true })).toBeVisible()
  await expect(page.getByText("2026-11-24", { exact: true })).toBeVisible()
  await expect(page.getByText("等待接口", { exact: true })).toBeVisible()
})
test("keyboard can move a roadmap item without dragging", async ({ page }) => {
  await routes(page)
  await page.goto("/roadmap/manage")
  const control = page.getByRole("combobox", { name: "移动 优化反馈分流" })
  await control.selectOption("confirmed")
  await control.press("Tab")
  const submit = page.getByRole("button", { name: "应用状态 优化反馈分流" })
  await expect(submit).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("status")).toHaveText(/已更新/)
})
test("the retired local public Roadmap redirects to the external site", async ({ request }) => {
  const res = await request.get("/roadmap", { maxRedirects: 0 })
  expect(res.status()).toBe(307)
  expect(res.headers().location).toBe("https://roadmap.example.com/")
})
test("member cannot open management", async ({ page }) => {
  await routes(page, "member")
  await page.goto("/admin/users")
  await expect(page.getByText("无管理权限", { exact: true })).toBeVisible()
})
test("source outage is not shown as zero delivery", async ({ page }) => {
  await routes(page, "admin", true)
  await page.goto("/projects")
  await expect(page.locator("main").getByRole("alert")).toHaveText(
    /项目数据暂不可用/
  )
  await expect(page.getByText("本周交付数", { exact: true })).toHaveCount(0)
})
