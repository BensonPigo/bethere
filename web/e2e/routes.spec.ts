// The end-to-end routes of docs/process/e2e-test-routes.md: each one acts
// as the people of one test event would, on an iPad (總召) and iPhones
// (everyone else), in Safari's engine. They check what the screens show,
// not how the code is built; the people-centred conditions (US-x-n.Cm)
// stay with real people on real phones.
import { devices, expect, test } from "@playwright/test";
import type { Browser, BrowserContext, Page } from "@playwright/test";
import { createInvite, loadState, startServer, stopServer, SUPPORT_EMAIL, target, tomorrow } from "./target";
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
    await expect(master.locator(".prep-guide")).toBeVisible();
    await advance(master);
    await expect(master.locator(".current .cue-title")).toHaveText("新娘化妝");
    await expect(master.locator(".prep-guide")).toHaveCount(0); // US-M-18.1
  });

  await test.step("預覽列出不會移動的固定環節（US-M-2.1）", async () => {
    await master.getByRole("button", { name: "+10", exact: true }).click();
    const panel = master.getByRole("dialog");
    await expect(panel.locator(".move.fixed")).toHaveText(/一進預備.*固定，不移動/);
    await panel.getByRole("button", { name: "取消" }).click();
    await expect(panel).toBeHidden();
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
    await expect(media.locator(".current-line")).toHaveText(/^目前：新娘化妝・/, { timeout: PROPAGATE_MS });
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

test("連結外流：重發後舊連結失效、新連結可用、撤銷、以復原碼換發總召連結", async ({ browser }) => {
  const links = event(2);
  const master = await open(browser, links, "總召");
  const media = await open(browser, links, "攝影");
  await expect(media.locator(".glance-head > span")).toHaveText("攝影");

  let link = "";
  await test.step("總召重發攝影的連結（US-M-7.1）", async () => {
    await master.locator(".role-row", { hasText: "攝影" }).click();
    await master.getByRole("button", { name: "重發連結" }).click();
    const text = master.locator(".link-text");
    await expect(text).toHaveValue(/#t=[A-Za-z0-9_-]{22}$/);
    link = await text.inputValue();
    await expect(master.getByRole("dialog").locator("a")).toHaveCount(0); // text, never a link to tap
    await expect(master.getByRole("dialog").locator("img.qr")).toBeVisible(); // US-M-13.1
    await closeDialog(master);
  });

  await test.step("持舊連結的手機看到連結已失效（US-M-7.3）", async () => {
    await timed("重發 → 舊連結失效", () =>
      expect(media.getByText("連結已失效，請向總召索取新連結")).toBeVisible({ timeout: PROPAGATE_MS }),
    );
  });

  await test.step("新連結可用", async () => {
    const page = await open(browser, { ...links, 攝影: link.split("#t=")[1] }, "攝影");
    await expect(page.locator(".glance-head > span")).toHaveText("攝影");
  });

  await test.step("撤銷後新連結也失效（US-M-7.2）", async () => {
    await master.locator(".role-row", { hasText: "攝影" }).click();
    await master.getByRole("button", { name: "撤銷連結" }).click();
    await expect(master.locator(".role-row", { hasText: "攝影" })).toContainText("連結未發或已撤銷", { timeout: PROPAGATE_MS });
    const page = contexts[contexts.length - 1].pages()[0];
    await expect(page.getByText("連結已失效，請向總召索取新連結")).toBeVisible({ timeout: PROPAGATE_MS });
  });

  await test.step("備援操作者以復原碼換發總召連結，舊的總召連結失效（US-O-3）", async () => {
    // Opened with another role's link in the address: the entry must not use it (US-O-3.1).
    const ctx = await browser.newContext({ ...IPHONE, baseURL: loadState().baseURL });
    contexts.push(ctx);
    const op = await ctx.newPage();
    const snapshots: string[] = [];
    op.on("request", (r) => r.url().includes("/api/snapshot") && snapshots.push(r.url()));
    await op.goto(`/recover#t=${links.新秘}`);
    const field = op.getByLabel("復原碼");
    await field.fill("AAAA-BBBB-CCCC-DDDD-EEEE-FFFF-GG");
    await op.getByRole("button", { name: "換發總召連結" }).click();
    await expect(op.getByRole("alert")).toHaveText("復原碼不正確");

    // Typed from paper: lower case and spaces.
    await field.fill(links.復原碼.toLowerCase().replaceAll("-", " "));
    await field.press("Enter");
    const text = op.locator(".link-text");
    await expect(text).toHaveValue(/#t=[A-Za-z0-9_-]{22}$/);
    await expect(op.locator("main a")).toHaveCount(0); // text, never a link to tap
    await expect(op.getByText("若是外流，請總召到角色清單檢查並重發可疑的連結")).toBeVisible();
    expect(op.url()).not.toContain(links.復原碼.slice(0, 4).toLowerCase());
    expect(snapshots).toEqual([]);

    await timed("換發 → 舊總召連結失效", () =>
      expect(master.getByText("連結已失效，請向總召索取新連結")).toBeVisible({ timeout: PROPAGATE_MS }),
    );
    const fresh = await open(browser, { ...links, 總召: (await text.inputValue()).split("#t=")[1] }, "總召");
    await expect(fresh.getByRole("button", { name: "推進", exact: true })).toBeVisible();
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

test("備援流程表：總召印全部或某角色，攝影印自己的，新人沒有", async ({ browser }) => {
  const links = event(4);
  const master = await open(browser, links, "總召");
  await master.getByRole("link", { name: "列印備援流程表" }).click();
  await expect(master).toHaveURL(/\/print#t=/);
  const rows = (p: Page) => p.locator(".print tbody tr");
  await expect(rows(master)).toHaveCount(5); // US-X-9.1
  await expect(rows(master).filter({ hasText: "迎賓" })).toContainText("攝影：大門口 welcome.mp3");
  await master.getByLabel("印給").selectOption({ label: "新秘" }); // US-X-9.2
  await expect(rows(master)).toHaveCount(3);
  await expect(master.locator(".print tbody")).not.toContainText("攝影");

  const media = await open(browser, links, "攝影");
  await media.getByRole("link", { name: "列印我的流程表" }).click(); // US-X-9.3
  await expect(rows(media)).toHaveCount(4);
  await expect(media.locator(".print tbody")).not.toContainText("新秘");
  await expect(media.locator(".print h1")).toContainText("攝影");

  const host = await open(browser, links, "新人");
  await host.goto(`/print#t=${links["新人"]}`);
  await expect(host.locator(".print")).toContainText("這個連結沒有可列印的流程表"); // US-X-9.4
});

test("現場工作：領班收到、迎賓查桌號與報到、呼叫機動組、禮金檢核", async ({ browser }) => {
  // Before the day (the second event of tomorrow): the Master sets up the guest list and the checklist.
  const prep = event(8);
  const master = await open(browser, prep, "總召");
  await master.getByRole("button", { name: /現場工作/ }).click();
  await master.getByRole("button", { name: "賓客名單" }).click();
  await master.locator(".jobs textarea").fill("陳大文\t5\t素食\n林阿姨\t主桌\t輪椅");
  await master.getByRole("button", { name: "匯入（取代整份名單）" }).click();
  await expect(master.locator(".jobs")).toContainText("已報到 0／2 位"); // US-M-16.2
  await master.getByRole("button", { name: "禮金檢核" }).click();
  await master.locator(".jobs textarea").fill("總金額確認\n簽名確認");
  await master.getByRole("button", { name: "儲存檢核表" }).click();
  await expect(master.locator(".jobs label.check")).toHaveCount(2); // US-M-16.3

  // US-RC-1.6, US-RC-1.2: one character finds the table; arrival reaches the Master.
  const reception = await open(browser, prep, "接待");
  await reception.locator(".guest-search").fill("陳");
  await expect(reception.locator(".guest")).toHaveCount(1);
  await expect(reception.locator(".guest")).toContainText("5 桌");
  await reception.locator(".guest").getByRole("button", { name: "報到" }).click();
  await master.getByRole("button", { name: "賓客名單" }).click();
  await timed("報到 → 總召", () => expect(master.locator(".jobs")).toContainText("已報到 1／2 位", { timeout: PROPAGATE_MS }));

  // US-RC-2.1, US-RN-1.1: a call for help, taken by a runner.
  const runner = await open(browser, prep, "機動組");
  await reception.getByRole("button", { name: "呼叫機動組" }).click();
  const task = runner.locator(".tasks .task", { hasText: "接待需要機動組支援" });
  await timed("呼叫 → 機動組", () => expect(task).toBeVisible({ timeout: PROPAGATE_MS }));
  await task.getByRole("button", { name: "我來" }).click();
  await expect(reception.locator(".call-state")).toContainText("處理中（機動組）", { timeout: PROPAGATE_MS });

  // US-CA-1.2: the cashier ticks an item; the Master sees who.
  const cashier = await open(browser, prep, "禮金");
  // The box follows the server's answer, never a guess (ADR-0005).
  await cashier.locator("label.check", { hasText: "總金額確認" }).locator("input").click();
  await expect(cashier.locator("label.check.done")).toContainText("總金額確認");
  await master.getByRole("button", { name: "禮金檢核" }).click();
  await expect(master.locator(".jobs label.check.done")).toContainText("禮金", { timeout: PROPAGATE_MS });

  // On the day (the seventh event of today): the venue lead answers its cue; the MC sees the delay.
  const day = event(6);
  const console_ = await open(browser, day, "總召");
  await advance(console_);
  const lead = await open(browser, day, "領班");
  await expect(lead.locator(".next-card")).toContainText("迎賓");
  await expect(lead.locator(".current-line")).toContainText("一切順利"); // US-X-10.2
  await lead.getByRole("button", { name: "收到" }).click(); // US-VL-1.1
  await timed("收到 → 總召", () =>
    expect(cueRow(console_, "迎賓")).toContainText("領班收到", { timeout: PROPAGATE_MS }),
  );
});

// ---------------------------------------------------------------- 婚前準備

// The tab-separated rundown a spreadsheet copies (US-M-12.1); 主持 is a role
// the event does not have yet (US-M-12.6).
const PASTED = [
  ["seq", "title", "start", "duration_min", "fixed", "roles", "role_note", "note"],
  ["1", "新娘化妝", "09:00", "60", "", "新秘", "新娘房", ""],
  ["2", "證婚", "10:30", "20", "Y", "主持;攝影", "台上", ""],
  ["3", "送客", "13:00", "30", "", "攝影", "", ""],
].map((r) => r.join("\t")).join("\n");

const editorRow = (master: Page, i: number) => master.locator(".editor-row").nth(i);

test("婚前準備：編輯流程表、新增角色與 QR Code、預覽、貼上匯入、刪除角色", async ({ browser }) => {
  const links = event(7); // the first event of tomorrow: its day has not begun
  const master = await open(browser, links, "總召");
  const stylist = await open(browser, links, "新秘");
  await expect(stylist.locator(".preday")).toContainText("新娘化妝");
  // Rundown, roles and every link are there: the checklist is one line (US-M-18.4).
  await expect(master.locator(".prep-guide.done")).toHaveText("婚前準備完成：活動日當天按「推進」開始");

  await test.step("改環節標題，新秘自己看到（US-M-10.2、US-M-10.C2 的功能部分）", async () => {
    await master.getByRole("button", { name: "編輯流程表" }).click();
    let row = editorRow(master, 0);
    await expect(row.locator("input.title")).toHaveValue("新娘化妝");
    // A value reported by a change event only, as some pickers do.
    await row.locator("input.time").evaluate((el: HTMLInputElement) => {
      el.value = "09:10";
      el.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await expect(row.getByRole("button", { name: "儲存" })).toBeEnabled();
    await row.getByRole("button", { name: "儲存" }).click();
    await master.waitForTimeout(1200);
    await expect(stylist.locator(".preday-row", { hasText: "新娘化妝" }).locator(".time")).toHaveText("09:10", { timeout: PROPAGATE_MS });
    row = editorRow(master, 0); // the row starts over from the new version
    await row.locator("input.title").fill("新娘化妝（加長）");
    await row.locator("input.duration").fill("45");
    // Typed as people type it, with no colon (US-M-10.C2 field test).
    await row.locator("input.time").fill("");
    await row.locator("input.time").pressSequentially("0920");
    await row.getByRole("button", { name: "儲存" }).click();
    await master.waitForTimeout(1200);
    await timed("編輯 → 新秘", () => expect(stylist.locator(".preday")).toContainText("新娘化妝（加長）", { timeout: PROPAGATE_MS }));
    await expect(stylist.locator(".preday-row", { hasText: "新娘化妝（加長）" }).locator(".time")).toHaveText("09:20");
  });

  await test.step("上移與刪除環節（US-M-10.3、10.4）", async () => {
    await editorRow(master, 3).getByRole("button", { name: "上移" }).click(); // 敬酒 above 一進預備
    await master.waitForTimeout(1200);
    await expect(editorRow(master, 2).locator("input.title")).toHaveValue("敬酒");
    await editorRow(master, 2).getByRole("button", { name: "刪除" }).click();
    await master.waitForTimeout(1200);
    await expect(master.locator(".editor-row")).toHaveCount(4);
    await master.locator(".editor").getByRole("button", { name: "關閉" }).click();
  });

  let link = "";
  await test.step("新增角色「伴娘」，連結附 QR Code，手機打開就是伴娘（US-M-11.1、11.2、US-M-13）", async () => {
    await master.getByRole("button", { name: "＋ 新增角色" }).click();
    const d = master.getByRole("dialog");
    await d.getByLabel("名稱").fill("伴娘");
    await d.getByRole("radio", { name: /快速瞄/ }).check();
    await d.getByRole("button", { name: "新增並產生連結" }).click();
    await expect(d.locator("img.qr")).toBeVisible();
    link = await d.locator(".link-text").inputValue();
    await closeDialog(master);
    const phone = await open(browser, { ...links, 新人: link.split("#t=")[1] }, "新人");
    await expect(phone.locator(".glance-head > span")).toHaveText("伴娘");
  });

  await test.step("預覽新秘看到的畫面，不必另一支手機（US-M-14）", async () => {
    await master.locator(".role-row", { hasText: "新秘" }).click();
    await master.getByRole("button", { name: "預覽此角色的畫面" }).click();
    const preview = master.locator(".role-preview");
    await expect(preview.locator(".preview-bar")).toContainText("預覽：新秘 看到的畫面");
    await expect(preview.locator(".preday")).toContainText("新娘化妝（加長）");
    await preview.getByRole("button", { name: "結束預覽" }).click();
    await expect(preview).toBeHidden();
  });

  await test.step("貼上整份流程表，缺少的角色一起建立（US-M-12）", async () => {
    await master.getByRole("button", { name: "編輯流程表" }).click();
    await master.getByRole("button", { name: "貼上匯入" }).click();
    await master.locator(".import-text").fill(PASTED);
    await master.getByRole("button", { name: "預覽", exact: true }).click();
    await expect(master.locator(".missing-roles")).toContainText("主持");
    await expect(master.locator(".import-preview tr")).toHaveCount(3);
    await master.getByRole("button", { name: "建立 1 個角色並匯入" }).click();
    await expect(master.locator(".import-text")).toBeHidden();
    await expect(master.locator(".editor-row")).toHaveCount(3);
    await expect(editorRow(master, 1).locator("input.title")).toHaveValue("證婚");
    await master.locator(".editor").getByRole("button", { name: "關閉" }).click();
    await expect(master.locator(".role-row", { hasText: "主持" })).toContainText("連結未發或已撤銷");
  });

  await test.step("刪除角色「伴娘」，她的手機看到連結已失效（US-M-11.5）", async () => {
    await master.locator(".role-row", { hasText: "伴娘" }).click();
    await master.getByRole("button", { name: "刪除角色" }).click();
    await expect(master.locator(".role-row", { hasText: "伴娘" })).toHaveCount(0);
    const phone = contexts[contexts.length - 1].pages()[0];
    await timed("刪除角色 → 手機", () =>
      expect(phone.getByText("連結已失效，請向總召索取新連結")).toBeVisible({ timeout: PROPAGATE_MS }),
    );
  });
});

// ---------------------------------------------------------------- 用邀請碼建立活動

// The support address on the way in and on the console (US-O-8.3); only
// the local server has one.
async function supportShown(page: Page) {
  if (target !== "local") return;
  await expect(page.locator(".support-note a")).toHaveAttribute("href", `mailto:${SUPPORT_EMAIL}`);
}

test("用邀請碼建立活動：首頁 → 建立 → 進入總召畫面，上一頁回不到復原碼，同一組碼不能再用", async ({ browser }) => {
  const code = createInvite();
  const ctx = await browser.newContext({ ...IPHONE, baseURL: loadState().baseURL });
  contexts.push(ctx);
  const page = await ctx.newPage();

  await test.step("/new 不讀網址上的連結：帶著總召連結開也只是建立頁（US-M-17.3）", async () => {
    await page.goto(`/new#t=${event(0)["總召"]}`);
    await expect(page.getByRole("heading", { name: "建立活動" })).toBeVisible();
    await expect(page.getByLabel("邀請碼")).toHaveValue("");
    await supportShown(page);
  });

  await test.step("首頁沒有連結：廠商的提示在上，下面才是建立活動的入口（US-M-17.2）", async () => {
    await page.goto("/");
    await expect(page.locator(".msg")).toHaveText("找不到連結資訊，請使用總召給你的完整連結開啟");
    await expect(page.locator(".msg ~ .invite-note")).toBeVisible();
    await supportShown(page);
    await page.getByRole("link", { name: "用邀請碼建立" }).click();
    await expect(page).toHaveURL(/\/new$/);
  });

  await test.step("輸入邀請碼、名稱與日期建立，拿到總召連結與復原碼（US-M-17.3、17.8）", async () => {
    await page.getByLabel("邀請碼").fill(code.toLowerCase());
    await page.getByLabel("活動名稱").fill("受邀的活動");
    await page.getByLabel("活動日期").fill(tomorrow());
    await page.getByRole("button", { name: "建立活動" }).click();
    await expect(page.getByRole("heading", { name: "活動已建立" })).toBeVisible();
    await expect(page.locator(".link-text").nth(1)).toHaveValue(/^[0-9A-Z]{4}(-[0-9A-Z]{1,4}){6}$/);
    const link = await page.locator(".link-text").nth(0).inputValue();
    const token = link.split("#t=")[1];
    const recovery = await page.locator(".link-text").nth(1).inputValue();
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
    expect(token).toHaveLength(22);
    expect(stored).not.toContain(token);
    expect(stored).not.toContain(recovery);
  });

  await test.step("進入總召畫面；上一頁回不到建立結果（US-M-17.8）", async () => {
    await page.getByRole("button", { name: "進入總召畫面" }).click();
    await expect(page.getByRole("button", { name: "編輯流程表" })).toBeVisible();
    await supportShown(page);
    // A new event: the checklist shows what to do, nothing ticked (US-M-18.1, 18.2).
    const guide = page.getByRole("region", { name: "婚前準備" });
    await expect(guide.locator("li .mark")).toHaveText(["○", "○", "○", "・"]);
    // Each step's button goes straight to the work (US-M-18.3).
    await guide.getByRole("button", { name: "開始" }).click();
    await expect(page.locator(".editor")).toBeVisible();
    await page.locator(".editor").getByRole("button", { name: "關閉" }).click();
    await guide.getByRole("button", { name: "新增" }).click();
    const d = page.getByRole("dialog");
    await d.getByLabel("名稱").fill("新秘");
    await d.getByRole("radio", { name: /免手持/ }).check();
    await d.getByRole("button", { name: "新增並產生連結" }).click();
    await expect(d.locator(".link-text")).toBeVisible();
    await d.getByRole("button", { name: "關閉" }).click();
    await expect(guide.locator("li .mark")).toHaveText(["○", "✔", "✔", "・"]);
    await page.goBack();
    // Safari may restore a page from its back-forward cache as it was; the
    // creation result must not be in the history at all.
    await expect(page).not.toHaveURL(/\/new$/);
    await expect(page.getByRole("heading", { name: "活動已建立" })).toBeHidden();
    await expect(page.locator(".link-text")).toHaveCount(0);
  });

  await test.step("同一組邀請碼再用一次被拒（US-M-17.6）", async () => {
    await page.goto("/new");
    await page.getByLabel("邀請碼").fill(code);
    await page.getByLabel("活動名稱").fill("第二場");
    await page.getByLabel("活動日期").fill(tomorrow());
    await page.getByRole("button", { name: "建立活動" }).click();
    await expect(page.getByRole("alert")).toHaveText("邀請碼不正確或已失效");
  });
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
    await expect(media.locator(".current-line")).toHaveText(/^目前：迎賓・/, { timeout: PROPAGATE_MS });
  });
});

// ---------------------------------------------------------------- 維護模式（本機）

test("維護模式：只有總召連得上，其他人離線但保留時程，解除後自己恢復", async ({ browser }) => {
  test.skip(target === "vm", "the VM's test service has no maintenance switch; the drill covers the VM (US-O-2.C1)");
  test.setTimeout(180 * 1000);
  const links = event(5);
  const master = await open(browser, links, "總召");
  const media = await open(browser, links, "攝影");
  await advance(master); // runs on its own too, not only after the outage route
  await expect(media.locator(".current-line")).toBeVisible({ timeout: PROPAGATE_MS });
  const before = await media.locator(".current-line").textContent();

  await test.step("以維護模式重新啟動：總召看到橫幅並照常推進，攝影顯示離線、時程還在（US-O-2.C1 的自動部分）", async () => {
    await stopServer();
    await startServer(true);
    await expect(master.locator(".maintenance-banner")).toHaveText("維護中，其他角色無法連線", { timeout: OFFLINE_MS });
    await expect(master.locator(".offline-banner")).toBeHidden({ timeout: OFFLINE_MS });
    await advance(master);
    await expect(media.locator(".offline-banner")).toContainText("離線中", { timeout: OFFLINE_MS });
    await expect(media.locator(".current-line")).toHaveText(before!);
    await expect(media.locator(".next-card")).toBeVisible();
  });

  await test.step("解除維護後攝影自己恢復並收到推進，總召的橫幅消失", async () => {
    await stopServer();
    await startServer();
    await expect(media.locator(".offline-banner")).toBeHidden({ timeout: OFFLINE_MS });
    await expect(media.locator(".current-line")).not.toHaveText(before!, { timeout: PROPAGATE_MS });
    await expect(master.locator(".maintenance-banner")).toBeHidden({ timeout: OFFLINE_MS });
  });
});
