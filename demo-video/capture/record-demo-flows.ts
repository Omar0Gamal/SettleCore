import fs from "node:fs";
import path from "node:path";
import { expect, test, type Locator, type Page } from "@playwright/test";

const CAPTURES_DIR = path.resolve(process.cwd(), "public", "captures");
const VIEWPORT_W = 1920;
const VIEWPORT_H = 1080;

interface NormalizedBox {
  x: number;
  y: number;
  w: number;
  h: number;
}

interface MeasuredControl {
  x: number;
  y: number;
  targetBox: NormalizedBox;
}

function round5(n: number): number {
  return Math.round(n * 100_000) / 100_000;
}

async function firstVisible(candidates: Locator[]): Promise<Locator> {
  for (const loc of candidates) {
    const count = await loc.count();
    if (count > 0 && (await loc.first().isVisible())) {
      return loc.first();
    }
  }
  return candidates[0]!;
}

async function measureControl(locator: Locator, fallback: MeasuredControl): Promise<MeasuredControl> {
  try {
    await locator.scrollIntoViewIfNeeded().catch(() => undefined);
    const box = await locator.boundingBox();
    if (!box || box.width <= 0 || box.height <= 0) {
      return fallback;
    }
    return {
      x: round5((box.x + box.width / 2) / VIEWPORT_W),
      y: round5((box.y + box.height / 2) / VIEWPORT_H),
      targetBox: {
        x: round5(box.x / VIEWPORT_W),
        y: round5(box.y / VIEWPORT_H),
        w: round5(box.width / VIEWPORT_W),
        h: round5(box.height / VIEWPORT_H),
      },
    };
  } catch {
    return fallback;
  }
}

async function seekDemoClock(page: Page, frame: number): Promise<void> {
  await page.evaluate((f) => {
    const w = window as unknown as { __demo?: { seek?: (frame: number) => void } };
    if (w.__demo && typeof w.__demo.seek === "function") {
      w.__demo.seek(f);
    }
  }, frame);
  await page.waitForTimeout(250);
}

async function capturePng(page: Page, filename: string): Promise<void> {
  fs.mkdirSync(CAPTURES_DIR, { recursive: true });
  const target = path.join(CAPTURES_DIR, filename);
  await page.screenshot({ path: target, fullPage: false });
}

