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

function strokeQuad2D(x0, z0, x1, z1, color, w, dash, y) {
  const yy = y || 0.02;
  const pts = [
    proj(x0, yy, z0), proj(x1, yy, z0), proj(x1, yy, z1), proj(x0, yy, z1),
  ];
  ctx.save();
  if (dash) ctx.setLineDash(dash);
  ctx.strokeStyle = color; ctx.lineWidth = w || 1;
  ctx.beginPath();
  ctx.moveTo(pts[0][0], pts[0][1]);
  for (const p of pts.slice(1)) ctx.lineTo(p[0], p[1]);
  ctx.closePath(); ctx.stroke();
  ctx.restore();
}

function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawGrid();
  const n = st.positions.length;
  if (!n) return;

  const sc = st.scanner;
  const flags = (sc && sc.flags) ? sc.flags : '';
  const hits = (sc && sc.hitFlags) ? sc.hitFlags : '';
  const qx = (sc && sc.queryX) || [];
  const qz = (sc && sc.queryZ) || [];
  const qr = (sc && sc.qr) || 0;

  // 每个黄圈 = 一次查询（半径 QUERY_R 的球）；首个查询画粗一点
  for (let i = qx.length - 1; i >= 0; i--) {
    circle(qx[i], HEIGHT * 0.5, qz[i], qr,
      i === 0 ? 'rgba(255,216,102,0.95)' : 'rgba(255,216,102,0.30)',
      i === 0 ? 'rgba(255,216,102,0.06)' : null, i === 0 ? 2 : 1);
  }
  // 首个查询的 AABB：宽阶段真正拿来剔除的盒子（虚线方框）
  if (qr > 0) {
    strokeQuad2D(qx[0] - qr, qz[0] - qr, qx[0] + qr, qz[0] + qr, 'rgba(255,216,102,0.85)', 1.5, [6, 5]);
  }

  // 物体：灰 = 被宽阶段剔除；蓝 = 进了窄阶段；红 = 真的命中（都针对首个查询）
  const stride = n > 20000 ? 4 : (n > 8000 ? 2 : 1);
  const size = Math.max(2.5, S * zoom * RADIUS * 1.8);
  for (let i = 0; i < n; i += stride) {
    const p = st.positions[i];
    const s = proj(p.x, p.y, p.z);
    let color = 'rgba(96,104,124,0.55)';
    if (flags && flags[i] === '1') color = 'rgba(110,168,254,0.95)';
    if (hits && hits[i] === '1') color = 'rgba(255,92,82,1)';
    ctx.fillStyle = color;
    ctx.fillRect(s[0] - size / 2, s[1] - size / 2, size, size);
  }
  // 候选的 fat AABB 轮廓：就是"和查询盒重叠的那些盒子"
  if (flags) {
    const pad = RADIUS + 0.25;
    ctx.save();
    ctx.strokeStyle = 'rgba(110,168,254,0.5)';
    ctx.lineWidth = 1;
    for (let i = 0; i < n; i++) {
      if (flags[i] !== '1') continue;
      strokeQuad2D(st.positions[i].x - pad, st.positions[i].z - pad,
                   st.positions[i].x + pad, st.positions[i].z + pad,
                   'rgba(110,168,254,0.5)', 1, null, 0.03);
    }
    ctx.restore();
  }
  // 命中点（只画首个查询的，避免一片红点看不清）
  if (hits) {
    for (let i = 0; i < n; i++) {
      if (hits[i] !== '1') continue;
      const p = st.positions[i];
      const s = proj(p.x, RADIUS, p.z);
      ctx.fillStyle = '#ff5c52';
      ctx.beginPath(); ctx.arc(s[0], s[1], 4, 0, Math.PI * 2); ctx.fill();
      ctx.strokeStyle = 'rgba(255,180,170,0.9)'; ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.arc(s[0], s[1], 7, 0, Math.PI * 2); ctx.stroke();
    }
  }
}

function bar(ms) {
  const pct = Math.min(100, (ms / 40) * 100);
  const cls = ms <= FRAME_BUDGET ? 'bar ok' : 'bar hot';
  return `<div class="${cls}"><i style="width:${pct.toFixed(1)}%"></i></div>`;
}

function updateHUD() {
  const a = st.avg, n = st.naive, s = st.scanner;
  if (!n || !s) {
    document.getElementById('hud').innerHTML =
      (st.wasmMissing ? '<div class="bad"><b>WASM 未就绪</b>：请强制刷新页面</div>'
        : '<div>正在建立场景…</div>');
    return;
  }
  const ratio = s.narrow > 0 ? n.narrow / s.narrow : 0;
  const speedup = a.scanner > 0 ? a.naive / a.scanner : 0;
  const idxMode = st.rebuild ? '<span class="warn">每帧全量重建</span>' : '增量更新';
  document.getElementById('hud').innerHTML = `
    <div class="step">每帧：① 推进模拟（<b>${Math.round(st.dirty * 100)}%</b> 的物体移动并同步索引）
      ② 用<b>同一批 ${st.queries} 个查询</b>各跑一遍两种模式</div>
    <div class="step">一次查询 = 半径 ${QUERY_R} 的球（黄圈）：「谁和我重叠？」
      两种模式命中的目标完全一致，只比<b>形状测试被调用多少次</b></div>
    <table>
      <tr><td></td><td class="num">每次查询</td><td class="num">合计</td><td class="num">耗时</td><td class="num">相对</td></tr>
      <tr>
        <td>不用扫描器<br><small>N 个物体全喂形状测试</small></td>
        <td class="num">${n.pairs.toFixed(0)}</td>
        <td class="num ${n.narrow > 100000 ? 'bad' : ''}">${n.narrow.toLocaleString()}</td>
        <td class="num ${a.naive > FRAME_BUDGET ? 'bad' : ''}">${a.naive.toFixed(2)} ms</td>
        <td class="num">1.00×</td>
      </tr>
      <tr>
        <td>用扫描器<br><small>先 AABB 剔除，只有候选进形状测试</small></td>
        <td class="num"><b>${s.pairs.toFixed(1)}</b></td>
        <td class="num"><b>${s.narrow.toLocaleString()}</b></td>
        <td class="num"><b>${a.scanner.toFixed(2)} ms</b></td>
        <td class="num"><b>${speedup.toFixed(1)}×</b></td>
      </tr>
      <tr><td colspan="5">${bar(a.naive)}</td></tr>
      <tr><td colspan="5">${bar(a.scanner)}</td></tr>
    </table>
    <div class="step">形状测试少 <b>${ratio.toFixed(0)}×</b>　候选 ${s.candidates.toLocaleString()} 个　命中 ${s.hits.toLocaleString()} 个　
      索引维护（${idxMode}）${a.step.toFixed(2)} ms</div>
    <small>灰点 = 被宽阶段剔除 · 蓝点 = 进了窄阶段（它们外面那个蓝框就是 fat AABB）·
    红点 = 真的命中 · 黄色粗圈 = 首个查询、虚线方框 = 它的 AABB ·
    数组扫描器的"重建"只是重写数组，所以便宜；换成 BVH 后才会反转</small>`;
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
