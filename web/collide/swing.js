'use strict';

// 挥砍：一次挥击 = 一个扇形查询体积（From → To）。
// 业务侧只提供数字（张角、射程、内圈）与句柄；扇形体积、宽阶段剔除、窄阶段判定
// 全在 Go 侧完成，返回的命中部位（Point）用来在目标身上画局部红色。

const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
let S = 62, zoom = 1, ox = 0, oy = 0;
let yaw = 0.0, pitch = 1.25;
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
  oy = r.height * 0.62;
  S = Math.min(r.width, r.height) / 10;
  updateCam();
}

// ---- 场景 ----
const PLAYER = { x: 0, z: 0, radius: 0.4, height: 1.8 };
const SWING_DURATION = 0.55;
const TARGETS = [
  { ang: -50, d: 2.4 },   // 弧内、中距 → 命中
  { ang: -30, d: 3.0 },   // 弧内 → 命中
  { ang: 0, d: 2.0 },     // 正前 → 命中
  { ang: 25, d: 2.8 },    // 弧内 → 命中
  { ang: 45, d: 3.2 },    // 弧内靠边 → 命中（刚好在 60° 内）
  { ang: -20, d: 1.2 },   // 弧内但很近 → 命中（> 内圈）
  { ang: 70, d: 2.5 },    // 弧外
  { ang: 135, d: 2.2 },   // 背后
  { ang: 5, d: 0.35 },    // 贴脸 → 落进内圈，不命中
  { ang: 12, d: 5.2 },    // 超出射程
].map((t) => {
  const a = (t.ang * Math.PI) / 180;
  return { x: Math.cos(a) * t.d, z: Math.sin(a) * t.d, radius: 0.35, height: 1.7 };
});

const st = {
  half: Math.PI / 3,
  reach: 3.6,
  inner: 0.9,
  showBlade: true,
  paused: false,
  ready: false,
  wasmMissing: false,
  t: 0,            // 0..1 挥砍进度
  angle: -Math.PI / 3,
  hold: 0,
  marks: [],       // 命中标记 {index, point, life}
  last: null,      // 上一次 sectorStep 的结果
  totalHits: 0,
  runs: 0,
};

function setup() {
  if (typeof window.collideSectorSetup !== 'function') { st.wasmMissing = true; return; }
  const out = JSON.parse(window.collideSectorSetup(JSON.stringify({
    player: PLAYER, targets: TARGETS,
  })));
  st.wasmMissing = !out.ok;
}

function reset() {
  st.t = 0;
  st.angle = -st.half;
  st.hold = 0;
  st.marks = [];
  st.last = null;
  st.totalHits = 0;
}

function swingStep() {
  if (st.wasmMissing) return;
  const out = JSON.parse(window.collideSectorStep(JSON.stringify({
    from: -st.half, to: st.angle, r0: st.inner, r1: st.reach,
  })));
  st.last = out;
  for (const h of out.hits || []) {
    // 已被标记过的目标不再重复加标记（一次挥砍每个目标只留一处红）
    if (st.marks.some((m) => m.index === h.index)) continue;
    st.marks.push({ index: h.index, point: h.point, life: 1 });
    st.totalHits++;
  }
}

function step(dt) {
  if (st.paused || !st.ready) return;
  if (st.hold > 0) {
    st.hold -= dt;
    if (st.hold <= 0) { reset(); st.runs++; }
    return;
  }
  st.t += dt / SWING_DURATION;
  if (st.t >= 1) {
    st.t = 1;
    st.angle = st.half;
    swingStep();
    st.hold = 1.2; // 停一会儿再循环
    return;
  }
  st.angle = -st.half + 2*st.half*st.t;
  swingStep();
}

// ---- 渲染 ----
function line3(a, b, color, w, dash) {
  ctx.save();
  if (dash) ctx.setLineDash(dash);
  ctx.strokeStyle = color; ctx.lineWidth = w || 1;
  const p = proj(a.x, a.y, a.z), q = proj(b.x, b.y, b.z);
  ctx.beginPath(); ctx.moveTo(p[0], p[1]); ctx.lineTo(q[0], q[1]);
  ctx.stroke(); ctx.restore();
}
function dot3(p, color, r) {
  const s = proj(p.x, p.y, p.z);
  ctx.fillStyle = color;
  ctx.beginPath(); ctx.arc(s[0], s[1], r || 4, 0, Math.PI * 2); ctx.fill();
}
function capsule(c, color, width) {
  const a = { x: c.x, y: 0, z: c.z };
  const b = { x: c.x, y: c.height, z: c.z };
  line3(a, b, color, width || 2 * c.radius * S * zoom);
  for (const p of [a, b]) {
    const s = proj(p.x, p.y, p.z);
    ctx.beginPath(); ctx.arc(s[0], s[1], c.radius * S * zoom, 0, Math.PI * 2);
    ctx.strokeStyle = color; ctx.lineWidth = 1.5; ctx.stroke();
  }
}
function drawGrid() {
  const N = 5;
  for (let i = -N; i <= N; i++) {
    line3({ x: i, y: 0, z: -N }, { x: i, y: 0, z: N }, '#232838', 1);
    line3({ x: -N, y: 0, z: i }, { x: N, y: 0, z: i }, '#232838', 1);
  }
}

