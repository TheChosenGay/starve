'use strict';

// 宽阶段压测：同一批物体、同一批查询，对比
//   naive   —— 不用扫描器：每次查询把全部物体喂给窄阶段
//   scanner —— 用扫描器（默认数组实现）：先 AABB 剔除，只有候选进窄阶段
// 两个模式的命中目标必须完全一致（Go 侧有差分测试），差别只在窄阶段调用次数与耗时。

const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
let S = 62, zoom = 1, ox = 0, oy = 0;
let yaw = 0.0, pitch = 1.32;
const PITCH_MIN = 0.2, PITCH_MAX = Math.PI / 2 - 0.01;

let cam = { r: { X: 1, Y: 0, Z: 0 }, u: { X: 0, Y: 1, Z: 0 }, eye: { X: 0, Y: 0, Z: 1 } };
function updateCam() {
  const cp = Math.cos(pitch), sp = Math.sin(pitch), cy = Math.cos(yaw), sy = Math.sin(yaw);
  const eye = { X: sy * cp, Y: sp, Z: cy * cp };
  let r = { X: eye.Z, Y: 0, Z: -eye.X };
  const rl = Math.hypot(r.X, r.Z) || 1;
  r = { X: r.X / rl, Y: 0, Z: r.Z / rl };
  const u = {
    X: eye.Y * r.Z - eye.Z * r.Y,
    Y: eye.Z * r.X - eye.X * r.Z,
    Z: eye.X * r.Y - eye.Y * r.X,
  };
  cam = { r, u, eye };
}
function proj(x, y, z) {
  const k = S * zoom;
  return [ox + (cam.r.X * x + cam.r.Y * y + cam.r.Z * z) * k,
          oy - (cam.u.X * x + cam.u.Y * y + cam.u.Z * z) * k];
}
function resize() {
  const r = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(1, Math.round(r.width * dpr));
  canvas.height = Math.max(1, Math.round(r.height * dpr));
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.lineCap = 'round';
  ox = r.width * 0.5;
  oy = r.height * 0.58;
  S = Math.min(r.width, r.height) / 9;
  updateCam();
}

// fitView 让整个广场（±BOUND）在初始时完整入画：物体上万时看的是"面"而不是几个点。
function fitView() {
  const r = canvas.getBoundingClientRect();
  zoom = (Math.min(r.width, r.height) * 0.45) / (BOUND * S);
}

// ---- 状态 ----
const COUNTS = [200, 500, 1000, 2000, 5000, 10000, 20000, 40000, 80000];
const BOUND = 40;         // 广场半边长（米）
const RADIUS = 0.4;       // 物体半径
const HEIGHT = 1.8;
const QUERY_R = 1.6;      // 查询球半径
const FRAME_BUDGET = 16.7; // 20Hz tick 预算其实是 50ms；这里用 60fps 的 16.7ms 作参考线

const st = {
  countIdx: 4,
  queries: 32,
  dirty: 0.2,
  rebuild: false,
  paused: false,
  ready: false,
  wasmMissing: false,
  tick: 0,
  positions: [],
  radii: [],
  step: 0,       // 本帧索引维护耗时（ms）
  naive: null,   // 上一帧结果
  scanner: null,
  avg: { step: 0, naive: 0, scanner: 0, n: 0 },
};

function count() { return COUNTS[st.countIdx]; }

function setup() {
  if (typeof window.collideBPSetup !== 'function') { st.wasmMissing = true; return; }
  const out = JSON.parse(window.collideBPSetup(JSON.stringify({
    count: count(), seed: 1, bound: BOUND, margin: 0.25, radius: RADIUS, height: HEIGHT,
  })));
  st.wasmMissing = !out.ok;
  st.tick = 0;
  st.positions = [];
  st.radii = [];
  st.naive = st.scanner = null;
  st.avg = { step: 0, naive: 0, scanner: 0, n: 0 };
}

