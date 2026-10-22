// The end-to-end routes of docs/process/e2e-test-routes.md: each one acts
// as the people of one test event would, on an iPad (總召) and iPhones
// (everyone else), in Safari's engine. They check what the screens show,
// not how the code is built; the people-centred conditions (US-x-n.Cm)
// stay with real people on real phones.
import { devices, expect, test } from "@playwright/test";
import type { Browser, BrowserContext, Page } from "@playwright/test";
import { loadState, startServer, stopServer, target } from "./target";
import type { Links, RoleName } from "./target";

// NFR-2 is 3 seconds for 95% of devices; one poll is 2 seconds. A route
// waits a little longer, then reports the time it took.
const PROPAGATE_MS = 5000;
const OFFLINE_MS = 15000; // offline shows after about 10 s without an answer
const OUTAGE_S = Number(process.env.E2E_OUTAGE_SEC ?? (target === "vm" ? 60 : 15));

const IPAD = devices["iPad Pro 11 landscape"];
const IPHONE = devices["iPhone 13"];

let contexts: BrowserContext[] = [];

test.afterEach(async () => {
  await Promise.all(contexts.map((c) => c.close()));
  contexts = [];
});

function event(i: number): Links {
  return loadState().events[i];
}

// open opens a role's link on its own device, as tapping it in LINE would.
async function open(browser: Browser, links: Links, role: RoleName): Promise<Page> {
  const ctx = await browser.newContext({ ...(role === "總召" ? IPAD : IPHONE), baseURL: loadState().baseURL });
  contexts.push(ctx);
  const page = await ctx.newPage();
  page.on("dialog", (d) => void d.accept()); // the two-step confirmations
  await page.goto(`/#t=${links[role]}`);
  return page;
}

// timed runs an action, waits for its effect on another device and
// reports how long that took (NFR-2).
async function timed(label: string, effect: () => Promise<void>) {
  const t0 = Date.now();
  await effect();
  test.info().annotations.push({ type: "propagation", description: `${label}: ${Date.now() - t0} ms` });
}

const cueRow = (master: Page, title: string) => master.locator(".timeline .cue", { hasText: title });

async function openCue(master: Page, title: string) {
  await cueRow(master, title).click();
  await expect(master.getByRole("dialog")).toBeVisible();
}

async function closeDialog(master: Page) {
  await master.getByRole("dialog").getByRole("button", { name: "關閉" }).click();
}

async function advance(master: Page) {
  await master.getByRole("button", { name: "推進", exact: true }).click();
  await master.waitForTimeout(1200); // the buttons lock for about a second (US-M-3.3)
}

async function shift(master: Page, label: string) {
  await master.getByRole("button", { name: label, exact: true }).click();
  const panel = master.getByRole("dialog");
  await expect(panel.locator(".move").first()).toBeVisible();
  await panel.getByRole("button", { name: "確認平移" }).click();
  await expect(panel).toBeHidden();
  await master.waitForTimeout(1200);
}

async function undo(master: Page) {
  await master.getByRole("button", { name: /復原上一步/ }).click();
  await master.waitForTimeout(1200);
}

async function insertAfter(master: Page, after: string, title: string, role: string) {
  await openCue(master, after);
  await master.getByRole("button", { name: "在此之後新增環節" }).click();
  const d = master.getByRole("dialog");
  await d.getByLabel("標題").fill(title);
  await d.locator(".pick", { hasText: role }).getByRole("checkbox").check();
  await d.getByRole("button", { name: "新增", exact: true }).click();
  await expect(d).toBeHidden();
  await master.waitForTimeout(1200);
}

// ---------------------------------------------------------------- 總召的一天

test("總召的一天：略過、推進、平移、復原、新增、改派、編輯", async ({ browser }) => {
  const links = event(0);
  const master = await open(browser, links, "總召");
  await expect(master.locator(".current .label")).toHaveText("活動尚未開始");

  await test.step("活動開始前略過與復原（US-M-6.3、6.4）", async () => {
    await openCue(master, "迎賓");
    await master.getByRole("button", { name: "略過" }).click();
    await expect(cueRow(master, "迎賓")).toHaveClass(/skipped/);
    await expect(master.getByRole("button", { name: /復原上一步/ })).toContainText("撤銷：略過『迎賓』");
    await master.waitForTimeout(1200);
    await undo(master);
    await expect(cueRow(master, "迎賓")).toHaveClass(/pending/);
  });

  await test.step("第一次推進即開始活動（US-M-3.6）", async () => {
    await advance(master);
    await expect(master.locator(".current .cue-title")).toHaveText("新娘化妝");
  });

  await test.step("延後 10 分再復原（US-M-2、US-M-4.6）", async () => {
    await shift(master, "+10");
    await expect(cueRow(master, "迎賓").locator(".original")).toBeVisible();
    await expect(master.getByRole("button", { name: /復原上一步/ })).toContainText("撤銷：+10 分");
    await undo(master);
    await expect(cueRow(master, "迎賓").locator(".original")).toHaveCount(0);
  });

  await test.step("活動中新增環節並指派（US-M-8.1、8.2）", async () => {
    await insertAfter(master, "一進預備", "長輩致詞", "音控");
    const titles = await master.locator(".timeline .cue .title").allTextContents();
    expect(titles).toEqual(["新娘化妝", "迎賓", "一進預備", "長輩致詞", "敬酒", "送客"]);
  });

  await test.step("改派敬酒給音控（US-M-8.3）", async () => {
    await openCue(master, "敬酒");
    const d = master.getByRole("dialog");
    await d.locator(".pick", { hasText: "攝影" }).getByRole("checkbox").uncheck();
    await d.locator(".pick", { hasText: "音控" }).getByRole("checkbox").check();
    await d.getByRole("button", { name: "儲存指派" }).click();
    await expect(d.getByRole("button", { name: "儲存指派" })).toBeDisabled();
    await closeDialog(master);
  });

  await test.step("改標題（US-M-8.7）", async () => {
    await openCue(master, "送客");
    const d = master.getByRole("dialog");
    await d.getByLabel("標題").fill("送客合照");
    await d.getByRole("button", { name: "儲存標題與備註" }).click();
    await closeDialog(master);
    await expect(cueRow(master, "送客合照")).toBeVisible();
  });
});