// 扇形：内圈到外圈的环带楔形，按 3° 采样成多边形
function drawSector(from, to, r0, r1, stroke, fill) {
  const steps = Math.max(2, Math.ceil(Math.abs(to - from) / 0.05));
  const y = 0.06;
  const pts = [];
  for (let i = 0; i <= steps; i++) {
    const a = from + ((to - from) * i) / steps;
    pts.push(proj(Math.cos(a) * r1, y, Math.sin(a) * r1));
  }
  for (let i = steps; i >= 0; i--) {
    const a = from + ((to - from) * i) / steps;
    pts.push(proj(Math.cos(a) * r0, y, Math.sin(a) * r0));
  }
  ctx.beginPath();
  ctx.moveTo(pts[0][0], pts[0][1]);
  for (const p of pts.slice(1)) ctx.lineTo(p[0], p[1]);
  ctx.closePath();
  if (fill) { ctx.fillStyle = fill; ctx.fill(); }
  if (stroke) { ctx.strokeStyle = stroke; ctx.lineWidth = 1.5; ctx.stroke(); }
}

function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawGrid();

  // 判定区域：从挥砍起点到当前刀刃角度的扇形
  if (st.angle > -st.half + 1e-4) {
    drawSector(-st.half, st.angle, st.inner, st.reach, 'rgba(255,216,102,0.55)', 'rgba(255,216,102,0.10)');
  }
  // 整段挥砍范围（参考）
  drawSector(-st.half, st.half, st.reach - 0.02, st.reach, 'rgba(255,216,102,0.25)', null);

  // 目标
  for (const t of TARGETS) capsule(t, '#ffa657');

  // 刀身（当前角度）
  if (st.showBlade) {
    const tip = { x: Math.cos(st.angle) * st.reach, y: 1.1, z: Math.sin(st.angle) * st.reach };
    line3({ x: PLAYER.x, y: 1.1, z: PLAYER.z }, tip, '#e6e8ee', 3);
    dot3(tip, '#ffd866', 4);
  }

  // 玩家
  capsule(PLAYER, '#7fd18b');

  // 命中标记：目标身上的一块局部红
  for (const m of st.marks) {
    const s = proj(m.point.x, m.point.y, m.point.z);
    const rad = Math.max(5, 0.42 * S * zoom) * (0.6 + 0.4 * m.life);
    const g = ctx.createRadialGradient(s[0], s[1], 0, s[0], s[1], rad);
    g.addColorStop(0, `rgba(255,60,50,${0.95 * m.life})`);
    g.addColorStop(0.6, `rgba(214,32,32,${0.8 * m.life})`);
    g.addColorStop(1, 'rgba(160,20,20,0)');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(s[0], s[1], rad, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = `rgba(255,150,140,${0.85 * m.life})`;
    ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.arc(s[0], s[1], rad * 0.55, 0, Math.PI * 2); ctx.stroke();
  }
}

function updateHUD() {
  const warn = st.wasmMissing ? '<div class="hit"><b>WASM 未就绪</b>：请强制刷新</div>' : '';
  const out = st.last;
  const hits = (out && out.hits) || [];
  const list = hits.length
    ? hits.map((h) => `<span class="row">#${h.index} (${h.point.x.toFixed(2)}, ${h.point.y.toFixed(2)}, ${h.point.z.toFixed(2)})</span>`).join('　')
    : '<span style="color:#8b93a7">无</span>';
  document.getElementById('hud').innerHTML = warn +
    `<div>挥砍进度 ${(st.t * 100).toFixed(0)}%　刀刃角 ${((st.angle * 180) / Math.PI).toFixed(0)}°</div>` +
    `<div>扇形：±${((st.half * 180) / Math.PI).toFixed(0)}°　射程 ${st.reach.toFixed(1)}　内圈 ${st.inner.toFixed(2)}</div>` +
    `<div>候选 <b>${out ? out.candidates : 0}</b>　本次命中 <span class="hit">${hits.length}</span>　累计标记 <b>${st.totalHits}</b></div>` +
    `<div style="margin-top:4px">命中部位：${list}</div>` +
    '<small>红块 = 引擎返回的命中部位（目标表面朝向扇心的点）· 半透明黄 = 本次挥砍已覆盖的扇形 · ' +
    '拖空白处旋转 · 滚轮缩放</small>';
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
  zoom = Math.max(0.3, Math.min(5, zoom * Math.exp(-e.deltaY * 0.0015)));
}, { passive: false });

document.getElementById('a').addEventListener('input', (e) => {
  st.half = (Number(e.target.value) * Math.PI) / 360;
  document.getElementById('av').textContent = '±' + Number(e.target.value) / 2 + '°';
  reset();
});
document.getElementById('av').textContent = '±60°';
document.getElementById('r1').addEventListener('input', (e) => {
  st.reach = Number(e.target.value);
  document.getElementById('r1v').textContent = st.reach.toFixed(1);
});
document.getElementById('r0').addEventListener('input', (e) => {
  st.inner = Number(e.target.value);
  document.getElementById('r0v').textContent = st.inner.toFixed(2);
});
document.getElementById('blade').addEventListener('click', (e) => {
  st.showBlade = !st.showBlade;
  e.target.textContent = st.showBlade ? '刀身：显示' : '刀身：隐藏';
});
document.getElementById('pause').addEventListener('click', (e) => {
  st.paused = !st.paused;
  e.target.textContent = st.paused ? '继续' : '暂停';
});
document.getElementById('replay').addEventListener('click', reset);
window.addEventListener('resize', resize);

let last = performance.now();
function frame(now) {
  const dt = Math.min(0.05, (now - last) / 1000);
  last = now;
  step(dt);
  for (const m of st.marks) m.life = Math.max(0.45, m.life - dt * 0.25);
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
  setup();
  reset();
  st.ready = true;
  requestAnimationFrame(frame);
})();
