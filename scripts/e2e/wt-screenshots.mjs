// Screenshots of every Libre WT page for the user guide (light and dark).
//
//   node scripts/e2e/wt-screenshots.mjs WT_URL OUT_DIR ADMIN_USER ADMIN_PASSWORD
//
// Runs against the system-test stack (scripts/e2e/wt-system-test.sh with
// WT_SYS_KEEP=1) after the system test, so the pages show its synthetic
// data. Drives a headless Chromium (/usr/sbin/chromium, or $CHROMIUM) over
// the DevTools protocol with Node's built-in WebSocket; no packages.
// Documentation only: the images are not verification records.
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, mkdtempSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const [WT, OUT, USER, PASS] = process.argv.slice(2);
if (!PASS) { console.error("usage: wt-screenshots.mjs WT_URL OUT_DIR USER PASSWORD"); process.exit(2); }
mkdirSync(OUT, { recursive: true });
const PORT = 9333;
const profile = mkdtempSync(join(process.env.TMPDIR || tmpdir(), "wtshot-"));
const chrome = spawn(process.env.CHROMIUM || "/usr/sbin/chromium", [
  "--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars", `--remote-debugging-port=${PORT}`,
  `--user-data-dir=${profile}`, "--no-first-run", "--disable-extensions", "about:blank"], { stdio: "ignore" });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function json(path, method = "GET") {
  for (let i = 0; i < 50; i++) {
    try { const r = await fetch(`http://127.0.0.1:${PORT}${path}`, { method }); return await r.json(); } catch { await sleep(200); }
  }
  throw new Error("Chromium did not start");
}
await json("/json/version");
const target = await json("/json/new?about:blank", "PUT");
const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((r) => ws.addEventListener("open", r, { once: true }));
let seq = 0;
const pending = new Map();
const waiters = [];
ws.addEventListener("message", (m) => {
  const msg = JSON.parse(m.data);
  if (msg.id && pending.has(msg.id)) {
    const { res, rej } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result);
  } else if (msg.method) {
    for (const w of waiters.filter((w) => w.name === msg.method)) { waiters.splice(waiters.indexOf(w), 1); w.res(msg.params); }
  }
});
const cdp = (method, params = {}) => new Promise((res, rej) => { const id = ++seq; pending.set(id, { res, rej }); ws.send(JSON.stringify({ id, method, params })); });
const once = (name, ms = 20000) => new Promise((res, rej) => { const w = { name, res }; waiters.push(w); setTimeout(() => rej(new Error("timeout " + name)), ms); });

await cdp("Page.enable");
await cdp("Page.setBypassCSP", { enabled: true }); // lets Runtime.evaluate fill forms; the pages themselves have no script
await cdp("Emulation.setDeviceMetricsOverride", { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });

async function go(url) { const l = once("Page.loadEventFired"); await cdp("Page.navigate", { url: WT + url }); await l; }
async function js(expr) {
  const r = await cdp("Runtime.evaluate", { expression: expr, returnByValue: true });
  if (r.exceptionDetails) throw new Error(expr + ": " + JSON.stringify(r.exceptionDetails));
  return r.result.value;
}
// submit runs a form-filling expression that ends in form.submit() and waits for the next page.
async function submit(expr) { const l = once("Page.loadEventFired"); await js(expr); await l; }
async function shot(name, scheme) {
  await sleep(150);
  const m = await cdp("Page.getLayoutMetrics");
  const h = Math.min(Math.ceil(m.cssContentSize.height), 2400);
  const r = await cdp("Page.captureScreenshot", { format: "webp", quality: 82, captureBeyondViewport: true, clip: { x: 0, y: 0, width: 1280, height: h, scale: 1 } });
  const file = join(OUT, `${name}-${scheme}.webp`);
  writeFileSync(file, Buffer.from(r.data, "base64"));
  const title = await js("document.title");
  const wide = await js("document.documentElement.scrollWidth");
  if (wide > 1280) { overflow++; console.log(`OVERFLOW ${name}-${scheme}: page is ${wide}px wide`); }
  console.log(`${file}\t${title}`);
}

const q = (s) => JSON.stringify(s);
const fill = (form, values) => `(() => { const f = document.querySelector(${q(form)}); if (!f) throw new Error("no form " + ${q(form)});
  for (const [n, v] of Object.entries(${JSON.stringify(values)})) { const e = [...f.elements].filter(x => x.name === n).pop();
    if (!e) throw new Error("no field " + n); if (e.type === "checkbox") e.checked = !!v; else e.value = v; }
  f.submit(); })()`;
const firstHref = (sel) => js(`(document.querySelector(${q(sel)})||{}).getAttribute ? document.querySelector(${q(sel)}).getAttribute("href") : ""`);

