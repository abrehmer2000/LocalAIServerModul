// Run with Playwright installed: node scripts/desktop_browser_test.cjs.
// The browser exercises real assets; a local fixture controls backend failures
// and long operations without downloading multi-GB model weights in CI.
const { chromium } = require("playwright");
const AxeBuilder = require("@axe-core/playwright").default;
const assert = require("node:assert/strict");
const http = require("node:http");
const fs = require("node:fs");
const path = require("node:path");

async function main() {
  let calls = [];
  let authenticate = true;
  let status = {};
  let failPost = false;
  const initial = () => ({
    stage: "welcome",
    connected: false,
    ready: false,
    busy: false,
    runtime: {
      supported: true,
      installed: false,
      version: "b12128",
      platform: "windows/amd64",
      size: 24730477,
      sha256: "a".repeat(64),
      source:
        "https://github.com/ggml-org/llama.cpp/releases/download/b12128/runtime.zip",
    },
    model: {
      name: "Gemma",
      quantization: "Q4",
      source: "https://huggingface.co/google",
    },
    lifecycle: { mode: "desktop", can_quit: true },
  });
  status = initial();
  const server = http.createServer(async (req, res) => {
    if (req.url.startsWith("/desktop/")) {
      let body = "";
      for await (const chunk of req) body += chunk;
      calls.push({
        path: req.url,
        method: req.method,
        body,
        token: req.headers["x-desktop-token"],
      });
      res.setHeader("Content-Type", "application/json");
      if (req.url === "/desktop/session") {
        res.statusCode = authenticate ? 200 : 401;
        res.end(
          JSON.stringify(
            authenticate
              ? { authenticated: true }
              : { error: "authentication_required" },
          ),
        );
      } else if (req.url === "/desktop/status") {
        res.end(JSON.stringify(status));
      } else if (req.url === "/desktop/access") {
        res.end(
          JSON.stringify({
            base_url: "https://127.0.0.1:8443/v1",
            user: "desktop-client",
            model: "Gemma",
            key: "synthetic-application-key",
            ca_pem: "synthetic-public-ca",
          }),
        );
      } else if (failPost) {
        res.statusCode = 503;
        res.end(JSON.stringify({ error: "storage_failed" }));
      } else {
        if (req.url === "/desktop/prepare") {
          status = {
            ...status,
            busy: true,
            progress: {
              stage: "downloading",
              downloaded: 1000000,
              total: 24730477,
            },
          };
        }
        if (req.url === "/desktop/cancel")
          status = { ...status, busy: false, error: "canceled" };
        if (req.url === "/desktop/model")
          status = { ...status, report: { state: "loading" } };
        if (req.url === "/desktop/test")
          status = {
            ...status,
            busy: false,
            answer: "Hallo. <img src=x onerror=alert(1)>",
          };
        res.statusCode = 202;
        res.end(JSON.stringify({ accepted: true }));
      }
      return;
    }
    const asset = req.url === "/" ? "desktop.html" : req.url.slice(1);
    if (!["desktop.html", "desktop.js", "desktop.css"].includes(asset)) {
      res.writeHead(404);
      res.end();
      return;
    }
    res.setHeader(
      "Content-Type",
      asset.endsWith(".js")
        ? "text/javascript"
        : asset.endsWith(".css")
          ? "text/css"
          : "text/html",
    );
    res.end(
      fs.readFileSync(path.join(__dirname, "../internal/webui/assets", asset)),
    );
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({
    headless: true,
    executablePath: process.env.CHROMIUM_PATH || undefined,
    args: ["--no-sandbox"],
  });
  try {
    const context = await browser.newContext({
      viewport: { width: 1280, height: 1100 },
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${base}/#${"b".repeat(64)}`);
    await page.waitForFunction(
      () => !document.querySelector("#prepare-button").disabled,
    );
    assert.equal(
      new URL(page.url()).hash,
      "",
      "launch capability must disappear from URL",
    );
    assert.equal(
      calls.filter((c) => c.path === "/desktop/session")[0].token,
      "b".repeat(64),
    );
    assert.equal(
      calls.filter((c) => c.method === "POST" && c.path !== "/desktop/session")
        .length,
      0,
      "opening UI must not download executables",
    );
    if (process.env.KI_DESKTOP_SCREENSHOT)
      await page.screenshot({
        path: process.env.KI_DESKTOP_SCREENSHOT,
        fullPage: true,
      });
    const firstRunAccessibility = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
      .analyze();
    assert.deepEqual(
      firstRunAccessibility.violations.map(({ id, nodes }) => ({
        id,
        elements: nodes.map(({ target }) => target),
      })),
      [],
      "first-run accessibility violations",
    );
    await page
      .getByRole("button", { name: "Vorbereiten", exact: true })
      .click();
    await page.waitForFunction(
      () => !document.querySelector("#cancel-button").hidden,
    );
    assert.equal(await page.locator("#prepare-button").isDisabled(), true);
    await page.getByRole("button", { name: "Abbrechen", exact: true }).click();
    await page.waitForFunction(() =>
      document
        .querySelector("#error-notice")
        .textContent.includes("abgebrochen"),
    );
    assert.equal(
      await page.locator("#prepare-button").isEnabled(),
      true,
      "cancel should permit retry",
    );
    status = {
      ...initial(),
      connected: true,
      runtime: { ...initial().runtime, installed: true },
      report: { state: "idle" },
    };
    await page.reload();
    await page
      .getByRole("button", { name: "Modell herunterladen und starten" })
      .click();
    await page.waitForFunction(() =>
      document
        .querySelector("#progress-label")
        .textContent.includes("Speicher"),
    );
    status = { ...status, ready: true, report: { state: "running" } };
    await page.reload();
    await page.getByRole("button", { name: "Testantwort erzeugen" }).click();
    await page.waitForFunction(() =>
      document.querySelector("#test-answer").textContent.startsWith("Hallo."),
    );
    assert.equal(
      await page.locator("#test-answer img").count(),
      0,
      "engine output must be text, never markup",
    );
    assert.equal(
      await page.locator("#state-badge").innerText(),
      "Test bestanden",
    );
    assert.equal(
      await page.locator("#access-key").inputValue(),
      "",
      "application credential must not be fetched during status polling",
    );
    await page
      .getByRole("button", { name: "Verbindungsdaten anzeigen" })
      .click();
    await page.waitForFunction(
      () =>
        document.querySelector("#access-key").value ===
        "synthetic-application-key",
    );
    assert.equal(
      await page.locator("#access-key").getAttribute("type"),
      "password",
    );
    await page.getByRole("button", { name: "Ausblenden", exact: true }).click();
    assert.equal(
      await page.locator("#access-key").inputValue(),
      "",
      "hide must clear credential from DOM",
    );
    await page.setViewportSize({ width: 375, height: 900 });
    assert.equal(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      true,
      "mobile layout must not overflow",
    );
    if (process.env.KI_DESKTOP_MOBILE_SCREENSHOT)
      await page.screenshot({
        path: process.env.KI_DESKTOP_MOBILE_SCREENSHOT,
        fullPage: true,
      });
    const readyAccessibility = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
      .analyze();
    assert.deepEqual(
      readyAccessibility.violations.map(({ id, nodes }) => ({
        id,
        elements: nodes.map(({ target }) => target),
      })),
      [],
      "completed mobile accessibility violations",
    );
    status = {
      ...initial(),
      runtime: { supported: false, platform: "other/arch" },
    };
    await page.reload();
    await page.waitForFunction(() =>
      document
        .querySelector("#runtime-description")
        .textContent.includes("Kein geprüftes"),
    );
    assert.equal(await page.locator("#prepare-button").isDisabled(), true);
    status = initial();
    failPost = true;
    await page.reload();
    await page
      .getByRole("button", { name: "Vorbereiten", exact: true })
      .click();
    await page.waitForFunction(() =>
      document
        .querySelector("#error-notice")
        .textContent.includes("Schreibrechte"),
    );
    assert.equal(
      await page.locator("#prepare-button").isEnabled(),
      true,
      "request failure must permit retry",
    );
    authenticate = false;
    await page.goto("about:blank");
    await page.goto(`${base}/#${"c".repeat(64)}`);
    await page.waitForFunction(() =>
      document
        .querySelector("#connection-notice")
        .textContent.includes("Startmenü"),
    );
    assert.equal(new URL(page.url()).hash, "");
    assert.equal(await page.locator("#prepare-button").isDisabled(), true);
    assert.deepEqual(errors, []);
    console.log(
      "Desktop browser checks passed: consent, authentication, download/cancel/retry, readiness, safe answer, narrow layout, failures.",
    );
  } finally {
    await browser.close();
    await new Promise((resolve) => server.close(resolve));
  }
}
main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