// ---------------------------------------------------------------- 廠商收到變更

test("廠商收到變更：打卡、平移與新增環節的提示、目前環節", async ({ browser }) => {
  const links = event(1);
  const master = await open(browser, links, "總召");
  const media = await open(browser, links, "攝影");
  const stylist = await open(browser, links, "新秘");
  const av = await open(browser, links, "音控");

  await test.step("攝影按「我已就位」，總召看到（US-X-5.1、US-M-1.6）", async () => {
    await media.getByRole("button", { name: "我已就位" }).click();
    await expect(media.getByRole("button", { name: /已就位 ✔/ })).toBeVisible();
    await timed("打卡 → 總召", () =>
      expect(master.locator(".role-row", { hasText: "攝影" })).toContainText("● 已就位", { timeout: PROPAGATE_MS }),
    );
  });

  await test.step("新秘第一次開啟時回答已到場（US-X-5.2）", async () => {
    await expect(stylist.getByText("你已經到場了嗎？")).toBeVisible();
    await stylist.getByRole("button", { name: "我已就位" }).click();
    await expect(stylist.getByText("你已經到場了嗎？")).toBeHidden();
    await timed("新秘打卡 → 總召", () =>
      expect(master.locator(".role-row", { hasText: "新秘" })).toContainText("● 已就位", { timeout: PROPAGATE_MS }),
    );
  });

  await test.step("開始後攝影看到目前環節的名稱（US-X-6.4）", async () => {
    await advance(master);
    await expect(media.locator(".current-line")).toHaveText("目前：新娘化妝", { timeout: PROPAGATE_MS });
  });

  await test.step("總召延後 10 分，攝影與新秘看到時程已更新（US-ST-2.1、2.2）", async () => {
    await shift(master, "+10");
    await timed("平移 → 攝影", () =>
      expect(media.locator(".change-banner")).toContainText("時程已更新 +10 分", { timeout: PROPAGATE_MS }),
    );
    await expect(stylist.locator(".change-banner")).toContainText("時程已更新 +10 分", { timeout: PROPAGATE_MS });
  });

  await test.step("總召新增環節給攝影，攝影看到新增環節（US-X-7.1）", async () => {
    await insertAfter(master, "一進預備", "長輩致詞", "攝影");
    await timed("新增 → 攝影", () =>
      expect(media.locator(".change-banner")).toContainText("新增環節：長輩致詞", { timeout: PROPAGATE_MS }),
    );
  });

  await test.step("音控的備註以等寬字體顯示（US-X-6.6）", async () => {
    await expect(av.locator(".next-card .note.mono").first()).toContainText("welcome.mp3");
  });
});

// ---------------------------------------------------------------- 連結外流

