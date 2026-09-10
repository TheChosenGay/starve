'use strict';

// ---- 轨道相机（与 index/sim 同一套正交投影）----
const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
let S = 62, zoom = 1, ox = 0, oy = 0;
let yaw = 0.85, pitch = 1.15;
const PITCH_MIN = 0.15, PITCH_MAX = Math.PI / 2 - 0.01;

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
  S = Math.min(r.width, r.height) / 9;
  updateCam();
}

// ---- 绘图小工具 ----
function line3(a, b, color, w, dash) {
  ctx.save();
  if (dash) ctx.setLineDash(dash);
  ctx.strokeStyle = color; ctx.lineWidth = w || 1;
  const p = proj(a.x, a.y, a.z), q = proj(b.x, b.y, b.z);
  ctx.beginPath();
  ctx.moveTo(p[0], p[1]); ctx.lineTo(q[0], q[1]);
  ctx.stroke(); ctx.restore();
}
function dot3(p, color, r) {
  const s = proj(p.x, p.y, p.z);
  ctx.fillStyle = color;
  ctx.beginPath(); ctx.arc(s[0], s[1], r || 4, 0, Math.PI * 2); ctx.fill();
}
function circle3(p, r, stroke, fill) {
  const s = proj(p.x, p.y, p.z);
  ctx.beginPath(); ctx.arc(s[0], s[1], r * S * zoom, 0, Math.PI * 2);
  if (fill) { ctx.fillStyle = fill; ctx.fill(); }
  if (stroke) { ctx.strokeStyle = stroke; ctx.lineWidth = 2; ctx.stroke(); }
}
const BOX_EDGES = [[0,1],[2,3],[4,5],[6,7],[0,2],[1,3],[4,6],[5,7],[0,4],[1,5],[2,6],[3,7]];
function drawAABB(b, color) {
  const corner = (i) => ({
    x: (i & 1) ? b.max.x : b.min.x,
    y: (i & 2) ? b.max.y : b.min.y,
    z: (i & 4) ? b.max.z : b.min.z,
  });
  for (const e of BOX_EDGES) line3(corner(e[0]), corner(e[1]), color, 2);
}
function drawOBB(b, color) {
  const corner = (i) => {
    const sx = (i & 1) ? 1 : -1, sy = (i & 2) ? 1 : -1, sz = (i & 4) ? 1 : -1;
    return {
      x: b.c.x + sx * b.e[0] * b.u[0].x + sy * b.e[1] * b.u[1].x + sz * b.e[2] * b.u[2].x,
      y: b.c.y + sx * b.e[0] * b.u[0].y + sy * b.e[1] * b.u[1].y + sz * b.e[2] * b.u[2].y,
      z: b.c.z + sx * b.e[0] * b.u[0].z + sy * b.e[1] * b.u[1].z + sz * b.e[2] * b.u[2].z,
    };
  };
  for (const e of BOX_EDGES) line3(corner(e[0]), corner(e[1]), color, 2);
}
function drawCapsule(c, color) {
  line3(c.a, c.b, color, 2 * c.r * S * zoom);
  circle3(c.a, c.r, color, null);
  circle3(c.b, c.r, color, null);
}
function drawGrid() {
  const N = 5;
  for (let i = -N; i <= N; i++) {
    line3({ x: i, y: 0, z: -N }, { x: i, y: 0, z: N }, '#232838', 1);
    line3({ x: -N, y: 0, z: i }, { x: N, y: 0, z: i }, '#232838', 1);
  }
}

// ---- 场景状态 ----
const MELEE_ORIGIN = { x: -2.6, y: 1.2, z: 0 };
const BLADE_LEN = 2.6;   // 刀长
const BLADE_HALF_H = 0.05; // 刀厚（竖直）
const BLADE_HALF_W = 0.12; // 刀宽（挥砍平面内）
const flashes = [];

