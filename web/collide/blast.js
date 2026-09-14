'use strict';

// 丢炸弹：点画面 = 把炸弹丢到鼠标指的地面位置，引信到点爆炸，
// **爆炸半径内的胶囊全部闪红**，并按强度被推离爆心。
//
// 两步几何都在 Go 侧算，前端只把屏幕坐标换成世界空间的射线：
//   collideBlastAim     射线 → 地面落点（IntersectRayAABB 打一块厚地面板）+ 宽阶段候选数
//   collideBlastThrow   投一颗（同一套射线 → 落点 + 引信）
//   collideBlastStep    每帧：漫游 / 击退 / 闪红衰减 / 引信倒计时 / 到点做球形爆炸查询

const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
let S = 62, zoom = 1, ox = 0, oy = 0;
let yaw = 2.35, pitch = 0.55;
const PITCH_MIN = 0.18, PITCH_MAX = Math.PI / 2 - 0.01;

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
const ARENA = 9;
const RAY_DIST = 60;         // 正交相机：视线起点放这么远（够远就行）
const AUTO_PERIOD = 1.5;     // 自动投弹间隔（秒）
const BOMB_Y = 0.35;         // 爆心离地高度（与 Go 侧 blastBombY 保持一致：炸弹躺在地上）
const THROWER = { x: -2.5, z: -5.5, radius: 0.4, height: 1.8 };
const TARGETS = [
  { x: 3.2, z: -1.0, dir: 0.4, speed: 1.0 },
  { x: -1.0, z: 4.0, dir: 2.2, speed: 1.3 },
  { x: 5.0, z: 3.4, dir: 3.4, speed: 0.8 },
  { x: -5.5, z: 2.6, dir: 1.1, speed: 1.5 },
  { x: 0.5, z: 0.5, dir: 5.0, speed: 1.2 },
  { x: 1.6, z: -3.4, dir: 0.9, speed: 1.1 },
  { x: -6.2, z: -2.4, dir: 2.6, speed: 1.0 },
  { x: 4.2, z: -5.2, dir: 1.6, speed: 1.2 },
  { x: -3.2, z: -1.2, dir: 4.2, speed: 0.9 },
  { x: 6.4, z: 1.0, dir: 3.0, speed: 1.1 },
  { x: -2.0, z: 5.6, dir: 5.6, speed: 1.3 },
  { x: 2.4, z: 2.6, dir: 0.2, speed: 1.4 },
].map((t) => ({ ...t, radius: 0.35, height: 1.7 }));

const st = {
  radius: 4,
  fuse: 0.7,
  power: 7,
  paused: false,
  ready: false,
  wasmMissing: false,
  // 来自引擎的每帧状态
  tx: [], tz: [], ty: [], flash: [], spin: [], rollX: [], rollZ: [],
  // 鼠标瞄准（落点由 Go 算）
  mouse: null,
  aim: null,
  aimMs: 0,
  // 视觉：飞行中的炸弹 / 冲击波 / 命中红点
  shots: [],
  booms: [],
  marks: [],
  explosions: 0,
  hitTotal: 0,
  lastBoom: null,
  stepMs: 0,
  auto: new URLSearchParams(location.search).get('auto') === '1',
  autoTimer: 0,
  seed: 12345,
  warmup: 0, // 开局先空跑 N 帧（演示 / 截图用，见下面的 URL 参数）
};

// URL 参数覆盖（演示 / 截图用）：blast.html?auto=1&radius=5&fuse=0.2&power=12
{
  const q = new URLSearchParams(location.search);
  const num = (key, lo, hi, fallback) => {
    const v = Number(q.get(key));
    if (!Number.isFinite(v) || q.get(key) === null) return fallback;
    return Math.max(lo, Math.min(hi, v));
  };
  st.radius = num('radius', 1, 8, st.radius);
  st.fuse = num('fuse', 0.05, 2, st.fuse);
  st.power = num('power', 0, 16, st.power);
  st.warmup = Math.round(num('steps', 0, 600, 0));
}

// warmupFrames 开局空跑 N 帧（与 frame() 同一套顺序：先自动投弹、再步进模拟）。
// 无头截图 / 演示链接用：blast.html?auto=1&fuse=0.05&steps=20 一打开就是"刚炸完"的画面。
function warmupFrames() {
  if (st.warmup <= 0) return;
  const dt = 1 / 60;
  for (let i = 0; i < st.warmup; i++) {
    if (st.auto) {
      st.autoTimer -= dt;
      if (st.autoTimer <= 0) { autoThrow(); st.autoTimer = AUTO_PERIOD; }
    }
    stepSim(dt);
  }
}