// ---- 每帧：推进 + 两种模式各跑一遍 ----
function frameWork(dt) {
  if (!st.ready || st.wasmMissing) return;
  st.tick++;

  let t0 = performance.now();
  const stepOut = JSON.parse(window.collideBPStep(JSON.stringify({
    dt, speed: 6, dirty: st.dirty, rebuild: st.rebuild,
  })));
  st.step = performance.now() - t0;
  st.positions = stepOut.positions || [];
  st.radii = stepOut.radii || [];

  const payload = (mode) => JSON.stringify({ mode, count: st.queries, radius: QUERY_R });

  // 先算暴力，再算扫描器：两者用同一份场景与同一批查询（查询位置由 Go 侧按 tick 生成）
  t0 = performance.now();
  st.naive = JSON.parse(window.collideBPQuery(payload('naive')));
  const tNaive = performance.now() - t0;

  t0 = performance.now();
  st.scanner = JSON.parse(window.collideBPQuery(payload('scanner')));
  const tScanner = performance.now() - t0;

  // 指数滑动平均，读数不至于每帧乱跳
  const a = st.avg;
  a.step = a.n === 0 ? st.step : a.step * 0.9 + st.step * 0.1;
  a.naive = a.n === 0 ? tNaive : a.naive * 0.9 + tNaive * 0.1;
  a.scanner = a.n === 0 ? tScanner : a.scanner * 0.9 + tScanner * 0.1;
  a.n++;
}

// ---- 渲染 ----
function drawGrid() {
  const N = 8;
  for (let i = -N; i <= N; i++) {
    const t = (i / N) * BOUND;
    line(t, 0, -BOUND, t, 0, BOUND, '#232838', 1);
    line(-BOUND, 0, t, BOUND, 0, t, '#232838', 1);
  }
}
function line(x1, y1, z1, x2, y2, z2, color, w) {
  const p = proj(x1, y1, z1), q = proj(x2, y2, z2);
  ctx.strokeStyle = color; ctx.lineWidth = w || 1;
  ctx.beginPath(); ctx.moveTo(p[0], p[1]); ctx.lineTo(q[0], q[1]); ctx.stroke();
}
function circle(x, y, z, r, stroke, fill, w) {
  const s = proj(x, y, z);
  ctx.beginPath(); ctx.arc(s[0], s[1], r * S * zoom, 0, Math.PI * 2);
  if (fill) { ctx.fillStyle = fill; ctx.fill(); }
  if (stroke) { ctx.strokeStyle = stroke; ctx.lineWidth = w || 1.5; ctx.stroke(); }
}

function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawGrid();
  if (!st.positions.length) return;

  // 物体：候选上色（候选 = 首个查询的 AABB 剔除结果）。数量大时抽稀绘制。
  const flags = (st.scanner && st.scanner.flags) ? st.scanner.flags : '';
  const stride = st.positions.length > 4000 ? 4 : 1;
  const size = S * zoom * RADIUS * 0.7;
  for (let i = 0; i < st.positions.length; i += stride) {
    const p = st.positions[i];
    const s = proj(p.x, p.y, p.z);
    ctx.fillStyle = (flags && flags[i] === '1') ? 'rgba(110,168,254,0.95)' : 'rgba(90,99,120,0.75)';
    ctx.fillRect(s[0] - size / 2, s[1] - size / 2, size, size);
  }

  // 首个查询球：候选判定就是拿它去剔除的
  if (st.scanner && st.scanner.qr > 0) {
    circle(st.scanner.qx, HEIGHT * 0.5, st.scanner.qz, st.scanner.qr, '#ffd866', 'rgba(255,216,102,0.14)', 2.5);
  }
  // 命中点
  const pts = (st.scanner && st.scanner.points) ? st.scanner.points : [];
  for (const p of pts) {
    const s = proj(p.x, p.y, p.z);
    ctx.fillStyle = '#ff5c52';
    ctx.beginPath(); ctx.arc(s[0], s[1], 3, 0, Math.PI * 2); ctx.fill();
  }
}

function bar(ms) {
  const pct = Math.min(100, (ms / 40) * 100);
  const cls = ms <= FRAME_BUDGET ? 'bar ok' : 'bar hot';
  return `<div class="${cls}"><i style="width:${pct.toFixed(1)}%"></i></div>`;
}