const st = {
  mode: 'bullet',
  bullet: { x: -5.5, y: 1.2, z: 0 },
  angle: 1.25,
  done: false,
  hold: 0,
  hitInfo: null,
  wasmMissing: false,
  paused: false,
  runs: 0,   // 已跑完的轮数
  hits: 0,   // 其中命中的轮数
  // 每轮随机化的场景参数（循环跑出不同用例，才看得出检测准不准）
  bulletY: 1.2,
  bulletZ: 0,
  targetX: -1.0,
  targetZ: 0,
};
const BULLET_SPEED = 34;
const SWING_SPEED = -4.2;
const rand = (a, b) => a + Math.random() * (b - a);

function swordTip(angle) {
  return {
    x: MELEE_ORIGIN.x + Math.cos(angle) * BLADE_LEN,
    y: MELEE_ORIGIN.y,
    z: MELEE_ORIGIN.z + Math.sin(angle) * BLADE_LEN,
  };
}

// 刀的 OBB 基：U0 = 刀身方向，U1 = 竖直，U2 = 挥砍平面内的侧向（与 Go 侧 bladeOBB 一致）
function bladeBasis(angle) {
  const dir = { x: Math.cos(angle), y: 0, z: Math.sin(angle) };
  const up = { x: 0, y: 1, z: 0 };
  const side = { x: -dir.z, y: 0, z: dir.x };
  return { dir, up, side };
}
function bladeBox(angle) {
  const b = bladeBasis(angle);
  const half = BLADE_LEN / 2;
  return {
    c: {
      x: MELEE_ORIGIN.x + b.dir.x * half,
      y: MELEE_ORIGIN.y,
      z: MELEE_ORIGIN.z + b.dir.z * half,
    },
    u: [b.dir, b.up, b.side],
    e: [half, BLADE_HALF_H, BLADE_HALF_W],
  };
}

function targets() {
  if (st.mode === 'bullet') {
    return [
      { kind: 'aabb', min: { x: -2.8, y: 0, z: -2.6 }, max: { x: -1.4, y: 2.6, z: -0.8 } },
      { kind: 'capsule', a: { x: st.targetX, y: 0, z: st.targetZ }, b: { x: st.targetX, y: 1.8, z: st.targetZ }, r: 0.35 },
      // 这块盒子故意放在弹道侧旁：只是场景装饰，不参与命中，避免“每轮必中”
      { kind: 'aabb', min: { x: 2.4, y: 0, z: 1.8 }, max: { x: 3.8, y: 2.0, z: 3.2 } },
    ];
  }
  const list = [{ kind: 'capsule', a: { x: st.targetX, y: 0, z: st.targetZ }, b: { x: st.targetX, y: 1.8, z: st.targetZ }, r: 0.4 }];
  if (st.mode === 'meleeBlocked') {
    list.push({ kind: 'aabb', min: { x: -1.5, y: 0, z: -2.6 }, max: { x: -1.3, y: 2.4, z: 2.6 } });
  }
  return list;
}

function cast(sphere, motion) {
  if (typeof window.collideCast !== 'function') { st.wasmMissing = true; return { hit: false }; }
  try {
    const out = JSON.parse(window.collideCast(JSON.stringify({ sphere, motion, targets: targets() })));
    st.wasmMissing = false;
    return out;
  } catch (e) {
    st.wasmMissing = true;
    return { hit: false };
  }
}

// 劈砍：刀是 OBB，交给 Go 在角区间内细分扫掠（旋转运动的保守前进）
function swing(angleFrom, angleTo) {
  if (typeof window.collideSwing !== 'function') { st.wasmMissing = true; return null; }
  const payload = JSON.stringify({
    blade: { origin: MELEE_ORIGIN, length: BLADE_LEN, halfHeight: BLADE_HALF_H, halfWidth: BLADE_HALF_W },
    angleFrom, angleTo, subSteps: 20, targets: targets(),
  });
  try {
    const out = JSON.parse(window.collideSwing(payload));
    st.wasmMissing = false;
    return out;
  } catch (e) {
    st.wasmMissing = true;
    return null;
  }
}

function addFlash(p) {
  flashes.push({ x: p.x, y: p.y, z: p.z, life: 1 });
  if (flashes.length > 40) flashes.shift();
}