test.describe("SettleCore 4K Retina (3840x2160) Launch Video Capture", () => {
  test("captures 9 click-synchronized 4K states and live control waypoints", async ({ page }) => {
    await page.setViewportSize({ width: VIEWPORT_W, height: VIEWPORT_H });
    await page.goto("/?demo=1", { waitUntil: "networkidle" });

    // Wait for main desk + order book + candlesticks to mount cleanly
    await expect(page.getByText("SettleCore").first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText(/Order Entry|Place Order/i).first()).toBeVisible({ timeout: 20_000 });
    await page.waitForTimeout(1800);

    // Dismiss any first-visit architecture guide modal if open during capture
    const skipTourBtn = page.getByTestId("tour-close-btn").or(page.getByRole("button", { name: /Close architecture guide|Close guided tour|Skip/i }));
    if ((await skipTourBtn.count()) > 0 && (await skipTourBtn.first().isVisible())) {
      await skipTourBtn.first().click();
      await page.waitForTimeout(250);
    }

    const controls: Record<string, MeasuredControl> = {};

    // Measure Screen 1 controls prior to interaction
    const buyBtn = await firstVisible([
      page.getByTestId("order-side-buy"),
      page.getByRole("button", { name: /^BUY/i }),
    ]);
    controls.orderSideBuy = await measureControl(buyBtn, {
      x: 0.81669,
      y: 0.14537,
      targetBox: { x: 0.75924, y: 0.13241, w: 0.11491, h: 0.02593 },
    });

    const limitTab = await firstVisible([
      page.getByTestId("order-type-limit"),
      page.getByRole("button", { name: /^LIMIT$/i }),
    ]);
    controls.orderTypeLimit = await measureControl(limitTab, {
      x: 0.76381,
      y: 0.18333,
      targetBox: { x: 0.75663, y: 0.17407, w: 0.01436, h: 0.01852 },
    });

    const priceInput = await firstVisible([
      page.getByTestId("order-price-input"),
      page.locator("#order-price"),
    ]);
    controls.orderPriceInput = await measureControl(priceInput, {
      x: 0.86361,
      y: 0.24907,
      targetBox: { x: 0.77382, y: 0.23426, w: 0.17958, h: 0.02963 },
    });

    const qtyInput = await firstVisible([
      page.getByTestId("order-qty-input"),
      page.locator("#order-qty"),
    ]);
    controls.orderQtyInput = await measureControl(qtyInput, {
      x: 0.86448,
      y: 0.31065,
      targetBox: { x: 0.75715, y: 0.29583, w: 0.21465, h: 0.02963 },
    });

    const submitBtn = await firstVisible([
      page.getByTestId("submit-order-btn"),
      page.locator('form button[type="submit"]'),
    ]);
    controls.submitOrderBtn = await measureControl(submitBtn, {
      x: 0.87519,
      y: 0.52778,
      targetBox: { x: 0.75663, y: 0.51111, w: 0.23711, h: 0.03333 },
    });

    const marketTab = await firstVisible([
      page.getByTestId("order-type-market"),
      page.getByRole("button", { name: /^MARKET$/i }),
    ]);
    controls.orderTypeMarket = await measureControl(marketTab, {
      x: 0.78771,
      y: 0.18333,
      targetBox: { x: 0.77725, y: 0.17407, w: 0.02092, h: 0.01852 },
    });

    // State 1: Screen 1 (Trade) initial state at seek(0)
    await seekDemoClock(page, 0);
    await capturePng(page, "step1-desk.png");

    // State 2: Price 3125.50 & Qty 2.50 typed + seek(60)
    await buyBtn.click();
    await limitTab.click();
    await priceInput.fill("3125.50");
    await qtyInput.fill("2.50");
    controls.submitOrderBtn = await measureControl(submitBtn, controls.submitOrderBtn);
    await seekDemoClock(page, 60);
    await capturePng(page, "step2-price-focused.png");

    // State 3: LIMIT BUY fill receipt (#9004130) + seek(120)
    await submitBtn.click();
    await page
      .waitForSelector('[data-testid="order-exec-receipt"], text=/9004130|wal_seq:/i', {
        timeout: 15_000,
      })
      .catch(() => undefined);
    await seekDemoClock(page, 120);
    await page.waitForTimeout(450);
    await capturePng(page, "step3-limit-filled.png");

    // State 4: MARKET BUY fill receipt (#9004131) + seek(180)
    controls.orderTypeMarket = await measureControl(marketTab, controls.orderTypeMarket);
    await marketTab.click();
    await page.waitForTimeout(200);

    const qtyInputMarket = await firstVisible([
      page.getByTestId("order-qty-input"),
      page.locator("#order-qty"),
    ]);
    controls.orderQtyInputMarket = await measureControl(qtyInputMarket, controls.orderQtyInput);
    await qtyInputMarket.fill("0.75");

    const submitBtnMarket = await firstVisible([
      page.getByTestId("submit-order-btn"),
      page.locator('form button[type="submit"]'),
    ]);
    controls.submitOrderBtnMarket = await measureControl(submitBtnMarket, controls.submitOrderBtn);
    await submitBtnMarket.click();
    await seekDemoClock(page, 180);
    await page.waitForTimeout(600);
    await capturePng(page, "step4-market-filled.png");

    // State 5: Open 2-Phase Order Lifecycle Inspector Drawer
    const inspectLifecycleBtn = await firstVisible([
      page.getByTestId("inspect-lifecycle-btn"),
      page.getByTestId("open-lifecycle-btn"),
      page.getByRole("button", { name: /Inspect.*Order Lifecycle|Order Lifecycle/i }),
      page.locator('[data-testid^="blotter-row-"]').first(),
    ]);
    controls.inspectLifecycleBtn = await measureControl(inspectLifecycleBtn, {
      x: 0.87,
      y: 0.62,
      targetBox: { x: 0.77, y: 0.602, w: 0.2, h: 0.032 },
    });
    await inspectLifecycleBtn.click();
    await page
      .waitForSelector('[data-testid="lifecycle-drawer"]', {
        timeout: 10_000,
      })
      .catch(() => undefined);
    await seekDemoClock(page, 240);
    await page.waitForTimeout(450);
    await capturePng(page, "step5-lifecycle-open.png");

    // Close lifecycle drawer if close button exists before switching workspace
    const closeLifecycleBtn = page.getByTestId("close-lifecycle-btn");
    if ((await closeLifecycleBtn.count()) > 0 && (await closeLifecycleBtn.first().isVisible())) {
      await closeLifecycleBtn.first().click();
      await page.waitForTimeout(250);
    }

    // Measure Chaos top nav tab
    const navTabChaos = await firstVisible([
      page.getByTestId("nav-tab-chaos"),
      page.getByRole("button", { name: /Chaos.*Lab/i }),
    ]);
    controls.navTabChaos = await measureControl(navTabChaos, {
      x: 0.29138,
      y: 0.02176,
      targetBox: { x: 0.24407, y: 0.0088, w: 0.09463, h: 0.02593 },
    });

    // State 6: Screen 3 (Chaos & Systems Lab) armed state
    await navTabChaos.click();
    await page
      .waitForSelector('[data-testid="chaos-trigger-reorg"]', { timeout: 10_000 })
      .catch(() => undefined);
    await page.waitForTimeout(550);

    const reorgBtn = await firstVisible([
      page.getByTestId("chaos-trigger-reorg"),
      page.getByRole("button", { name: /3-Block Reorg/i }),
    ]);
    controls.chaosTriggerReorg = await measureControl(reorgBtn, {
      x: 0.20859,
      y: 0.44363,
      targetBox: { x: 0.11458, y: 0.3787, w: 0.18802, h: 0.12986 },
    });
    await capturePng(page, "step6-chaos-armed.png");

    // State 7: Trigger 3-Block Reorg -> Orphaned block fork & deposit rollback
    await reorgBtn.click();
    await page
      .waitForSelector('text=/ORPHANED|ROLLED BACK/i', { timeout: 10_000 })
      .catch(() => undefined);
    await page.waitForTimeout(800);

    const depositBadge = page.getByTestId("deposit-status-badge").first();
    controls.depositStatusBadge = await measureControl(depositBadge, {
      x: 0.54282,
      y: 0.57847,
      targetBox: { x: 0.47454, y: 0.56875, w: 0.13657, h: 0.01944 },
    });

    const navTabPor = await firstVisible([
      page.getByTestId("nav-tab-por"),
      page.getByRole("button", { name: /Proof of Reserves/i }),
    ]);
    controls.navTabPor = await measureControl(navTabPor, {
      x: 0.38253,
      y: 0.02176,
      targetBox: { x: 0.34078, y: 0.0088, w: 0.0835, h: 0.02593 },
    });
    await capturePng(page, "step7-chaos-reorg.png");

    // State 8: Screen 4 (Proof of Reserves) BEFORE clicking Verify Merkle Proof
    await navTabPor.click();
    await page
      .waitForSelector('[data-testid="verify-por-btn"]', { timeout: 10_000 })
      .catch(() => undefined);
    await page.waitForTimeout(550);

    const verifyPorBtn = await firstVisible([
      page.getByTestId("verify-por-btn"),
      page.getByRole("button", { name: /Verify.*Merkle Proof/i }),
    ]);
    controls.verifyPorBtn = await measureControl(verifyPorBtn, {
      x: 0.1585,
      y: 0.65599,
      targetBox: { x: 0.11458, y: 0.64117, w: 0.08784, h: 0.02963 },
    });
    await capturePng(page, "step8-por-unverified.png");

    // State 9: Screen 4 (Proof of Reserves) AFTER clicking Verify Merkle Proof
    await verifyPorBtn.click();
    await page
      .waitForSelector('[data-testid="por-verified-banner"], text=/Cryptographic Inclusion|Merkle Root Verified/i', {
        timeout: 15_000,
      })
      .catch(() => undefined);
    await page.waitForTimeout(650);
    await capturePng(page, "step9-por-verified.png");

    // Persist measured normalized coordinates & target bounding boxes
    fs.mkdirSync(CAPTURES_DIR, { recursive: true });
    const waypointsPath = path.join(CAPTURES_DIR, "waypoints.json");
    fs.writeFileSync(
      waypointsPath,
      JSON.stringify(
        {
          viewport: { width: VIEWPORT_W, height: VIEWPORT_H, deviceScaleFactor: 2 },
          controls,
        },
        null,
        2
      ) + "\n",
      "utf8"
    );
  });
});