// 把（可能被 URL 改过的）参数写回滑杆，免得按钮说 4.0 而实际是 5.0
function syncControls() {
  const put = (id, value, digits) => {
    document.getElementById(id).value = String(value);
    document.getElementById(id + 'v').textContent = value.toFixed(digits);
  };
  put('rad', st.radius, 1);
  put('fuse', st.fuse, 2);
  put('pow', st.power, 1);
}

function setup() {
  st.shots.length = 0;
  st.booms.length = 0;
  st.marks.length = 0;
  st.lastBoom = null;
  st.explosions = 0;
  st.hitTotal = 0;
  st.aim = null;
  if (typeof window.collideBlastSetup !== 'function') { st.wasmMissing = true; return; }
  const out = JSON.parse(window.collideBlastSetup(JSON.stringify({
    thrower: THROWER, targets: TARGETS, arena: ARENA,
  })));
  st.wasmMissing = !out.ok;
  aim(); // 立刻算一次瞄准，免得开局 HUD 是空的
}

// ---- 屏幕 → 世界射线（正交相机，方向 = 视线反向） ----
function rayFromScreen(clientX, clientY) {
  const rect = canvas.getBoundingClientRect();
  const k = S * zoom;
  const a = (clientX - rect.left - ox) / k;
  const b = (oy - (clientY - rect.top)) / k;
  const e = cam.eye;
  return {
    origin: {
      X: cam.r.X * a + cam.u.X * b + e.X * RAY_DIST,
      Y: cam.r.Y * a + cam.u.Y * b + e.Y * RAY_DIST,
      Z: cam.r.Z * a + cam.u.Z * b + e.Z * RAY_DIST,
    },
    dir: { X: -e.X, Y: -e.Y, Z: -e.Z },
    max: RAY_DIST * 2,
    radius: st.radius,
  };
}

function aim() {
  if (!st.ready || st.wasmMissing || !st.mouse) return;
  const out = JSON.parse(window.collideBlastAim(JSON.stringify(rayFromScreen(st.mouse[0], st.mouse[1]))));
  if (out.error) return;
  st.aim = out.ok ? out : null;
  st.aimMs = out.ms || 0;
}

function throwBomb(clientX, clientY) {
  if (!st.ready || st.wasmMissing || st.paused) return null;
  const ray = rayFromScreen(clientX, clientY);
  ray.fuse = st.fuse;
  ray.power = st.power;
  const out = JSON.parse(window.collideBlastThrow(JSON.stringify(ray)));
  if (!out.ok) return null; // 指针没指到地面（比如平视）就不丢
  st.aim = { ok: true, x: out.x, y: out.y, z: out.z };
  return out;
}

// 自动投弹：对着某个目标的头顶正上方丢（射线竖直向下，落点正好是它脚下）。
function autoThrow() {
  if (!TARGETS.length) return;
  st.seed = (st.seed * 1103515245 + 12345) & 0x7fffffff;
  const i = st.seed % TARGETS.length;
  const t = { x: st.tx[i] ?? TARGETS[i].x, z: st.tz[i] ?? TARGETS[i].z };
  const out = JSON.parse(window.collideBlastThrow(JSON.stringify({
    origin: { X: t.x, Y: RAY_DIST, Z: t.z },
    dir: { X: 0, Y: -1, Z: 0 },
    max: RAY_DIST * 4,
    radius: st.radius,
    fuse: st.fuse,
    power: st.power,
  })));
  if (out.ok) st.aim = { ok: true, x: out.x, y: out.y, z: out.z };
}