// nextRun 开启新一轮：随机化场景参数，让循环覆盖不同用例
function nextRun() {
  st.done = false;
  st.hold = 0;
  st.hitInfo = null;
  flashes.length = 0;
  if (st.mode === 'bullet') {
    st.bulletY = rand(0.8, 1.8);
    st.bulletZ = rand(-0.45, 0.45);
    st.targetX = rand(-1.2, 0.6);
    st.targetZ = rand(-0.9, 0.9);
    st.bullet = { x: -5.5, y: st.bulletY, z: st.bulletZ };
  } else {
    st.angle = rand(1.1, 1.35);
    st.targetX = rand(-0.15, 1.05);
    st.targetZ = rand(-0.3, 0.3);
  }
}

// reset 供“重放 / 切换场景”使用：重新开始，但不计入统计
function reset() {
  st.runs = 0;
  st.hits = 0;
  nextRun();
}

// finishRun 结束本轮并计入统计
function finishRun(hit, hold) {
  st.done = true;
  st.hold = hold;
  st.runs++;
  if (hit) {
    st.hits++;
  }
}

function step(dt) {
  if (st.paused) {
    return;
  }
  if (st.done) {
    st.hold -= dt;
    if (st.hold <= 0) nextRun();
    return;
  }
  if (st.mode === 'bullet') {
    const motion = { x: BULLET_SPEED * dt, y: 0, z: 0 };
    const out = cast({ c: { ...st.bullet }, r: 0.12 }, motion);
    if (out.hit) {
      st.bullet.x += motion.x * out.best.t;
      st.hitInfo = out.best;
      addFlash(out.best.point);
      finishRun(true, 0.5);
      return;
    }
    st.bullet.x += motion.x;
    if (st.bullet.x > 6.5) finishRun(false, 0.25);
    return;
  }
  // 劈砍：整把刀（OBB）在 [angleFrom, angleTo] 内细分扫掠，最早命中的角度即结果
  const angleFrom = st.angle;
  const angleTo = st.angle + SWING_SPEED * dt;
  const out = swing(angleFrom, angleTo);
  if (out && out.hit) {
    st.angle = out.best.angle;
    st.hitInfo = out.best;
    addFlash(out.best.point);
    finishRun(true, 0.5);
    return;
  }
  st.angle = angleTo;
  if (st.angle < -0.55) finishRun(false, 0.25);
}

// ---- 渲染 ----
function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawGrid();

  for (const t of targets()) {
    if (t.kind === 'aabb') drawAABB({ min: t.min, max: t.max }, '#6ea8fe');
    else drawCapsule({ a: t.a, b: t.b, r: t.r }, '#ffa657');
  }

  if (st.mode === 'bullet') {
    line3({ x: -5.5, y: st.bullet.y, z: st.bullet.z }, st.bullet, '#ffd866', 1, [4, 4]);
    circle3(st.bullet, 0.12, '#ffd866', 'rgba(255,216,102,0.35)');
  } else {
    drawCapsule({ a: { x: MELEE_ORIGIN.x, y: 0, z: 0 }, b: { x: MELEE_ORIGIN.x, y: 1.8, z: 0 }, r: 0.35 }, '#7fd18b');
    drawOBB(bladeBox(st.angle), '#e6e8ee'); // 刀用 OBB 画
    dot3(swordTip(st.angle), '#ffd866', 3.5);
  }

  for (const f of flashes) {
    const s = proj(f.x, f.y, f.z);
    const rad = Math.max(4, 16 * f.life);
    ctx.globalAlpha = Math.max(0, f.life) * 0.9;
    ctx.fillStyle = '#ff4d4d';
    ctx.beginPath(); ctx.arc(s[0], s[1], rad, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = '#ffb3b3'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(s[0], s[1], rad + 4, 0, Math.PI * 2); ctx.stroke();
    ctx.globalAlpha = 1;
  }
}

