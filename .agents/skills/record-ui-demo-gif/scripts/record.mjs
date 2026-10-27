// Records the dashboard demo GIF. Storyboard, capture, and encode: see SKILL.md.
// Usage: node record.mjs [--change 2] [--base http://localhost:8080] [--out /tmp/promoter-ui-demo/recording]

import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { mkdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));

const args = Object.fromEntries(
  process.argv.slice(2).reduce((acc, arg, i, all) => {
    if (arg.startsWith('--')) acc.push([arg.slice(2), all[i + 1]]);
    return acc;
  }, []),
);
const CHANGE = args.change ?? '2';
const BASE = args.base ?? 'http://localhost:8080';
const OUT = args.out ?? '/tmp/promoter-ui-demo/recording';
const NAMESPACE = args.namespace ?? 'promoter-demo';
const STRATEGY = args.strategy ?? 'shop';
const changes = JSON.parse(readFileSync(path.join(here, 'changes.json'), 'utf8'));
const SUBJECT = args.subject ?? changes[Number(CHANGE)]?.subject;
if (!SUBJECT) throw new Error(`no subject for change ${CHANGE} in changes.json`);

// The Explorer matrix needs about 1600 CSS px, with gaps and padding, to fit the
// commit column and all four environment columns (350 + 4 x 286). 1800 / 1.1
// leaves no horizontal scroll; the zoom keeps text legible after the GIF is
// scaled down.
const VIEWPORT = { width: 1800, height: 1000 };
const ZOOM = 1.1;

const FRAMES = path.join(OUT, 'frames');
await rm(OUT, { recursive: true, force: true });
await mkdir(FRAMES, { recursive: true });

const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: VIEWPORT,
  deviceScaleFactor: 1,
  // Disables the pulsing "pushing to active" animations, so frames are only
  // produced when something meaningful changes.
  reducedMotion: 'reduce',
});

// Zoom the page and draw a fake cursor (headless video has none).
await context.addInitScript((zoom) => {
  document.addEventListener('DOMContentLoaded', () => {
    document.documentElement.style.zoom = String(zoom);
    const dot = document.createElement('div');
    dot.id = 'demo-cursor';
    Object.assign(dot.style, {
      position: 'fixed',
      left: '0px',
      top: '0px',
      width: '18px',
      height: '18px',
      marginLeft: '-4px',
      marginTop: '-4px',
      borderRadius: '50%',
      background: 'rgba(30, 41, 59, 0.55)',
      border: '2px solid #fff',
      boxShadow: '0 1px 4px rgba(0,0,0,0.4)',
      pointerEvents: 'none',
      zIndex: '2147483647',
      transition: 'transform 120ms ease-out',
      zoom: String(1 / zoom),
    });
    document.body.appendChild(dot);
    document.addEventListener(
      'mousemove',
      (e) => {
        // clientX/Y are viewport px; the dot's own zoom (1/zoom) cancels the
        // root zoom so its offsets are viewport px too.
        dot.style.left = `${e.clientX}px`;
        dot.style.top = `${e.clientY}px`;
      },
      true,
    );
    document.addEventListener('mousedown', () => (dot.style.transform = 'scale(0.7)'), true);
    document.addEventListener('mouseup', () => (dot.style.transform = 'scale(1)'), true);
  });
}, ZOOM);

const page = await context.newPage();
const t0 = Date.now();
const marks = [];
const now = () => (Date.now() - t0) / 1000;

// Lossless frame capture. Chrome only emits a frame when the page repaints and
// waits for the ack before sending the next one; the small delay caps the rate.
const frames = [];
const pendingWrites = [];
const cdp = await context.newCDPSession(page);
cdp.on('Page.screencastFrame', ({ data, sessionId }) => {
  const t = now();
  const file = path.join(FRAMES, `f${String(frames.length).padStart(5, '0')}.png`);
  frames.push({ file, t });
  pendingWrites.push(writeFile(file, Buffer.from(data, 'base64')));
  setTimeout(() => cdp.send('Page.screencastFrameAck', { sessionId }).catch(() => {}), 50);
});
await cdp.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 });
const mark = (name, maxDuration) => {
  marks.push({ name, start: now(), maxDuration });
  console.log(`[${now().toFixed(1)}s] ${name}`);
};
const sleep = (ms) => page.waitForTimeout(ms);

// Move the cursor to the centre of an element in steps so it glides on video.
async function glideTo(locator, { steps = 30 } = {}) {
  const box = await locator.boundingBox();
  if (!box) throw new Error('element not visible');
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps });
  return box;
}
async function glideClick(locator) {
  await glideTo(locator);
  await sleep(250);
  await page.mouse.down();
  await sleep(90);
  await page.mouse.up();
}