// Each step: [name, async () => navigate]. Order matters only for the action results.
const steps = [
  ["fleet", () => go("/")],
  ["about", () => go("/about")],
  ["dashboard", () => go("/i/dcm-1/")],
  ["status", () => go("/i/dcm-1/status")],
  ["exceptions", () => go("/i/dcm-1/exceptions?state=all")],
  ["exception", async () => { await go("/i/dcm-1/exceptions?state=all"); await go(await firstHref('a[href*="/exceptions/"]:not([href$="exceptions"])')); }],
  ["exception-review", async () => { await go("/i/dcm-1/exceptions?state=active"); const h = await firstHref('a[href*="/exceptions/"]:not([href$="exceptions"])'); await go(h + "/review?action=reject"); }],
  ["config", () => go("/i/dcm-1/config")],
  ["config-edit", () => go("/i/dcm-1/config/edit")],
  ["config-review", async () => { await go("/i/dcm-1/config/edit"); await submit(`(() => { const f = [...document.forms].find(f => f.setting && f.setting.value === "remote_aes");
      const n = [...f.elements].filter(e => e.name === "name" && e.type !== "hidden").pop(); const i = [...f.elements].indexOf(n); const els = [...f.elements];
      n.value = "VIEWER2"; els[i+1].value = "192.0.2.20"; els[i+2].value = "104"; els[i+3].value = "VIEWER2"; f.submit(); })()`); }],
  ["reload-review", async () => { await go("/i/dcm-1/config"); await submit(fill('form[action$="/reload/review"]', {})); }],
  ["rollback-review", async () => { await go("/i/dcm-1/config"); await submit(fill('form[action$="/rollback/review"]', {})); }],
  ["remote-aes", () => go("/i/dcm-1/remote-aes")],
  ["allow-list", () => go("/i/dcm-2/allow-list")],
  ["echo", () => go("/i/dcm-1/echo")],
  ["echo-review", async () => { await go("/i/dcm-1/echo"); await submit(fill('form[action$="/echo/review"]', {})); }],
  ["echo-result", async () => { await go("/i/dcm-1/echo"); await submit(fill('form[action$="/echo/review"]', {}));
      await submit(fill('form[action$="/echo/apply"]', { reason: "screenshot for the user guide", approve: true })); }],
  ["license", () => go("/i/dcm-1/license")],
  ["license-unlicensed", () => go("/i/dcm-2/license")],
  ["license-ack-review", async () => { await go("/i/dcm-2/license"); await submit(fill('form[action$="/license/ack/review"]', {})); }],
  ["backups", () => go("/i/dcm-1/backups")],
  ["backup-review", async () => { await go("/i/dcm-1/backups"); await submit(fill('form[action$="/backup/review"]', {})); }],
  ["backup-result", async () => { await go("/i/dcm-1/backups"); await submit(fill('form[action$="/backup/review"]', {}));
      await submit(fill('form[action$="/backup/apply"]', { reason: "screenshot for the user guide", approve: true })); }],
  ["studies", () => go("/i/dcm-1/studies")],
  ["studies-search", async () => { await go("/i/dcm-1/studies"); await submit(fill('form[action$="/studies/search"]', { patient_id: "WTSYS-00*" })); }],
  ["study", async () => { await go("/i/dcm-1/studies"); await submit(fill('form[action$="/studies/search"]', { patient_id: "WTSYS-00*" }));
      await go(await firstHref('a[href*="/studies/"]')); }],
  ["series", async () => { await go("/i/dcm-1/studies"); await submit(fill('form[action$="/studies/search"]', { patient_id: "WTSYS-00*" }));
      await go(await firstHref('a[href*="/studies/"]')); await go(await firstHref('a[href*="/series/"]')); }],
  ["send-review", async () => { await go("/i/dcm-1/studies"); await submit(fill('form[action$="/studies/search"]', { patient_id: "WTSYS-00*" }));
      await go(await firstHref('a[href*="/studies/"]')); await submit(fill('form[action$="/send/review"]', { destination: "STORESCP" })); }],
  ["send-result", async () => { await go("/i/dcm-1/studies"); await submit(fill('form[action$="/studies/search"]', { patient_id: "WTSYS-00*" }));
      await go(await firstHref('a[href*="/studies/"]')); await submit(fill('form[action$="/send/review"]', { destination: "STORESCP" }));
      await submit(fill('form[action$="/send/apply"]', { reason: "screenshot for the user guide", approve: true })); }],
  ["dcm-audit", () => go("/i/dcm-1/audit?period=1h")],
  ["logs", () => go("/i/dcm-1/logs?period=1h")],
  ["wt-audit", () => go("/audit")],
  ["users", () => go("/users")],
  ["account", () => go("/account")],
];

let failed = 0, overflow = 0;
for (const scheme of ["light", "dark"]) {
  await cdp("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: scheme }] });
  await cdp("Network.clearBrowserCookies").catch(() => {});
  await go("/login");
  await shot("login", scheme);
  await submit(fill('form[action="/login"]', { username: USER, password: PASS }));
  for (const [name, nav] of steps) {
    try { await nav(); await shot(name, scheme); } catch (e) { failed++; console.log(`FAIL ${name}-${scheme}: ${e.message}`); }
  }
}
ws.close();
chrome.kill();
console.log(`done, ${failed} failed, ${overflow} wider than 1280 px`);
process.exit(failed || overflow ? 1 : 0);