function updateHUD() {
  const warn = st.wasmMissing ? '<div class="hit"><b>WASM 未就绪</b>：请强制刷新</div>' : '';
  let info = '<div>状态：飞行 / 挥砍中…</div>';
  if (st.hitInfo) {
    const b = st.hitInfo;
    info =
      `<div>命中：<b>#${b.index} ${b.kind}</b></div>` +
      `<div class="hit">接触点：(${b.point.x.toFixed(2)}, ${b.point.y.toFixed(2)}, ${b.point.z.toFixed(2)})</div>` +
      (b.t !== undefined
        ? `<div>沿运动比例 t=${b.t.toFixed(3)}　距离 ${b.dist.toFixed(2)}</div>`
        : `<div>命中角 θ=${b.angle.toFixed(3)}　穿透深度 ${b.depth.toFixed(3)}</div>`);
  } else if (st.done) {
    info = '<div>状态：<span class="hit">未命中</span></div>';
  }
  const rate = st.runs > 0 ? Math.round((st.hits / st.runs) * 100) + '%' : '—';
  document.getElementById('hud').innerHTML = warn + info +
    `<div>循环：第 ${st.runs + 1} 次　命中 <b>${st.hits}</b> / ${st.runs}（${rate}）</div>` +
    '<small>每轮随机化目标位置 · 子弹走 SweepSphere*，劈砍走刀 OBB 的角度细分扫掠 · 拖空白处旋转 · 滚轮缩放</small>';
  document.getElementById('pause').textContent = st.paused ? '继续' : '暂停';
}

// ---- 交互 ----
let dragMode = null, lastPt = [0, 0];
function pick(ev) {
  const r = canvas.getBoundingClientRect();
  return [ev.clientX - r.left, ev.clientY - r.top];
}
canvas.addEventListener('pointerdown', (ev) => {
  dragMode = 'orbit';
  lastPt = pick(ev);
  canvas.setPointerCapture(ev.pointerId);
});
canvas.addEventListener('pointermove', (ev) => {
  if (!dragMode) return;
  const [mx, my] = pick(ev);
  yaw -= (mx - lastPt[0]) * 0.01;
  pitch = Math.max(PITCH_MIN, Math.min(PITCH_MAX, pitch + (my - lastPt[1]) * 0.01));
  lastPt = [mx, my];
  updateCam();
});
canvas.addEventListener('pointerup', () => { dragMode = null; });
canvas.addEventListener('wheel', (ev) => {
  ev.preventDefault();
  zoom = Math.max(0.35, Math.min(4, zoom * Math.exp(-ev.deltaY * 0.0015)));
}, { passive: false });

const MODES = [
  ['bullet', '子弹射击'],
  ['melee', '劈砍'],
  ['meleeBlocked', '劈砍（隔墙）'],
];
const modesEl = document.getElementById('modes');
MODES.forEach((m) => {
  const b = document.createElement('button');
  b.textContent = m[1];
  b.className = (m[0] === st.mode) ? 'on' : '';
  b.onclick = () => {
    st.mode = m[0];
    for (const c of modesEl.children) c.className = '';
    b.className = 'on';
    reset();
  };
  modesEl.appendChild(b);
});
document.getElementById('replay').addEventListener('click', reset);
document.getElementById('pause').addEventListener('click', () => {
  st.paused = !st.paused;
  document.getElementById('pause').textContent = st.paused ? '继续' : '暂停';
});
window.addEventListener('resize', resize);

// ---- 主循环 ----
let last = performance.now();
function frame(now) {
  const dt = Math.min(0.05, (now - last) / 1000);
  last = now;
  step(dt);
  for (const f of flashes) f.life -= dt * 1.6;
  for (let i = flashes.length - 1; i >= 0; i--) if (flashes[i].life <= 0) flashes.splice(i, 1);
  render();
  updateHUD();
  requestAnimationFrame(frame);
}

(async function () {
  const go = new Go();
  const buf = await (await fetch('collide.wasm?v=7')).arrayBuffer();
  const mod = await WebAssembly.instantiate(buf, go.importObject);
  go.run(mod.instance);
  resize();
  reset();
  requestAnimationFrame(frame);
})();
