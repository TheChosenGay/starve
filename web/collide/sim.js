'use strict';

// ---- 轨道相机（与 app.js 同一套正交投影）----
const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
let S = 62, zoom = 1, ox = 0, oy = 0;
let yaw = 0.7, pitch = 0.95;
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
function depth(p) { return p.x * cam.eye.X + p.y * cam.eye.Y + p.z * cam.eye.Z; }

function resize() {
  const r = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(1, Math.round(r.width * dpr));
  canvas.height = Math.max(1, Math.round(r.height * dpr));
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ox = r.width * 0.5;
  oy = r.height * 0.62;
  S = Math.min(r.width, r.height) / 9;
  updateCam();
}

// ---- 场景 ----
const sim = {
  bodies: [],
  flashes: [],
  dt: 1 / 60,
  gravity: 22,
  floorY: 0,
  bound: 3,        // 围栏半边长
  height: 8,       // 天花板高度（仅渲染）
  rest: 0.25,
  friction: 0.06,
  totalContacts: 0,
};

function reset(count) {
  sim.bodies = [];
  sim.flashes = [];
  sim.totalContacts = 0;
  for (let i = 0; i < count; i++) {
    const r = 0.22 + Math.random() * 0.28;
    sim.bodies.push({
      p: {
        x: (Math.random() * 2 - 1) * (sim.bound - r - 0.1),
        y: 1.5 + Math.random() * (sim.height - 2.5),
        z: (Math.random() * 2 - 1) * (sim.bound - r - 0.1),
      },
      v: { x: (Math.random() * 2 - 1) * 0.6, y: 0, z: (Math.random() * 2 - 1) * 0.6 },
      r,
    });
  }
}

function stepSim() {
  if (typeof window.collideSim !== 'function') {
    sim.wasmMissing = true;
    return;
  }
  const payload = JSON.stringify({
    bodies: sim.bodies,
    dt: sim.dt,
    gravity: sim.gravity,
    floorY: sim.floorY,
    bound: sim.bound,
    rest: sim.rest,
    friction: sim.friction,
  });
  let out;
  try {
    out = JSON.parse(window.collideSim(payload));
  } catch (e) {
    sim.wasmMissing = true;
    return;
  }
  if (out.error) return;
  sim.wasmMissing = false;
  sim.bodies = out.bodies;
  for (const c of out.contacts) {
    // 运行期守卫：接触点必须带坐标，否则红点会画在 NaN 上而“消失”
    if (typeof c.x !== 'number' || typeof c.y !== 'number' || typeof c.z !== 'number') {
      sim.badContactData = true;
      continue;
    }
    sim.badContactData = false;
    sim.flashes.push({ x: c.x, y: c.y, z: c.z, r: Math.max(0.12, c.r * 0.5), life: 1 });
    sim.totalContacts++;
  }
  // 红点数量上限，避免长时间高负载下无限增长
  if (sim.flashes.length > 400) sim.flashes.splice(0, sim.flashes.length - 400);
}

// ---- 渲染 ----
function drawGrid() {
  const b = sim.bound;
  const N = 6;
  for (let i = -N; i <= N; i++) {
    const t = (i / N) * b;
    lineAt(t, sim.floorY, -b, t, sim.floorY, b, '#232838');
    lineAt(-b, sim.floorY, t, b, sim.floorY, t, '#232838');
  }
}
function lineAt(x1, y1, z1, x2, y2, z2, color) {
  const a = proj(x1, y1, z1), c = proj(x2, y2, z2);
  ctx.strokeStyle = color; ctx.lineWidth = 1;
  ctx.beginPath(); ctx.moveTo(a[0], a[1]); ctx.lineTo(c[0], c[1]); ctx.stroke();
}
function drawContainer() {
  const b = sim.bound, y0 = sim.floorY, y1 = sim.height;
  const corners = [
    [-b, y0, -b], [b, y0, -b], [b, y0, b], [-b, y0, b],
    [-b, y1, -b], [b, y1, -b], [b, y1, b], [-b, y1, b],
  ];
  const edges = [[0,1],[1,2],[2,3],[3,0],[4,5],[5,6],[6,7],[7,4],[0,4],[1,5],[2,6],[3,7]];
  for (const [i, j] of edges) {
    const p = corners[i], q = corners[j];
    lineAt(p[0], p[1], p[2], q[0], q[1], q[2], '#2b3346');
  }
}

