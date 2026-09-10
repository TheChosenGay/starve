'use strict';

// 竖劈竞技场：所有角色都在走，玩家手上有一把刀，点击挥砍。
//
// 挥砍是**竖直**的：弧线在「朝向 × 竖直」这个平面里，
// 引擎按玩家当前朝向算旋转轴，调用方只给角度区间 / 射程 / 厚度这些数字。

const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
let S = 62, zoom = 1, ox = 0, oy = 0;
let yaw = 2.35, pitch = 0.42;
const PITCH_MIN = 0.06, PITCH_MAX = Math.PI / 2 - 0.01;

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
  oy = r.height * 0.66;
  S = Math.min(r.width, r.height) / 9;
  updateCam();
}

// ---- 场景参数 ----
const ARENA = 7.5;          // 场地半边长
const CHEST_Y = 1.15;       // 挥砍绕胸口转
const SWING_DURATION = 0.45;
const PLAYER = { x: -4.5, z: -3.0, radius: 0.4, height: 1.8, dir: 0 };
const TARGETS = [
  { x: 3.2, z: -1.0, dir: 0.4, speed: 1.0 },
  { x: -1.0, z: 4.0, dir: 2.2, speed: 1.3 },
  { x: 5.0, z: 3.4, dir: 3.4, speed: 0.8 },
  { x: -5.5, z: 2.6, dir: 1.1, speed: 1.5 },
  { x: 0.5, z: 0.5, dir: 5.0, speed: 1.2 },
  { x: -2.5, z: -4.5, dir: 0.9, speed: 1.1 },
].map((t) => ({ ...t, radius: 0.35, height: 1.7 }));

const marks = [];   // 命中标记：相对目标当前位置的局部偏移 + 淡出

const st = {
  reach: 2.8,
  inner: 0.9,
  thickness: 0.7,
  half: (105 * Math.PI) / 360, // 半张角（弧度）：滑杆给的是总张角
  chase: true,
  paused: false,
  ready: false,
  wasmMissing: false,
  // 来自引擎的实时状态
  px: PLAYER.x,
  pz: PLAYER.z,
  pyaw: 0,
  tx: [],
  tz: [],
  // 挥砍状态机
  swinging: false,
  t: 0,
  angle: (105 * Math.PI) / 360,
  hold: 0,
  last: null,
  hitsThisSwing: 0,
  totalHits: 0,
  swings: 0,
  hitAll: 0,
  // 自动挥砍：演示 / 截图用（也可以用 &auto=1 打开）
  auto: new URLSearchParams(location.search).get('auto') === '1',
};

function setup() {
  if (typeof window.collideArenaSetup !== 'function') { st.wasmMissing = true; return; }
  const out = JSON.parse(window.collideArenaSetup(JSON.stringify({
    player: PLAYER, targets: TARGETS, arena: ARENA,
  })));
  st.wasmMissing = !out.ok;
  marks.length = 0;
  st.swinging = false;
  st.totalHits = 0;
  st.swings = 0;
  st.hitAll = 0;
}

function startSwing() {
  if (!st.ready || st.wasmMissing || st.swinging || st.paused) return;
  st.swinging = true;
  st.t = 0;
  st.hitsThisSwing = 0;
  st.swings++;
}

// ---- 每帧 ----
function stepSim(dt) {
  if (!st.ready || st.wasmMissing || st.paused) return;
  const out = JSON.parse(window.collideArenaStep(JSON.stringify({
    dt, chase: st.chase, speed: 1.1, reach: 1.4,
  })));
  if (out.error) return;
  st.px = out.playerX;
  st.pz = out.playerZ;
  st.pyaw = out.yaw;
  st.tx = out.targetX || [];
  st.tz = out.targetZ || [];
}

