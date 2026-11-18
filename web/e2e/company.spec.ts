// The wedding planner company routes of docs/process/e2e-test-routes.md:
// members log in by mail, add colleagues, create events and open the
// Master console together (US-CO-1 to US-CO-4). They run before the routes
// of routes.spec.ts, which stop the server at the end.
import { devices, expect, test } from "@playwright/test";
import type { Browser, BrowserContext, Page } from "@playwright/test";
import { COMPANIES, endEvent, issueLoginLink, loadState, mailLinks, target, today, tomorrow } from "./target";

const PROPAGATE_MS = 5000;
// The company page reads itself again every 10 s (CompanyPage.tsx POLL_MS).
const COMPANY_POLL_MS = 15000;
const IPHONE = devices["iPhone 13"];
const IPAD = devices["iPad Pro 11 landscape"];

let contexts: BrowserContext[] = [];

test.afterEach(async () => {
  await Promise.all(contexts.map((c) => c.close()));
  contexts = [];
});

// The sentence of US-CO-1.3, the same for members and everyone else.
const SENT = "如果這是成員的 email，登入信已經寄出，15 分鐘內有效。沒收到請看垃圾信件匣，過一陣子再試";
const DENIED_LOGIN = "這個登入連結已失效或用過了，請重新寄一封";

async function newPage(browser: Browser, device = IPHONE): Promise<Page> {
  const ctx = await browser.newContext({ ...device, baseURL: loadState().baseURL });
  contexts.push(ctx);
  return ctx.newPage();
}

// Every confirmation is recorded; it is accepted unless `accept` is turned off.
type Dialogs = { messages: string[]; accept: boolean };
function watchDialogs(page: Page): Dialogs {
  const d: Dialogs = { messages: [], accept: true };
  page.on("dialog", (dlg) => {
    d.messages.push(dlg.message());
    void (d.accept ? dlg.accept() : dlg.dismiss());
  });
  return d;
}

const stored = (page: Page) => page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
const hashToken = (page: Page) => new URL(page.url()).hash.slice(3);

// asMember opens the company page with a login link the developer issues
// (US-O-12.8), on either target.
async function asMember(browser: Browser, email: string): Promise<{ page: Page; token: string }> {
  const token = issueLoginLink(email);
  const page = await newPage(browser);
  await page.goto(`/company#t=${token}`);
  return { page, token };
}

const eventList = (p: Page) => p.locator(".admin-list").nth(0);
const memberList = (p: Page) => p.locator(".admin-list").nth(1);

async function createEvent(page: Page, name: string, date: string) {
  await page.getByLabel("活動名稱").fill(name);
  await page.getByLabel("活動日期").fill(date);
  await page.getByRole("button", { name: "新增活動" }).click();
}

// ---------------------------------------------------------------- 公司登入與同事

