// Screenshots of the LibreDVT web UI pages added or changed in v0.2
// (targets, test plans, run pages with TLS/association/DICOMweb evidence),
// in light and dark, and a check that no page is wider than its 1280 px
// window.
//
//   node scripts/e2e/dvt-screenshots.mjs DVT_URL OUT_DIR ADMIN_PASSWORD
//
// Runs against a LibreDVT that already holds targets and finished runs (the
// card DVT-0.2/5 demo stack). Drives a headless Chromium (/usr/sbin/chromium,
// or $CHROMIUM) over the DevTools protocol with Node's built-in WebSocket; no
// packages. Documentation only: the images are not verification records.
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, mkdtempSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

const [DVT, OUT, PASS] = process.argv.slice(2);
if (!PASS) { console.error("usage: dvt-screenshots.mjs DVT_URL OUT_DIR ADMIN_PASSWORD"); process.exit(2); }
mkdirSync(OUT, { recursive: true });
const PORT = 9334;
const profile = mkdtempSync(join(process.env.TMPDIR || tmpdir(), "dvtshot-"));
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

async function go(url) { const l = once("Page.loadEventFired"); await cdp("Page.navigate", { url: DVT + url }); await l; }
async function js(expr) {
  const r = await cdp("Runtime.evaluate", { expression: expr, returnByValue: true });
  if (r.exceptionDetails) throw new Error(expr + ": " + JSON.stringify(r.exceptionDetails));
  return r.result.value;
}
async function submit(expr) { const l = once("Page.loadEventFired"); await js(expr); await l; }
const q = (s) => JSON.stringify(s);
const fill = (form, values) => `(() => { const f = document.querySelector(${q(form)}); if (!f) throw new Error("no form " + ${q(form)});
  for (const [n, v] of Object.entries(${JSON.stringify(values)})) { const e = [...f.elements].filter(x => x.name === n).pop();
    if (!e) throw new Error("no field " + n); if (e.type === "checkbox") e.checked = !!v; else e.value = v; }
  f.submit(); })()`;

let failed = 0, overflow = 0;
// shot captures the page from the top of selector (or the page top), at
// most maxH pixels high.
async function shot(name, scheme, selector = "", maxH = 2400) {
  await sleep(150);
  const m = await cdp("Page.getLayoutMetrics");
  let y = 0;
  if (selector) {
    y = await js(`(() => { const e = document.querySelector(${q(selector)}); if (!e) throw new Error("no element " + ${q(selector)}); return Math.max(0, e.getBoundingClientRect().top + window.scrollY - 8); })()`);
  }
  const h = Math.min(Math.ceil(m.cssContentSize.height) - y, maxH);
  const r = await cdp("Page.captureScreenshot", { format: "webp", quality: 82, captureBeyondViewport: true, clip: { x: 0, y, width: 1280, height: h, scale: 1 } });
  const file = join(OUT, `${name}-${scheme}.webp`);
  writeFileSync(file, Buffer.from(r.data, "base64"));
  const wide = await js("document.documentElement.scrollWidth");
  if (wide > 1280) { overflow++; console.log(`OVERFLOW ${name}-${scheme}: page is ${wide}px wide`); }
  console.log(`${file}\t${await js("document.title")}\t${h}px`);
}
// runHref finds the newest run of a plan on the home page.
const runHref = (plan) => js(`(() => { const row = [...document.querySelectorAll("tr")].find(r => r.cells.length > 1 && r.cells[1].textContent.trim() === ${q(plan)});
  if (!row) throw new Error("no run of " + ${q(plan)}); return row.querySelector('a[href^="/runs/"]').getAttribute("href"); })()`);

const steps = [
  ["home", async () => go("/"), ""],
  ["targets", async () => go("/targets"), ""],
  ["targets-forms", async () => go("/targets"), "h2:nth-of-type(1)"],
  ["target-refused", async () => { await go("/targets");
    await submit(fill('form[action="/targets"]:has(input[value="dicomweb"])', { name: "outside-secrets", base_url: "https://orthanc-https:8042/dicom-web", class: "lab", auth_type: "bearer", token_file: "/opt/elsewhere/token" })); }, ""],
  ["plans", async () => go("/plans"), ""],
  ["run-smoke", async () => { await go("/"); await go(await runHref("LibreDCM smoke (DIMSE + TLS)")); }, ""],
  ["run-smoke-identity", async () => { await go("/"); await go(await runHref("LibreDCM smoke (DIMSE + TLS)")); }, "#endpoint-identity"],
  ["run-smoke-negotiate", async () => { await go("/"); await go(await runHref("LibreDCM smoke (DIMSE + TLS)")); }, "#evidence-negotiate"],
  ["run-dicom-tls-audit", async () => { await go("/"); await go(await runHref("Generic DICOM TLS audit")); }, ""],
  ["run-dicomweb", async () => { await go("/"); await go(await runHref("Generic DICOMweb audit")); }, ""],
  ["run-dicomweb-stow", async () => { await go("/"); await go(await runHref("Generic DICOMweb audit")); }, "#evidence-stow-samples"],
];

for (const scheme of ["light", "dark"]) {
  await cdp("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: scheme }] });
  await cdp("Network.clearBrowserCookies").catch(() => {});
  await go("/login");
  await submit(fill('form[action="/login"]', { password: PASS }));
  for (const [name, nav, sel] of steps) {
    try { await nav(); await shot(name, scheme, sel); } catch (e) { failed++; console.log(`FAIL ${name}-${scheme}: ${e.message}`); }
  }
}
ws.close();
chrome.kill();
console.log(`done, ${failed} failed, ${overflow} wider than 1280 px`);
process.exit(failed || overflow ? 1 : 0);