function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawGrid();
  drawContainer();

  const k = S * zoom;
  // 远的先画（画家算法）
  const order = sim.bodies.map((b, i) => [depth(b.p), i]).sort((a, c) => a[0] - c[0]);
  for (const [, i] of order) {
    const b = sim.bodies[i];
    const [sx, sy] = proj(b.p.x, b.p.y, b.p.z);
    const rad = b.r * k;
    const g = ctx.createRadialGradient(sx - rad * 0.3, sy - rad * 0.35, rad * 0.15, sx, sy, rad);
    g.addColorStop(0, '#a9c8ff');
    g.addColorStop(1, '#3f6fd8');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(sx, sy, rad, 0, Math.PI * 2); ctx.fill();
  }

  // 接触点：短暂红色
  for (const f of sim.flashes) {
    const [sx, sy] = proj(f.x, f.y, f.z);
    const rad = Math.max(3.5, f.r * k);
    ctx.globalAlpha = Math.max(0, f.life) * 0.9;
    ctx.fillStyle = '#ff4d4d';
    ctx.beginPath(); ctx.arc(sx, sy, rad, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = '#ffb3b3'; ctx.lineWidth = 1.5;
    ctx.beginPath(); ctx.arc(sx, sy, rad + 3, 0, Math.PI * 2); ctx.stroke();
    ctx.globalAlpha = 1;
  }
}

function updateHUD() {
  const warn = sim.wasmMissing
    ? `<div style="color:#ff7b72"><b>WASM 未就绪</b>：请强制刷新（Cmd+Shift+R）</div>`
    : (sim.badContactData
      ? `<div style="color:#ff7b72"><b>接触数据缺少坐标</b>：wasm 版本不匹配，请强制刷新</div>`
      : '');
  document.getElementById('hud').innerHTML =
    warn +
    `<div>物体：${sim.bodies.length}　接触点：${sim.flashes.length}（累计 ${sim.totalContacts}）</div>` +
    `<div>重力：${sim.gravity}　弹性：${sim.rest.toFixed(2)}</div>` +
    `<small>碰撞判定全部由 Go (WASM) 计算；红点是本帧的接触位置 · 拖空白处旋转 · 滚轮缩放</small>`;
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

document.getElementById('n').addEventListener('input', (ev) => {
  document.getElementById('nv').textContent = ev.target.value;
  reset(parseInt(ev.target.value, 10));
});
document.getElementById('r').addEventListener('input', (ev) => {
  sim.rest = parseFloat(ev.target.value);
  document.getElementById('rv').textContent = sim.rest.toFixed(2);
});
document.getElementById('reset').addEventListener('click', () => {
  reset(parseInt(document.getElementById('n').value, 10));
});
window.addEventListener('resize', () => { resize(); });

// ---- 主循环 ----
let lastT = performance.now(), acc = 0;
function frame(now) {
  const real = Math.min(0.1, (now - lastT) / 1000);
  lastT = now;
  acc += real;
  let steps = 0;
  while (acc >= sim.dt && steps < 4) {
    stepSim();
    acc -= sim.dt;
    steps++;
  }
  const decay = real * 2.5;
  for (const f of sim.flashes) f.life -= decay;
  if (sim.flashes.length) sim.flashes = sim.flashes.filter(f => f.life > 0);
  render();
  updateHUD();
  requestAnimationFrame(frame);
}

// ---- 启动 ----
(async function () {
  const go = new Go();
  const buf = await (await fetch('collide.wasm?v=7')).arrayBuffer();
  const mod = await WebAssembly.instantiate(buf, go.importObject);
  go.run(mod.instance);
  resize();
  reset(parseInt(document.getElementById('n').value, 10));
  requestAnimationFrame(frame);
})();