function stepSwing(dt) {
  if (st.paused) return;
  if (!st.swinging) {
    // 收刀后的短暂停顿；开着"自动"就在这里接下一次挥砍
    if (st.hold > 0) st.hold -= dt;
    if (st.auto && st.hold <= 0) startSwing();
    return;
  }
  st.t += dt / SWING_DURATION;
  if (st.t >= 1) st.t = 1;
  st.angle = st.half + (-st.half - st.half) * st.t; // 从 +half 扫到 -half

  const out = JSON.parse(window.collideArenaSwing(JSON.stringify({
    from: st.half,
    to: st.angle,
    r0: st.inner,
    r1: st.reach,
    thickness: st.thickness,
    chestY: CHEST_Y,
  })));
  if (out.error) return;
  st.last = out;
  for (const h of out.hits || []) {
    // 已经标记过的目标不重复加（一次挥砍每个目标只留一处红）
    if (marks.some((m) => m.index === h.index && m.swing === st.swings)) continue;
    marks.push({
      index: h.index,
      swing: st.swings,
      dx: h.point.x - st.tx[h.index],
      dy: h.point.y,
      dz: h.point.z - st.tz[h.index],
      life: 1,
    });
    st.hitsThisSwing++;
    st.totalHits++;
    if (marks.length > 24) marks.shift();
    if (st.hitsThisSwing === 1) st.hitAll++;
  }
  if (st.t >= 1) {
    st.swinging = false;
    st.hold = 0.15;
  }
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
function capsuleAt(x, z, radius, height, color) {
  const a = { x, y: 0, z }, b = { x, y: height, z };
  line3(a, b, color, 2 * radius * S * zoom);
  for (const p of [a, b]) {
    const s = proj(p.x, p.y, p.z);
    ctx.beginPath(); ctx.arc(s[0], s[1], radius * S * zoom, 0, Math.PI * 2);
    ctx.strokeStyle = color; ctx.lineWidth = 1.5; ctx.stroke();
  }
}
function drawArena() {
  const N = 5;
  for (let i = -N; i <= N; i++) {
    const t = (i / N) * ARENA;
    line3({ x: t, y: 0, z: -ARENA }, { x: t, y: 0, z: ARENA }, '#232838', 1);
    line3({ x: -ARENA, y: 0, z: t }, { x: ARENA, y: 0, z: t }, '#232838', 1);
  }
}

// 竖直扇形：在「朝向 × 竖直」平面里画一块环带楔形（在 ±厚度 各画一层）
function drawVerticalSector(from, to, r0, r1, thick, stroke, fill) {
  const steps = Math.max(2, Math.ceil(Math.abs(to - from) / 0.06));
  const f = { x: Math.cos(st.pyaw), z: Math.sin(st.pyaw) };
  const up = { x: 0, y: 1, z: 0 };
  const lat = { x: f.z, z: -f.x }; // 旋转轴方向（左右）
  const pt = (ang, rad, off) => ({
    x: st.px + f.x * Math.cos(ang) * rad + lat.x * off,
    y: CHEST_Y + Math.sin(ang) * rad,
    z: st.pz + f.z * Math.cos(ang) * rad + lat.z * off,
  });
  for (const off of [-thick, 0, thick]) {
    const pts = [];
    for (let i = 0; i <= steps; i++) {
      const a = from + ((to - from) * i) / steps;
      pts.push(proj(pt(a, r1, off).x, pt(a, r1, off).y, pt(a, r1, off).z));
    }
    for (let i = steps; i >= 0; i--) {
      const a = from + ((to - from) * i) / steps;
      pts.push(proj(pt(a, r0, off).x, pt(a, r0, off).y, pt(a, r0, off).z));
    }
    ctx.beginPath();
    ctx.moveTo(pts[0][0], pts[0][1]);
    for (const p of pts.slice(1)) ctx.lineTo(p[0], p[1]);
    ctx.closePath();
    if (fill) { ctx.fillStyle = off === 0 ? fill : 'rgba(255,216,102,0.05)'; ctx.fill(); }
    if (stroke) { ctx.strokeStyle = stroke; ctx.lineWidth = 1; ctx.stroke(); }
  }
}

function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawArena();

  // 刀路（整段可挥范围，虚线）
  const f = { x: Math.cos(st.pyaw), z: Math.sin(st.pyaw) };
  drawVerticalSector(st.half, -st.half, st.reach - 0.03, st.reach, 0, 'rgba(255,216,102,0.22)', null);

  // 本次挥砍已覆盖的扇形
  if (st.swinging || st.angle < st.half - 1e-3) {
    const to = st.swinging ? st.angle : -st.half;
    drawVerticalSector(st.half, to, st.inner, st.reach, st.thickness,
      'rgba(255,216,102,0.5)', 'rgba(255,216,102,0.10)');
  }

  // 目标
  for (let i = 0; i < st.tx.length; i++) {
    capsuleAt(st.tx[i], st.tz[i], TARGETS[i].radius, TARGETS[i].height, '#ffa657');
  }
  // 玩家 + 朝向
  capsuleAt(st.px, st.pz, PLAYER.radius, PLAYER.height, '#7fd18b');
  line3({ x: st.px, y: CHEST_Y, z: st.pz },
        { x: st.px + f.x * 1.1, y: CHEST_Y, z: st.pz + f.z * 1.1 },
        'rgba(127,209,139,0.7)', 2, [4, 4]);

  // 刀：当前角度上的一根长剑（竖直平面内）
  const blade = {
    x: f.x * Math.cos(st.angle), y: Math.sin(st.angle), z: f.z * Math.cos(st.angle),
  };
  const hilt = { x: st.px + blade.x * st.inner, y: CHEST_Y + blade.y * st.inner, z: st.pz + blade.z * st.inner };
  const tip = { x: st.px + blade.x * st.reach, y: CHEST_Y + blade.y * st.reach, z: st.pz + blade.z * st.reach };
  line3(hilt, tip, '#e6e8ee', 4);
  dot3(tip, '#ffd866', 4);

  // 命中标记：目标身上的一块局部红
  for (const m of marks) {
    const x = st.tx[m.index] + m.dx;
    const z = st.tz[m.index] + m.dz;
    const s = proj(x, m.dy, z);
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
  const list = (out && out.hits && out.hits.length)
    ? out.hits.map((h) => `<span class="row">#${h.index} (${h.point.x.toFixed(2)}, ${h.point.y.toFixed(2)}, ${h.point.z.toFixed(2)})</span>`).join('　')
    : '<span style="color:#8b93a7">无</span>';
  const phase = st.swinging ? `挥砍中 ${(st.t * 100).toFixed(0)}%　刀角 ${((st.angle * 180) / Math.PI).toFixed(0)}°` : '待机（点画面挥砍）';
  document.getElementById('hud').innerHTML = warn +
    `<div>${phase}</div>` +
    `<div>竖直扇形：张角 ±${((st.half * 180) / Math.PI).toFixed(0)}°　射程 ${st.reach.toFixed(1)}　内圈 ${st.inner.toFixed(2)}　厚度 ±${st.thickness.toFixed(2)}</div>` +
    `<div>候选 <b>${out ? out.candidates : 0}</b>　本次命中 <span class="hit">${st.hitsThisSwing}</span>　` +
    `挥砍 ${st.swings} 次 / 有命中 ${st.hitAll} 次　累计标记 <b>${st.totalHits}</b></div>` +
    `<div style="margin-top:4px">命中部位：${list}</div>` +
    '<small>红块 = 引擎返回的命中部位（目标表面朝向扇心的点），会跟着目标移动 · ' +
    '黄色楔形 = 本次挥砍覆盖的竖直扇形 · 虚线 = 整段刀路 · 拖动转视角 · 滚轮缩放</small>';
}

// ---- 交互 ----
let dragging = false, moved = 0, lastPt = [0, 0];
canvas.addEventListener('pointerdown', (e) => {
  dragging = true; moved = 0; lastPt = [e.clientX, e.clientY];
  canvas.setPointerCapture(e.pointerId);
});
canvas.addEventListener('pointermove', (e) => {
  if (!dragging) return;
  const dx = e.clientX - lastPt[0], dy = e.clientY - lastPt[1];
  moved += Math.abs(dx) + Math.abs(dy);
  yaw -= dx * 0.01;
  pitch = Math.max(PITCH_MIN, Math.min(PITCH_MAX, pitch + dy * 0.01));
  lastPt = [e.clientX, e.clientY];
  updateCam();
});
canvas.addEventListener('pointerup', () => {
  dragging = false;
  if (moved < 6) startSwing(); // 没怎么拖 = 点击 → 挥砍
});
canvas.addEventListener('wheel', (e) => {
  e.preventDefault();
  zoom = Math.max(0.3, Math.min(4, zoom * Math.exp(-e.deltaY * 0.0015)));
}, { passive: false });
window.addEventListener('keydown', (e) => {
  if (e.code === 'Space') { e.preventDefault(); startSwing(); }
});

document.getElementById('a').addEventListener('input', (e) => {
  st.half = (Number(e.target.value) * Math.PI) / 360;
  document.getElementById('av').textContent = e.target.value + '°';
});
document.getElementById('r1').addEventListener('input', (e) => {
  st.reach = Number(e.target.value);
  document.getElementById('r1v').textContent = st.reach.toFixed(1);
});
document.getElementById('r0').addEventListener('input', (e) => {
  st.inner = Number(e.target.value);
  document.getElementById('r0v').textContent = st.inner.toFixed(2);
});
document.getElementById('th').addEventListener('input', (e) => {
  st.thickness = Number(e.target.value);
  document.getElementById('thv').textContent = st.thickness.toFixed(2);
});
document.getElementById('auto').addEventListener('click', (e) => {
  st.auto = !st.auto;
  e.target.classList.toggle('on', st.auto);
});
document.getElementById('chase').addEventListener('click', (e) => {
  st.chase = !st.chase;
  e.target.classList.toggle('on', st.chase);
});
document.getElementById('pause').addEventListener('click', (e) => {
  st.paused = !st.paused;
  e.target.textContent = st.paused ? '继续' : '暂停';
});
document.getElementById('reset').addEventListener('click', () => {
  setup();
  st.last = null;
});
window.addEventListener('resize', resize);

let last = performance.now();
function frame(now) {
  const dt = Math.min(0.05, (now - last) / 1000);
  last = now;
  stepSim(dt);
  stepSwing(dt);
  for (const m of marks) m.life = Math.max(0.4, m.life - dt * 0.2);
  render();
  updateHUD();
  requestAnimationFrame(frame);
}

(async function () {
  const go = new Go();
  const buf = await (await fetch('collide.wasm?v=9')).arrayBuffer();
  const mod = await WebAssembly.instantiate(buf, go.importObject);
  go.run(mod.instance);
  resize();
  setup();
  st.ready = true;
  requestAnimationFrame(frame);
})();