// The new change's row in the Explorer matrix, and its cell for one environment.
// Environment columns follow spec.environments order.
const changeRow = () => page.locator('.hp-row', { hasText: SUBJECT }).first();
const changeCell = (i) => changeRow().locator('.hp-row__env-slot').nth(i);
async function waitForLive(indexes, timeout) {
  await page.waitForFunction(
    ([idxs, text]) => {
      const row = [...document.querySelectorAll('.hp-row')].find((r) => r.textContent?.includes(text));
      const slots = row?.querySelectorAll('.hp-row__env-slot');
      return !!slots && idxs.every((i) => slots[i]?.querySelector('.cell__pill--live'));
    },
    [indexes, SUBJECT],
    { timeout },
  );
}

try {
  // ---- 1. strategies list ------------------------------------------------
  await page.mouse.move(VIEWPORT.width / 2, VIEWPORT.height / 2);
  await page.goto(`${BASE}/promotion-strategies`);
  await page.waitForSelector('.namespace-dropdown__control');
  mark('namespace', 5);
  await sleep(900);
  await glideClick(page.locator('.namespace-dropdown__control'));
  await sleep(400);
  await page.keyboard.type(NAMESPACE, { delay: 55 });
  await sleep(500);
  await page.keyboard.press('Enter');
  const tile = page.locator('.ps-tile', { hasText: STRATEGY }).first();
  await tile.waitFor({ timeout: 15000 });
  await sleep(1200);
  await glideClick(tile);
  await page.waitForSelector('.hp-row');

  // ---- 2. Explorer, open the promotion map -------------------------------
  mark('explorer', 4);
  await sleep(1200);
  await glideClick(page.locator('.hp-map__thumb'));
  await page.waitForSelector('.hp-map--expanded .hp-node');
  await sleep(1800);

  // ---- 3. a new change arrives and flows through the diamond -------------
  // development has no upstream, so its pull request merges at once: the new row
  // usually appears with development already live.
  mark('promote-development', 3);
  await page.mouse.move(VIEWPORT.width * 0.7, VIEWPORT.height - 80, { steps: 20 });
  const promote = spawn('bash', [path.join(here, 'demo.sh'), 'promote', CHANGE], { stdio: 'inherit' });
  promote.on('exit', (code) => code && console.error(`demo.sh promote exited with ${code}`));
  await waitForLive([0], 180000);
  // Both staging regions merge together once development's soak passes.
  mark('promote-staging', 3);
  await waitForLive([1, 2], 180000);
  // production waits for both soaks, and staging-eu's is twice as long.
  mark('promote-production', 3.5);
  await waitForLive([3], 180000);
  mark('settled', 2.5);
  await sleep(2500);

  // ---- 4. detail drawer for production -----------------------------------
  mark('drawer', 6);
  await glideClick(changeCell(3));
  await page.waitForSelector('.hp-drawer--open', { timeout: 15000 });
  await page.mouse.move(VIEWPORT.width * 0.45, VIEWPORT.height * 0.8, { steps: 25 });
  await sleep(4500);
  mark('end', 0);
} finally {
  await cdp.send('Page.stopScreencast').catch(() => {});
  await Promise.all(pendingWrites);
  await context.close();
  await browser.close();
}

if (frames.length < 2) throw new Error('no frames captured');

// ffmpeg concat list: each frame is shown until the next one arrived. Times are
// rebased so the first frame is t=0, and the marks are shifted to match.
const base = frames[0].t;
const list = frames.map((f, i) => {
  const next = frames[i + 1]?.t ?? marks[marks.length - 1].start;
  return `file 'frames/${path.basename(f.file)}'\nduration ${Math.max(0.001, next - f.t).toFixed(3)}`;
});
list.push(`file 'frames/${path.basename(frames[frames.length - 1].file)}'`);
await writeFile(path.join(OUT, 'frames.txt'), list.join('\n') + '\n');

for (let i = 0; i < marks.length - 1; i++) marks[i].end = marks[i + 1].start - base;
marks.pop();
for (const m of marks) m.start -= base;
await writeFile(
  path.join(OUT, 'marks.json'),
  JSON.stringify({ viewport: VIEWPORT, zoom: ZOOM, frames: frames.length, segments: marks }, null, 2),
);
console.log(`captured ${frames.length} frames; wrote ${path.join(OUT, 'frames.txt')} and marks.json`);