test("連結外流：重發後舊連結失效、新連結可用、撤銷", async ({ browser }) => {
  const links = event(2);
  const master = await open(browser, links, "總召");
  const media = await open(browser, links, "攝影");
  await expect(media.locator(".glance-head")).toHaveText("攝影");

  let link = "";
  await test.step("總召重發攝影的連結（US-M-7.1）", async () => {
    await master.locator(".role-row", { hasText: "攝影" }).click();
    await master.getByRole("button", { name: "重發連結" }).click();
    const text = master.locator(".link-text");
    await expect(text).toHaveValue(/#t=[A-Za-z0-9_-]{22}$/);
    link = await text.inputValue();
    await expect(master.getByRole("dialog").locator("a")).toHaveCount(0); // text, never a link to tap
    await closeDialog(master);
  });

  await test.step("持舊連結的手機看到連結已失效（US-M-7.3）", async () => {
    await timed("重發 → 舊連結失效", () =>
      expect(media.getByText("連結已失效，請向總召索取新連結")).toBeVisible({ timeout: PROPAGATE_MS }),
    );
  });

  await test.step("新連結可用", async () => {
    const page = await open(browser, { ...links, 攝影: link.split("#t=")[1] }, "攝影");
    await expect(page.locator(".glance-head")).toHaveText("攝影");
  });

  await test.step("撤銷後新連結也失效（US-M-7.2）", async () => {
    await master.locator(".role-row", { hasText: "攝影" }).click();
    await master.getByRole("button", { name: "撤銷連結" }).click();
    await expect(master.locator(".role-row", { hasText: "攝影" })).toContainText("連結已撤銷", { timeout: PROPAGATE_MS });
    const page = contexts[contexts.length - 1].pages()[0];
    await expect(page.getByText("連結已失效，請向總召索取新連結")).toBeVisible({ timeout: PROPAGATE_MS });
  });
});

// ---------------------------------------------------------------- 斷網

test("斷網：仍看得到時程與倒數、打卡停用、恢復後自己更新", async ({ browser }) => {
  const links = event(3);
  const master = await open(browser, links, "總召");
  const media = await open(browser, links, "攝影");
  await advance(master);
  await expect(media.locator(".next-card")).toBeVisible();

  await test.step("攝影的手機斷網（US-X-8.1、8.3）", async () => {
    await media.context().setOffline(true);
    await expect(media.locator(".offline-banner")).toContainText("離線中", { timeout: OFFLINE_MS });
    await expect(media.locator(".next-card .count")).toContainText("倒數");
    await expect(media.getByRole("button", { name: "我已就位" })).toBeDisabled(); // US-X-5.7
  });

  await test.step("斷網中重新開啟頁面（US-X-8.C1 的自動部分）", async () => {
    await media.reload().catch(() => undefined);
    await expect(media.locator(".next-card")).toBeVisible({ timeout: OFFLINE_MS });
    await expect(media.locator(".offline-banner")).toContainText("離線中", { timeout: OFFLINE_MS });
  });

  await test.step("總召斷網時不能操作（US-X-8.4）", async () => {
    await master.context().setOffline(true);
    await expect(master.locator(".offline-banner")).toBeVisible({ timeout: OFFLINE_MS });
    await expect(master.getByRole("button", { name: "推進", exact: true })).toBeDisabled();
    await master.context().setOffline(false);
    await expect(master.locator(".offline-banner")).toBeHidden({ timeout: OFFLINE_MS });
  });

  await test.step("恢復連線後自己更新，不必重新整理", async () => {
    await shift(master, "+10");
    await media.context().setOffline(false);
    await expect(media.locator(".offline-banner")).toBeHidden({ timeout: OFFLINE_MS });
    await expect(media.locator(".change-banner")).toContainText("時程已更新 +10 分", { timeout: PROPAGATE_MS });
  });
});

// ---------------------------------------------------------------- 新人

test("新人儀表板：開始前、一切順利、延誤、復原", async ({ browser }) => {
  const links = event(4);
  const master = await open(browser, links, "總召");
  const host = await open(browser, links, "新人");
  await expect(host.locator(".dashboard .state")).toHaveText("活動尚未開始");

  await advance(master);
  await expect(host.locator(".dashboard .state")).toHaveText("一切順利", { timeout: PROPAGATE_MS });
  await expect(host.locator(".dashboard .current")).toHaveText("目前：新娘化妝");
  await expect(host.getByRole("button")).toHaveCount(0); // US-H-1.4

  await shift(master, "+10");
  await timed("平移 → 新人", () =>
    expect(host.locator(".dashboard .state")).toHaveText("延誤 10 分", { timeout: PROPAGATE_MS }),
  );
  await undo(master);
  await expect(host.locator(".dashboard .state")).toHaveText("一切順利", { timeout: PROPAGATE_MS });
});

// ---------------------------------------------------------------- 伺服器重啟（最後）

test("伺服器停擺：各裝置顯示離線、恢復後自己更新", async ({ browser }) => {
  test.setTimeout((OUTAGE_S + 180) * 1000);
  const links = event(5);
  const master = await open(browser, links, "總召");
  const media = await open(browser, links, "攝影");
  await advance(master);
  await expect(media.locator(".next-card")).toBeVisible();

  await test.step(`停掉伺服器 ${OUTAGE_S} 秒（US-X-8.C2 的自動部分）`, async () => {
    await stopServer();
    await expect(media.locator(".offline-banner")).toContainText("離線中", { timeout: OFFLINE_MS });
    await expect(master.getByRole("button", { name: "推進", exact: true })).toBeDisabled({ timeout: OFFLINE_MS });
    await expect(media.locator(".next-card .count")).toContainText("倒數");
    await media.waitForTimeout(OUTAGE_S * 1000);
  });

  await test.step("重新啟動後各裝置自己恢復", async () => {
    await startServer();
    await expect(media.locator(".offline-banner")).toBeHidden({ timeout: OFFLINE_MS });
    await expect(master.locator(".offline-banner")).toBeHidden({ timeout: OFFLINE_MS });
    await advance(master);
    await expect(media.locator(".current-line")).toHaveText("目前：迎賓", { timeout: PROPAGATE_MS });
  });
});