function stepSim(dt) {
  if (!st.ready || st.wasmMissing || st.paused) return;
  const out = JSON.parse(window.collideBlastStep(JSON.stringify({
    dt, speed: 1.1, damping: 3,
  })));
  if (out.error) return;
  st.tx = out.x || [];
  st.tz = out.z || [];
  st.ty = out.y || [];
  st.flash = out.flash || [];
  st.spin = out.spin || [];
  st.rollX = out.rollX || [];
  st.rollZ = out.rollZ || [];
  st.stepMs = out.ms || 0;
  st.explosions = out.explosions || 0;
  st.hitTotal = out.hitTotal || 0;
  // 飞行中的炸弹：直接照引擎回传的剩余引信重建（不至于与 Go 侧状态漂移）
  st.shots = (out.bombs || []).map((b) => ({ ...b }));
  for (const boom of out.booms || []) {
    st.booms.push({ x: boom.x, z: boom.z, radius: boom.radius, life: 1 });
    if (st.booms.length > 8) st.booms.shift();
    st.lastBoom = boom;
    for (const h of boom.hits) {
      if (st.tx[h.index] === undefined) continue;
      st.marks.push({
        index: h.index,
        dx: h.point.x - st.tx[h.index],
        dy: h.point.y - (st.ty[h.index] || 0), // 相对目标底端，被炸飞时也跟着走
        dz: h.point.z - st.tz[h.index],
        life: 1,
      });
    }
    while (st.marks.length > 40) st.marks.shift();
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
function dot3(p, color, r, glow) {
  const s = proj(p.x, p.y, p.z);
  if (glow) {
    const g = ctx.createRadialGradient(s[0], s[1], 0, s[0], s[1], glow);
    g.addColorStop(0, color);
    g.addColorStop(1, 'rgba(0,0,0,0)');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(s[0], s[1], glow, 0, Math.PI * 2); ctx.fill();
    return;
  }
  ctx.fillStyle = color;
  ctx.beginPath(); ctx.arc(s[0], s[1], r || 4, 0, Math.PI * 2); ctx.fill();
}
function poly3(points, stroke, fill, w, dash) {
  if (points.length < 2) return;
  ctx.save();
  if (dash) ctx.setLineDash(dash);
  ctx.beginPath();
  const first = proj(points[0].x, points[0].y, points[0].z);
  ctx.moveTo(first[0], first[1]);
  for (let i = 1; i < points.length; i++) {
    const s = proj(points[i].x, points[i].y, points[i].z);
    ctx.lineTo(s[0], s[1]);
  }
  if (fill) { ctx.fillStyle = fill; ctx.fill(); }
  if (stroke) { ctx.strokeStyle = stroke; ctx.lineWidth = w || 1; ctx.stroke(); }
  ctx.restore();
}
// 水平圆（地面上的爆炸圈）
function groundCircle(cx, cz, r, y, stroke, fill, w, dash) {
  const steps = 64, pts = [];
  for (let i = 0; i <= steps; i++) {
    const a = (i / steps) * Math.PI * 2;
    pts.push({ x: cx + Math.cos(a) * r, y, z: cz + Math.sin(a) * r });
  }
  poly3(pts, stroke, fill, w, dash);
}
// 爆炸球的三条大圆（两条竖直 + 一条水平），看着像个球而不是一个圈
function sphereWire(cx, cy, cz, r, stroke, dash) {
  const steps = 48, mk = (fn) => {
    const pts = [];
    for (let i = 0; i <= steps; i++) pts.push(fn((i / steps) * Math.PI * 2));
    return pts;
  };
  poly3(mk((a) => ({ x: cx + Math.cos(a) * r, y: cy, z: cz + Math.sin(a) * r })), stroke, null, 1, dash);
  poly3(mk((a) => ({ x: cx + Math.cos(a) * r, y: cy + Math.sin(a) * r, z: cz })), stroke, null, 1, dash);
  poly3(mk((a) => ({ x: cx, y: cy + Math.sin(a) * r, z: cz + Math.cos(a) * r })), stroke, null, 1, dash);
}
// 胶囊：颜色按闪红强度从橙（#ffa657）插值到亮红，并叠一层外发光。
// y = 离地高度、spin = 翻滚角（绕「竖直 × 击飞方向」轴转），被炸飞时整个身子翻着飞出去。
function capsuleAt(x, z, radius, height, flash, y, spin, rollX, rollZ) {
  const f = Math.max(0, Math.min(1, flash || 0));
  const base = y || 0;
  const g = Math.round(166 - 120 * f), b = Math.round(87 - 70 * f);
  const color = `rgb(255,${g},${b})`;
  // 地面阴影：飞得越高越淡越散，一眼能看出离地多高
  if (base > 0.01) {
    const sh = proj(x, 0.01, z);
    const sr = radius * S * zoom * (1 + base * 0.25);
    ctx.beginPath();
    ctx.ellipse(sh[0], sh[1], sr, sr * 0.45, 0, 0, Math.PI * 2);
    ctx.fillStyle = `rgba(0,0,0,${Math.max(0.12, 0.34 - base * 0.05)})`;
    ctx.fill();
  }
  // 翻滚轴 = 竖直 × 击飞方向 = (rollZ, 0, -rollX)，再把"向上"绕它转 spin
  let ux = 0, uy = 1, uz = 0;
  const rl = Math.hypot(rollX || 0, rollZ || 0);
  if (rl > 1e-6 && spin) {
    const ax = (rollZ || 0) / rl, az = -(rollX || 0) / rl;
    const cs = Math.cos(spin), sn = Math.sin(spin);
    ux = -az * sn; uy = cs; uz = ax * sn;
  }
  const half = height / 2;
  const midY = base + half;
  const a = { x: x - ux * half, y: midY - uy * half, z: z - uz * half };
  const c = { x: x + ux * half, y: midY + uy * half, z: z + uz * half };
  line3(a, c, color, 2 * radius * S * zoom);
  for (const p of [a, c]) {
    const s = proj(p.x, p.y, p.z);
    ctx.beginPath(); ctx.arc(s[0], s[1], radius * S * zoom, 0, Math.PI * 2);
    ctx.strokeStyle = color; ctx.lineWidth = 1.5; ctx.stroke();
  }
  if (f > 0.02) {
    const mid = proj(x, base + height * 0.55, z);
    const r = (0.9 + 0.9 * f) * S * zoom;
    const grad = ctx.createRadialGradient(mid[0], mid[1], 0, mid[0], mid[1], r);
    grad.addColorStop(0, `rgba(255,${Math.round(90 - 60 * f)},60,${0.55 * f})`);
    grad.addColorStop(1, 'rgba(255,0,0,0)');
    ctx.fillStyle = grad;
    ctx.beginPath(); ctx.arc(mid[0], mid[1], r, 0, Math.PI * 2); ctx.fill();
    if (f > 0.7) {
      dot3({ x, y: base + height * 0.55, z }, `rgba(255,240,220,${(f - 0.7) / 0.3})`, 0, 0.5 * S * zoom);
    }
  }
}
function drawArena() {
  const N = 5;
  for (let i = -N; i <= N; i++) {
    const t = (i / N) * ARENA;
    line3({ x: t, y: 0, z: -ARENA }, { x: t, y: 0, z: ARENA }, '#232838', 1);
    line3({ x: -ARENA, y: 0, z: t }, { x: ARENA, y: 0, z: t }, '#232838', 1);
  }
  groundCircle(0, 0, ARENA, 0, 'rgba(90,100,130,0.35)', null, 1, [6, 6]);
}

function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawArena();

  // 瞄准预览：地面圈 + 爆炸球线框（落点来自 Go 的射线 → 地面）
  if (st.aim && st.aim.ok) {
    groundCircle(st.aim.x, st.aim.z, st.radius, 0.01, 'rgba(255,216,102,0.55)', 'rgba(255,216,102,0.06)', 1.5, [5, 4]);
    sphereWire(st.aim.x, BOMB_Y, st.aim.z, st.radius, 'rgba(255,216,102,0.16)', [3, 5]);
    dot3({ x: st.aim.x, y: 0.02, z: st.aim.z }, '#ffd866', 3.5);
  }

  // 爆炸：冲击波（贴地扩散到半径）+ 火球
  for (const bm of st.booms) {
    const k = 1 - bm.life; // 0 → 1
    const rr = bm.radius * (0.25 + 0.75 * k);
    groundCircle(bm.x, bm.z, rr, 0.02, `rgba(255,150,60,${0.55 * bm.life})`, `rgba(255,90,30,${0.1 * bm.life})`, 2.5);
    groundCircle(bm.x, bm.z, rr * 0.55, 0.02, `rgba(255,230,180,${0.5 * bm.life})`, null, 1.5);
    dot3({ x: bm.x, y: 0.35, z: bm.z },
      `rgba(255,${Math.round(120 + 120 * bm.life)},80,${0.75 * bm.life})`, 0,
      Math.max(4, bm.radius * 0.5 * S * zoom * bm.life));
  }

  // 目标
  for (let i = 0; i < st.tx.length; i++) {
    capsuleAt(st.tx[i], st.tz[i], TARGETS[i].radius, TARGETS[i].height, st.flash[i] || 0,
      st.ty[i] || 0, st.spin[i] || 0, st.rollX[i] || 0, st.rollZ[i] || 0);
  }

  // 投掷手
  capsuleAt(THROWER.x, THROWER.z, THROWER.radius, THROWER.height, 0, 0, 0, 0, 0);

  // 炸弹：从投掷手到落点的抛物线 + 引信环
  for (const b of st.shots) {
    const t = b.total > 0 ? Math.max(0, Math.min(1, 1 - b.fuse / b.total)) : 1;
    const from = { x: THROWER.x, z: THROWER.z };
    const dist = Math.hypot(b.x - from.x, b.z - from.z);
    const peak = 1.6 + dist * 0.16;
    const arc = (s) => ({
      x: from.x + (b.x - from.x) * s,
      y: 1.2 + 4 * peak * s * (1 - s),
      z: from.z + (b.z - from.z) * s,
    });
    const flown = [];
    for (let i = 0; i <= 24; i++) flown.push(arc((i / 24) * t));
    poly3(flown, 'rgba(255,216,102,0.45)', null, 1.5);
    const rest = [];
    for (let i = 0; i <= 12; i++) rest.push(arc(t + ((1 - t) * i) / 12));
    poly3(rest, 'rgba(255,216,102,0.14)', null, 1, [4, 4]);
    const p = arc(t);
    const s0 = proj(p.x, p.y, p.z);
    ctx.beginPath(); ctx.arc(s0[0], s0[1], 0.28 * S * zoom, 0, Math.PI * 2);
    ctx.fillStyle = '#2b3145'; ctx.fill();
    ctx.strokeStyle = '#0d0f16'; ctx.lineWidth = 1.5; ctx.stroke();
    dot3({ x: p.x, y: p.y + 0.32, z: p.z }, `rgba(255,${Math.round(200 * (b.fuse / b.total))},90,0.95)`, 2.2);
    // 引信环：剩余时间越少，环越短越红
    ctx.beginPath();
    ctx.arc(s0[0], s0[1], 0.5 * S * zoom, -Math.PI / 2, -Math.PI / 2 + Math.PI * 2 * (b.fuse / b.total));
    ctx.strokeStyle = `rgba(255,${Math.round(90 + 120 * (1 - b.fuse / b.total))},60,0.9)`;
    ctx.lineWidth = 2; ctx.stroke();
  }

  // 命中红点：爆炸时引擎返回的目标表面点（存的是相对目标的偏移，跟着目标走）
  for (const m of st.marks) {
    const s = proj(st.tx[m.index] + m.dx, (st.ty[m.index] || 0) + m.dy, st.tz[m.index] + m.dz);
    const rad = Math.max(5, 0.5 * S * zoom) * (0.55 + 0.45 * m.life);
    const g = ctx.createRadialGradient(s[0], s[1], 0, s[0], s[1], rad);
    g.addColorStop(0, `rgba(255,60,50,${0.95 * m.life})`);
    g.addColorStop(0.6, `rgba(214,32,32,${0.8 * m.life})`);
    g.addColorStop(1, 'rgba(160,20,20,0)');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(s[0], s[1], rad, 0, Math.PI * 2); ctx.fill();
  }
}

function updateHUD() {
  const warn = st.wasmMissing ? '<div class="hit"><b>WASM 未就绪</b>：请强制刷新</div>' : '';
  const aimTxt = st.aim && st.aim.ok
    ? `<b>(${st.aim.x.toFixed(2)}, ${st.aim.z.toFixed(2)})</b>　圈内候选 ${st.aim.candidates ?? 0}`
    : '指针不在场地上';
  const last = st.lastBoom;
  const hits = last && last.hits.length
    ? last.hits
      .slice()
      .sort((a, b) => b.depth - a.depth)
      .map((h) => `<span class="row">#${h.index} 深度=${h.depth.toFixed(2)} 强度=${h.intensity.toFixed(2)} 起飞=${h.lift.toFixed(1)}m/s</span>`)
      .join('　')
    : '<span style="color:#8b93a7">无</span>';
  const airborne = st.ty.filter((v) => v > 0.01).length;
  document.getElementById('hud').innerHTML = warn +
    `<div>落点 ${aimTxt}　飞行中炸弹 ${st.shots.length} 颗</div>` +
    `<div>半径 <b>${st.radius.toFixed(1)}</b>　引信 ${st.fuse.toFixed(2)}s　击退 ${st.power.toFixed(1)} m/s　` +
    `爆炸查询 ${st.stepMs.toFixed(2)}ms（瞄准 ${st.aimMs.toFixed(2)}ms）</div>` +
    `<div>爆炸 <b>${st.explosions}</b> 次　命中 <span class="hit">${st.hitTotal}</span> 人次　` +
    `此刻闪红 ${st.flash.filter((f) => f > 0.05).length} 个　在空中 <b>${airborne}</b> 个</div>` +
    `<div style="margin-top:4px">上一次爆炸命中（穿透深度 / 强度 / 起飞速度，均为碰撞查询返回值）：${hits}</div>` +
    '<small>红 = 闪红强度（= 穿透深度 ÷ 半径，钳到 1）· 命中者按起飞速度离地翻滚、落地回正（地面阴影 = 离地高度）· ' +
    '黄圈 = 鼠标指的地面落点与爆炸球 · 冲击波 = 刚爆的那一颗 · ' +
    '拖动转视角 · 滚轮缩放 · 空格往落点丢一颗</small>';
}

// ---- 交互 ----
let dragging = false, moved = 0, lastPt = [0, 0];
canvas.addEventListener('pointerdown', (e) => {
  dragging = true; moved = 0; lastPt = [e.clientX, e.clientY];
  st.mouse = [e.clientX, e.clientY];
  aim();
  canvas.setPointerCapture(e.pointerId);
});
canvas.addEventListener('pointermove', (e) => {
  st.mouse = [e.clientX, e.clientY];
  if (dragging) {
    const dx = e.clientX - lastPt[0], dy = e.clientY - lastPt[1];
    moved += Math.abs(dx) + Math.abs(dy);
    yaw -= dx * 0.01;
    pitch = Math.max(PITCH_MIN, Math.min(PITCH_MAX, pitch + dy * 0.01));
    lastPt = [e.clientX, e.clientY];
    updateCam();
  }
  aim();
});
canvas.addEventListener('pointerleave', () => { st.mouse = null; st.aim = null; });
canvas.addEventListener('pointerup', (e) => {
  dragging = false;
  if (moved < 6) throwBomb(e.clientX, e.clientY); // 没怎么拖 = 点击 → 丢炸弹
});
canvas.addEventListener('wheel', (e) => {
  e.preventDefault();
  zoom = Math.max(0.3, Math.min(4, zoom * Math.exp(-e.deltaY * 0.0015)));
  aim();
}, { passive: false });
window.addEventListener('keydown', (e) => {
  if (e.code === 'Space') {
    e.preventDefault();
    if (st.mouse) throwBomb(st.mouse[0], st.mouse[1]);
  }
});

document.getElementById('rad').addEventListener('input', (e) => {
  st.radius = Number(e.target.value);
  document.getElementById('radv').textContent = st.radius.toFixed(1);
  aim();
});
document.getElementById('fuse').addEventListener('input', (e) => {
  st.fuse = Number(e.target.value);
  document.getElementById('fusev').textContent = st.fuse.toFixed(2);
});
document.getElementById('pow').addEventListener('input', (e) => {
  st.power = Number(e.target.value);
  document.getElementById('powv').textContent = st.power.toFixed(1);
});
document.getElementById('auto').addEventListener('click', (e) => {
  st.auto = !st.auto;
  st.autoTimer = 0;
  e.target.classList.toggle('on', st.auto);
});
document.getElementById('pause').addEventListener('click', (e) => {
  st.paused = !st.paused;
  e.target.textContent = st.paused ? '继续' : '暂停';
});
document.getElementById('reset').addEventListener('click', () => setup());
window.addEventListener('resize', () => { resize(); aim(); });

let last = performance.now();
function frame(now) {
  const dt = Math.min(0.05, (now - last) / 1000);
  last = now;
  if (st.auto && !st.paused) {
    st.autoTimer -= dt;
    if (st.autoTimer <= 0) { autoThrow(); st.autoTimer = AUTO_PERIOD; }
  }
  stepSim(dt);
  for (const bm of st.booms) bm.life = Math.max(0, bm.life - dt * 1.6);
  st.booms = st.booms.filter((bm) => bm.life > 0);
  for (const m of st.marks) m.life = Math.max(0, m.life - dt * 0.6);
  st.marks = st.marks.filter((m) => m.life > 0);
  render();
  updateHUD();
  requestAnimationFrame(frame);
}

(async function () {
  const go = new Go();
  const buf = await (await fetch('collide.wasm?v=10')).arrayBuffer();
  const mod = await WebAssembly.instantiate(buf, go.importObject);
  go.run(mod.instance);
  resize();
  syncControls();
  document.getElementById('auto').classList.toggle('on', st.auto);
  setup();
  st.ready = true;
  warmupFrames();
  requestAnimationFrame(frame);
})();