test("公司登入與同事：登入信、登入連結不進儲存、加入與移除同事", async ({ browser }) => {
  const co = COMPANIES.login;
  const phone = await newPage(browser);
  const roleRequests: string[] = [];
  phone.on("request", (r) => r.url().includes("/api/snapshot") && roleRequests.push(r.url()));

  await test.step("輸入壞的 email 被擋下，不寄信（US-CO-1.1、1.2）", async () => {
    await phone.goto("/login");
    await phone.getByLabel("email").fill("not-an-email");
    await phone.getByRole("button", { name: "寄登入信" }).click();
    await expect(phone.getByRole("alert")).toHaveText("email 格式不正確");
    expect(roleRequests).toEqual([]);
  });

  await test.step("成員與非成員看到同一句話（US-CO-1.3）", async () => {
    await phone.getByLabel("email").fill(` ${co.email.toUpperCase()} `); // cleaned and lower-cased (US-O-11.2)
    await phone.getByRole("button", { name: "寄登入信" }).click();
    await expect(phone.getByRole("status")).toHaveText(SENT);
    await phone.getByLabel("email").fill(co.stranger);
    await phone.getByRole("button", { name: "寄登入信" }).click();
    await expect(phone.getByRole("status")).toHaveText(SENT);
    // The same sentence wherever the mail goes; only the member's mail is written (US-CO-1.9).
    if (target === "local") {
      await expect.poll(() => mailLinks(co.email).length).toBe(1);
      expect(mailLinks(co.stranger)).toEqual([]);
    }
  });

  await test.step("信裡的連結：先清網址、只有「登入」、預取不會用掉、按下才進公司頁面（US-CO-1.10、1.6）", async () => {
    // The VM's test service sends no mail: this step runs locally (US-CO-1.14).
    if (target !== "local") return;
    const [mailToken] = mailLinks(co.email);
    const confirms: string[] = [];
    const prefetch = await newPage(browser);
    prefetch.on("request", (r) => r.url().includes("/api/company/login/confirm") && confirms.push(r.url()));
    await prefetch.goto(`/login#t=${mailToken}`);
    await expect(prefetch).toHaveURL(/\/login$/); // the link left the address
    await expect(prefetch.getByRole("button", { name: "登入", exact: true })).toBeVisible();
    await expect(prefetch.getByLabel("email")).toHaveCount(0);
    expect(await stored(prefetch)).not.toContain(mailToken);
    await prefetch.waitForTimeout(500); // a mail scanner only opens: nothing is used up
    expect(confirms).toEqual([]);

    // Opened again later, the same link still works: pressed once, it lands on the company page.
    const real = await newPage(browser);
    await real.goto(`/login#t=${mailToken}`);
    await real.getByRole("button", { name: "登入", exact: true }).click();
    await expect(real).toHaveURL(/\/company#t=[A-Za-z0-9_-]{22}$/);
    const loginToken = hashToken(real);
    await expect(real.getByRole("heading", { name: co.name })).toBeVisible();
    const keep = await stored(real);
    expect(keep).not.toContain(loginToken);
    expect(keep).not.toContain(mailToken);

    // Used once: the scanner's page, pressed now, is refused (US-CO-1.10).
    await prefetch.getByRole("button", { name: "登入", exact: true }).click();
    await expect(prefetch.getByRole("alert")).toHaveText(DENIED_LOGIN);
    await expect(prefetch.getByRole("link", { name: "重新寄一封" })).toBeVisible();
  });

  await test.step("角色連結進不了公司頁面，只顯示請重新登入（US-CO-1.5）", async () => {
    await phone.goto(`/company#t=${loadState().events[0].總召}`);
    await expect(phone.getByRole("alert")).toHaveText("請重新登入");
    await expect(phone.getByRole("link", { name: "前往登入頁" })).toBeVisible();
  });

  const { page: owner, token } = await asMember(browser, co.email);
  const dialogs = watchDialogs(owner);
  await test.step("公司頁面列出公司名稱與成員，登入連結不寫入儲存（US-CO-2.1、US-CO-1.6）", async () => {
    await expect(owner.getByRole("heading", { name: co.name })).toBeVisible();
    await expect(memberList(owner).locator("li")).toHaveText([`${co.email}（你）`]);
    const keep = await stored(owner);
    expect(keep).not.toContain(token);
  });

  await test.step("加入同事；已經是成員的 email 被擋下（US-CO-2.1）", async () => {
    await owner.getByLabel("同事的 email").fill(co.colleague);
    await owner.getByRole("button", { name: "加入" }).click();
    await expect(memberList(owner).locator("li")).toHaveCount(2);
    await expect(memberList(owner).locator("li").nth(1)).toContainText(co.colleague);
    await owner.getByLabel("同事的 email").fill(co.colleague);
    await owner.getByRole("button", { name: "加入" }).click();
    await expect(owner.getByRole("alert")).toHaveText("這個 email 已經是成員");
  });

  await test.step("自己旁邊沒有「移除」，移除要二次確認，取消不送出（US-CO-2.3）", async () => {
    await expect(memberList(owner).locator("li").nth(0).getByRole("button")).toHaveCount(0);
    const removals: string[] = [];
    owner.on("request", (r) => r.url().includes("/api/company/members/remove") && removals.push(r.url()));
    const colleague = memberList(owner).locator("li", { hasText: co.colleague });
    dialogs.accept = false;
    await colleague.getByRole("button", { name: "移除" }).click();
    await expect.poll(() => dialogs.messages.length).toBe(1);
    expect(dialogs.messages[0]).toBe(`移除 ${co.colleague}？他的登入與開著的主控台會立刻失效。`);
    expect(removals).toEqual([]);
    await expect(memberList(owner).locator("li")).toHaveCount(2);
    dialogs.accept = true;
    await colleague.getByRole("button", { name: "移除" }).click();
    await expect(memberList(owner).locator("li")).toHaveCount(1);
    expect(dialogs.messages).toHaveLength(2);
  });

  await test.step("登出：頁面提示已開啟的主控台仍可使用，網址不再帶連結（US-CO-1.4）", async () => {
    await owner.getByRole("button", { name: "登出" }).click();
    await expect(owner.getByRole("heading", { name: "已登出" })).toBeVisible();
    await expect(owner.getByText("已開啟的主控台仍可使用；要收回請同事把你移除再加回")).toBeVisible();
    expect(owner.url()).not.toContain("#t=");
    const again = await newPage(browser);
    await again.goto(`/company#t=${token}`);
    await expect(again.getByRole("alert")).toHaveText("請重新登入"); // the logged-out link is dead
  });
});

// ---------------------------------------------------------------- 公司的活動與主控台

test("公司的活動與主控台：新增活動、開主控台、兩位成員同步、移除成員", async ({ browser }) => {
  test.setTimeout(5 * 60 * 1000);
  const co = COMPANIES.events;
  const { page: owner, token: ownerToken } = await asMember(browser, co.email);
  await expect(owner.getByRole("heading", { name: co.name })).toBeVisible();

  await test.step("加入同事，他用自己的登入連結進來（US-CO-2.1）", async () => {
    await owner.getByLabel("同事的 email").fill(co.colleague);
    await owner.getByRole("button", { name: "加入" }).click();
    await expect(memberList(owner).locator("li")).toHaveCount(2);
  });
  const { page: mate, token: mateToken } = await asMember(browser, co.colleague);
  await expect(mate.getByRole("heading", { name: co.name })).toBeVisible();

  const TODAY = "公司的今天活動";
  const LATER = "公司的明天活動";
  await test.step("新增活動：連結與復原碼只顯示這一次，之後清單出現它（US-CO-3.3、3.1）", async () => {
    await createEvent(owner, TODAY, today());
    await expect(owner.getByRole("heading", { name: "活動已建立" })).toBeVisible();
    await expect(owner.getByText("下面兩樣只顯示這一次，請分開保存。")).toBeVisible();
    await expect(owner.getByText("總召連結（交給當天的總召）")).toBeVisible();
    await expect(owner.getByText("復原碼（交給總召以外、連結弄丟時能幫忙換發的人）")).toBeVisible();
    const link = await owner.locator(".link-text").nth(0).inputValue();
    const recovery = await owner.locator(".link-text").nth(1).inputValue();
    expect(link).toMatch(/\/#t=[A-Za-z0-9_-]{22}$/);
    expect(recovery).toMatch(/^[0-9A-Z]{4}(-[0-9A-Z]{1,4}){6}$/);
    const keep = await stored(owner);
    expect(keep).not.toContain(link.split("#t=")[1]);
    expect(keep).not.toContain(recovery);
    await owner.getByRole("button", { name: "回到公司頁面" }).click();
    await expect(eventList(owner).locator("li")).toHaveText([new RegExp(`${TODAY}・${today()}・準備中`)]);
    await expect(owner.locator(".link-text")).toHaveCount(0);
    await owner.reload(); // never shown again
    await expect(owner.locator(".link-text")).toHaveCount(0);
    await expect(owner.getByRole("heading", { name: "活動已建立" })).toHaveCount(0);
  });

  await test.step("另一位成員在清單看到新活動（US-CO-4.C1 的自動部分；清單約 10 秒讀一次）", async () => {
    await expect(eventList(mate).locator("li", { hasText: TODAY })).toBeVisible({ timeout: COMPANY_POLL_MS });
  });

  await test.step("清單依日期排序，已結束的排在最後且沒有「開啟主控台」（US-CO-3.1、4.4）", async () => {
    await createEvent(owner, LATER, tomorrow());
    await owner.getByRole("button", { name: "回到公司頁面" }).click();
    await expect(eventList(owner).locator("li").nth(0)).toContainText(TODAY);
    await expect(eventList(owner).locator("li").nth(1)).toContainText(`${LATER}・${tomorrow()}・準備中`);
    if (target === "local") {
      await createEvent(owner, "公司的已結束活動", tomorrow());
      await owner.getByRole("button", { name: "回到公司頁面" }).click();
      endEvent("公司的已結束活動"); // its day is over: no screen can do this
      await owner.reload();
      const items = eventList(owner).locator("li");
      await expect(items).toHaveCount(3);
      await expect(items.nth(2)).toContainText("公司的已結束活動・2020-01-01・已結束");
      await expect(items.nth(2).getByRole("button")).toHaveCount(0);
      await expect(items.nth(0).getByRole("button", { name: "開啟主控台" })).toBeVisible();
    }
  });

  await test.step("開啟主控台：同一分頁、上一頁回公司頁面（US-CO-4.1）", async () => {
    const tabsBefore = mate.context().pages().length;
    await eventList(mate).locator("li", { hasText: TODAY }).getByRole("button", { name: "開啟主控台" }).click();
    await expect(mate).toHaveURL(/\/#t=[A-Za-z0-9_-]{22}$/);
    expect(mate.context().pages()).toHaveLength(tabsBefore); // no new tab
    await expect(mate.getByRole("button", { name: "編輯流程表" })).toBeVisible();
    await mate.goBack();
    await expect(mate).toHaveURL(/\/company#t=/);
    await expect(mate.getByRole("heading", { name: co.name })).toBeVisible();
  });

  await test.step("再開一次：本機只留下同一場活動的最新快照（US-CO-4.7）", async () => {
    const snaps = () => mate.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith("snap:")).length);
    await expect.poll(snaps).toBeGreaterThanOrEqual(1);
    await eventList(mate).locator("li", { hasText: TODAY }).getByRole("button", { name: "開啟主控台" }).click();
    await expect(mate).toHaveURL(/\/#t=[A-Za-z0-9_-]{22}$/);
    await expect(mate.getByRole("button", { name: "編輯流程表" })).toBeVisible();
    await expect.poll(snaps).toBe(1);
  });

  // The owner opens the same event's console on an iPad.
  const pad = await newPage(browser, IPAD);
  await pad.goto(`/company#t=${ownerToken}`);
  await eventList(pad).locator("li", { hasText: TODAY }).getByRole("button", { name: "開啟主控台" }).click();
  await expect(pad.getByRole("button", { name: "編輯流程表" })).toBeVisible();

  await test.step("一位在主控台新增兩個環節，另一位看到（US-CO-4.1）", async () => {
    await pad.getByRole("button", { name: "編輯流程表" }).click();
    await pad.getByRole("button", { name: "新增第一個環節" }).click();
    await pad.getByRole("dialog").getByLabel("標題").fill("新娘化妝");
    await pad.getByRole("dialog").getByRole("button", { name: "新增", exact: true }).click();
    await expect(pad.locator(".editor-row")).toHaveCount(1);
    await pad.locator(".editor").getByRole("button", { name: "關閉" }).click();
    await pad.locator(".timeline .cue", { hasText: "新娘化妝" }).click();
    await pad.getByRole("button", { name: "在此之後新增環節" }).click();
    await pad.getByRole("dialog").getByLabel("標題").fill("迎賓");
    await pad.getByRole("dialog").getByRole("button", { name: "新增", exact: true }).click();
    await expect(pad.locator(".timeline .cue")).toHaveCount(2);
    await expect(mate.locator(".timeline .cue")).toHaveCount(2, { timeout: PROPAGATE_MS });
  });

  await test.step("一位推進，公司頁面顯示進行中；平移後另一位看到（US-CO-3.1、US-CO-4.C1 的自動部分）", async () => {
    await pad.getByRole("button", { name: "推進", exact: true }).click();
    await expect(mate.locator(".current .cue-title")).toHaveText("新娘化妝", { timeout: PROPAGATE_MS });
    await pad.waitForTimeout(1200);
    await pad.getByRole("button", { name: "+10", exact: true }).click();
    await pad.getByRole("dialog").getByRole("button", { name: "確認平移" }).click();
    await expect(pad.getByRole("dialog")).toBeHidden();
    await expect(mate.locator(".timeline .cue", { hasText: "迎賓" }).locator(".original")).toBeVisible({ timeout: PROPAGATE_MS });
  });

  // The company page of the colleague stays open on another tab.
  const mateCompany = await mate.context().newPage();
  await mateCompany.goto(`/company#t=${mateToken}`);
  await expect(mateCompany.getByRole("heading", { name: co.name })).toBeVisible();
  await expect(eventList(mateCompany).locator("li", { hasText: TODAY })).toContainText("進行中", { timeout: COMPANY_POLL_MS });

  await test.step("移除同事：他的主控台顯示連結已失效，他的公司頁面請重新登入（US-CO-2.4、US-CO-4.C1 的自動部分）", async () => {
    watchDialogs(owner);
    await owner.reload();
    await memberList(owner).locator("li", { hasText: co.colleague }).getByRole("button", { name: "移除" }).click();
    await expect(memberList(owner).locator("li")).toHaveCount(1);
    await expect(mate.getByText("連結已失效，請向總召索取新連結")).toBeVisible({ timeout: PROPAGATE_MS });
    await expect(mateCompany.getByRole("alert")).toHaveText("請重新登入", { timeout: COMPANY_POLL_MS });
    await expect(mateCompany.getByRole("link", { name: "前往登入頁" })).toBeVisible();
    // The pad, the owner's own console, is not affected.
    await expect(pad.getByRole("button", { name: "推進", exact: true })).toBeVisible();
  });

  await test.step("一天最多 20 場，第 21 場顯示上限（US-CO-3.4 的畫面）", async () => {
    // 4 events so far (1 or 3 shown above, the owner's page lists what it has); fill up to 20 with the API.
    const have = await eventList(owner).locator("li").count();
    for (let i = have; i < 20; i++) {
      const r = await owner.request.post("/api/company/events", {
        headers: { Authorization: `Bearer ${ownerToken}` },
        data: { name: `補滿 ${i}`, date: tomorrow() },
      });
      expect(r.status()).toBe(200);
    }
    await owner.reload();
    await createEvent(owner, "第 21 場", tomorrow());
    await expect(owner.getByRole("alert")).toHaveText("今天建立的活動已達上限，明天再建");
  });
});