function updateHUD() {
  const a = st.avg;
  const n = st.naive, s = st.scanner;
  if (!n || !s) {
    document.getElementById('hud').innerHTML =
      (st.wasmMissing ? '<div class="bad"><b>WASM 未就绪</b>：请强制刷新页面</div>'
        : '<div>正在建立场景…</div>');
    return;
  }
  const speedup = s.narrow > 0 ? (n.narrow / Math.max(1, s.narrow)) : 0;
  const tSpeedup = a.scanner > 0 ? (a.naive / a.scanner) : 0;
  const idxMode = st.rebuild ? '<span class="warn">每帧全量重建</span>' : '增量更新';
  document.getElementById('hud').innerHTML = `
    <table>
      <tr>
        <td>物体 <b>${count()}</b> · 每帧查询 <b>${st.queries}</b> · 移动 ${Math.round(st.dirty * 100)}%</td>
        <td class="num">窄阶段调用</td>
        <td class="num">耗时</td>
        <td class="num">相对</td>
      </tr>
      <tr>
        <td>不用扫描器（每次查询扫全部）</td>
        <td class="num ${n.narrow > 100000 ? 'bad' : ''}">${n.narrow.toLocaleString()}</td>
        <td class="num ${a.naive > FRAME_BUDGET ? 'bad' : ''}">${a.naive.toFixed(2)} ms</td>
        <td class="num">1.00×</td>
      </tr>
      <tr>
        <td>用扫描器（先 AABB 剔除）</td>
        <td class="num"><b>${s.narrow.toLocaleString()}</b></td>
        <td class="num"><b>${a.scanner.toFixed(2)} ms</b></td>
        <td class="num"><b>${tSpeedup.toFixed(1)}×</b></td>
      </tr>
      <tr>
        <td>候选 / 命中</td>
        <td class="num">${s.candidates.toLocaleString()} / ${s.hits.toLocaleString()}</td>
        <td class="num">窄阶段少 <b>${speedup.toFixed(0)}×</b></td>
        <td class="num"></td>
      </tr>
      <tr><td colspan="4">${bar(a.naive)}</td></tr>
      <tr><td colspan="4">${bar(a.scanner)}</td></tr>
      <tr><td colspan="4">索引维护（${idxMode}）：${a.step.toFixed(2)} ms</td></tr>
    </table>
    <small>蓝点 = 首个查询的候选（其余被宽阶段剔除）· 黄圈 = 查询球 · 红线 = 命中点 ·
    两者命中目标完全一致，差别只在窄阶段调用次数 · 拖空白处旋转 · 滚轮缩放<br>
    注意：数组扫描器的"重建"只是重写一个数组，所以很便宜；换成 BVH 后全量重建才成为主要成本，
    那时"fat AABB 内不动索引"的增量语义才真正值钱。</small>`;
}

// ---- 交互 ----
let dragging = false, lastPt = [0, 0];
canvas.addEventListener('pointerdown', (e) => {
  dragging = true; lastPt = [e.clientX, e.clientY];
  canvas.setPointerCapture(e.pointerId);
});
canvas.addEventListener('pointermove', (e) => {
  if (!dragging) return;
  yaw -= (e.clientX - lastPt[0]) * 0.01;
  pitch = Math.max(PITCH_MIN, Math.min(PITCH_MAX, pitch + (e.clientY - lastPt[1]) * 0.01));
  lastPt = [e.clientX, e.clientY];
  updateCam();
});
canvas.addEventListener('pointerup', () => { dragging = false; });
canvas.addEventListener('wheel', (e) => {
  e.preventDefault();
  zoom = Math.max(0.2, Math.min(6, zoom * Math.exp(-e.deltaY * 0.0015)));
}, { passive: false });

const bind = (id, fn) => {
  const el = document.getElementById(id);
  el.addEventListener('input', () => fn(el));
  fn(el);
};
bind('n', (el) => {
  st.countIdx = Number(el.value);
  document.getElementById('nv').textContent = count();
  setup();
});
bind('q', (el) => {
  st.queries = Number(el.value);
  document.getElementById('qv').textContent = st.queries;
});
bind('d', (el) => {
  st.dirty = Number(el.value);
  document.getElementById('dv').textContent = Math.round(st.dirty * 100) + '%';
});
document.getElementById('rebuild').addEventListener('click', (e) => {
  st.rebuild = !st.rebuild;
  e.target.textContent = st.rebuild ? '索引：每帧全量重建' : '索引：增量更新';
  e.target.classList.toggle('on', st.rebuild);
});
document.getElementById('pause').addEventListener('click', (e) => {
  st.paused = !st.paused;
  e.target.textContent = st.paused ? '继续' : '暂停';
});
document.getElementById('reset').addEventListener('click', setup);
window.addEventListener('resize', resize);

let last = performance.now();
function frame(now) {
  const dt = Math.min(0.05, (now - last) / 1000);
  last = now;
  if (!st.paused) frameWork(dt);
  render();
  updateHUD();
  requestAnimationFrame(frame);
}

(async function () {
  const go = new Go();
  const buf = await (await fetch('collide.wasm?v=8')).arrayBuffer();
  const mod = await WebAssembly.instantiate(buf, go.importObject);
  go.run(mod.instance);
  resize();
  fitView();
  setup();
  st.ready = true;
  requestAnimationFrame(frame);
})();
