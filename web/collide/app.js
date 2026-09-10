'use strict';

// ---- 轨道相机（正交投影，可旋转）----
const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
let S = 62;            // 每世界单位像素（resize 时重算）
let zoom = 1;          // 滚轮缩放
let ox = 0, oy = 0;    // 屏幕原点（世界原点投影到这里）
let yaw = 0.7;         // 绕 Y 轴旋转
let pitch = 0.95;      // 俯仰：0 = 水平，π/2 = 俯视
let topDown = false;
const PITCH_MIN = 0.30, PITCH_MAX = Math.PI / 2 - 0.01;

let cam = { r: { X: 1, Y: 0, Z: 0 }, u: { X: 0, Y: 1, Z: 0 } };
function updateCam() {
  const cp = Math.cos(pitch), sp = Math.sin(pitch), cy = Math.cos(yaw), sy = Math.sin(yaw);
  const eye = { X: sy * cp, Y: sp, Z: cy * cp };   // 从原点指向相机的方向
  let r = { X: eye.Z, Y: 0, Z: -eye.X };           // right = normalize(up × eye)
  const rl = Math.hypot(r.X, r.Z) || 1;
  r = { X: r.X / rl, Y: 0, Z: r.Z / rl };
  const u = {                                      // up = eye × right
    X: eye.Y * r.Z - eye.Z * r.Y,
    Y: eye.Z * r.X - eye.X * r.Z,
    Z: eye.X * r.Y - eye.Y * r.X,
  };
  cam = { r, u };
}

// 正交投影：球投影成圆、胶囊投影成等半径胶囊，几何与碰撞计算一致。
function proj(x, y, z) {
  const k = S * zoom;
  return [ox + (cam.r.X * x + cam.r.Y * y + cam.r.Z * z) * k,
          oy - (cam.u.X * x + cam.u.Y * y + cam.u.Z * z) * k];
}

// 已知高度 y 时，由屏幕坐标反解平面上的 (x,z)；平面接近侧视时返回 null。
function unproj(mx, my, y) {
  const k = S * zoom;
  const a1 = cam.r.X, b1 = cam.r.Z, c1 = (mx - ox) / k - cam.r.Y * y;
  const a2 = cam.u.X, b2 = cam.u.Z, c2 = (oy - my) / k - cam.u.Y * y;
  const det = a1 * b2 - a2 * b1;
  if (Math.abs(det) < 0.05) return null;
  return [(c1 * b2 - c2 * b1) / det, (a1 * c2 - a2 * c1) / det];
}

function resize() {
  const r = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(1, Math.round(r.width * dpr));
  canvas.height = Math.max(1, Math.round(r.height * dpr));
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.lineCap = 'round';   // 胶囊等以粗线绘制时需要圆头
  ctx.lineJoin = 'round';
  ox = r.width * 0.5;
  oy = r.height * 0.66;
  S = Math.min(r.width, r.height) / 9;
  updateCam();
  render();
}

// ---- 场景与状态 ----
const MODES = [
  ['pointSegment',  '点–线段 3D'],
  ['pointTriangle', '点–三角形 3D'],
  ['pointOBB',      '点–OBB'],
  ['capsuleCapsule','胶囊–胶囊'],
  ['sphereAABB',    '球–AABB'],
  ['sphereOBB',     '球–OBB'],
];
// 每个用例的默认高度
const MODE_DEFAULT_H = {
  pointSegment: 0.8,
  pointTriangle: 0.9,
  pointOBB: 1.6,
  capsuleCapsule: 0.6,
  sphereAABB: 0.6,
  sphereOBB: 0.6,
};
// 全部默认 3D 视角（需要精确校验平面关系时再手动切俯视）
const MODE_DEFAULT_TOPDOWN = {
  pointSegment: false,
  pointTriangle: false,
  pointOBB: false,
  capsuleCapsule: false,
  sphereAABB: false,
  sphereOBB: false,
};
const state = {
  mode: 'pointSegment',
  anchor: { x: 0.6, z: 0.4 },  // 可拖拽对象的 x/z
  height: 0,                   // 可拖拽对象的 y
  dragging: false,
};

let lastResult = {};

// Go 侧返回的向量是小写 x/y/z，统一成绘图用的 X/Y/Z。
function V(p) {
  if (!p) return null;
  return { X: p.x ?? p.X, Y: p.y ?? p.Y, Z: p.z ?? p.Z };
}

// 用 yaw+pitch 构造一个正交归一的 OBB 朝向
function makeOBB(center, yawDeg, pitchDeg, e) {
  const y = yawDeg * Math.PI / 180, p = pitchDeg * Math.PI / 180;
  const cy = Math.cos(y), sy = Math.sin(y), cp = Math.cos(p), sp = Math.sin(p);
  return {
    c: center,
    u: [
      { X: cy, Y: 0, Z: -sy },
      { X: sy * sp, Y: cp, Z: cy * sp },
      { X: sy * cp, Y: -sp, Z: cy * cp },
    ],
    e,
  };
}
const BOX_EDGES = [[0,1],[2,3],[4,5],[6,7],[0,2],[1,3],[4,6],[5,7],[0,4],[1,5],[2,6],[3,7]];

function buildScene() {
  const { mode, anchor, height: y } = state;
  const a = anchor;
  if (mode === 'pointSegment') {
    // 空间斜置线段（不在地面上）
    const seg = [{ X: -2.6, Y: 1.8, Z: -1.8 }, { X: 2.6, Y: -0.6, Z: 1.8 }];
    return { scene: { mode, point: { X: a.x, Y: y, Z: a.z }, segment: seg },
             shapes: [{ type: 'segment', a: seg[0], b: seg[1] }] };
  }
  if (mode === 'pointTriangle') {
    // 空间斜平面三角形
    const t = [{ X: -2.4, Y: 0.0, Z: -1.6 }, { X: 2.4, Y: 1.4, Z: -0.8 }, { X: 0.0, Y: 0.3, Z: 2.2 }];
    return { scene: { mode, point: { X: a.x, Y: y, Z: a.z }, triangle: t },
             shapes: [{ type: 'triangle', a: t[0], b: t[1], c: t[2] }] };
  }
  if (mode === 'pointOBB') {
    const o = makeOBB({ X: 0, Y: 0.6, Z: 0 }, 35, 18, [1.1, 0.55, 0.75]);
    return { scene: { mode, point: { X: a.x, Y: y, Z: a.z }, obb: o },
             shapes: [{ type: 'obb', obb: o }] };
  }
  if (mode === 'capsuleCapsule') {
    // 两条空间斜置胶囊
    const A = { a: { X: a.x, Y: y, Z: a.z }, b: { X: a.x + 0.8, Y: y + 1.5, Z: a.z + 0.6 }, r: 0.35 };
    const B = { a: { X: 1.2, Y: 0.3, Z: -0.6 }, b: { X: 1.9, Y: 1.8, Z: 0.2 }, r: 0.5 };
    return { scene: { mode, capsuleA: A, capsuleB: B },
             shapes: [{ type: 'capsule', a: A.a, b: A.b, r: A.r }, { type: 'capsule', a: B.a, b: B.b, r: B.r }] };
  }
  if (mode === 'sphereAABB') {
    const box = { min: { X: -1.0, Y: 0, Z: -1.0 }, max: { X: 1.0, Y: 1.3, Z: 1.0 } };
    const s = { c: { X: a.x, Y: y, Z: a.z }, r: 0.6 };
    return { scene: { mode, sphere: s, aabb: box },
             shapes: [{ type: 'box', min: box.min, max: box.max }, { type: 'sphere', c: s.c, r: s.r }] };
  }
  if (mode === 'sphereOBB') {
    const o = makeOBB({ X: 0, Y: 0.6, Z: 0 }, 35, 18, [1.1, 0.55, 0.75]);
    const s = { c: { X: a.x, Y: y, Z: a.z }, r: 0.6 };
    return { scene: { mode, sphere: s, obb: o },
             shapes: [{ type: 'obb', obb: o }, { type: 'sphere', c: s.c, r: s.r }] };
  }
  return null;
}

function drawBoxFaces(corner) {
  BOX_EDGES.forEach(([i, j]) => line(corner(i), corner(j), '#6ea8fe', 2));
  for (let i = 0; i < 8; i++) drop(corner(i), '#2c3348');
}
function obbCorner(o) {
  return (i) => {
    const sx = (i & 1) ? 1 : -1, sy = (i & 2) ? 1 : -1, sz = (i & 4) ? 1 : -1;
    return {
      X: o.c.X + sx * o.e[0] * o.u[0].X + sy * o.e[1] * o.u[1].X + sz * o.e[2] * o.u[2].X,
      Y: o.c.Y + sx * o.e[0] * o.u[0].Y + sy * o.e[1] * o.u[1].Y + sz * o.e[2] * o.u[2].Y,
      Z: o.c.Z + sx * o.e[0] * o.u[0].Z + sy * o.e[1] * o.u[1].Z + sz * o.e[2] * o.u[2].Z,
    };
  };
}

// ---- 绘图工具 ----
function line(p, q, color, w, dash) {
  ctx.save();
  if (dash) ctx.setLineDash(dash);
  ctx.strokeStyle = color; ctx.lineWidth = w || 1;
  ctx.beginPath();
  ctx.moveTo(...proj(p.X, p.Y, p.Z));
  ctx.lineTo(...proj(q.X, q.Y, q.Z));
  ctx.stroke();
  ctx.restore();
}
function dot(p, color, r, label) {
  const [sx, sy] = proj(p.X, p.Y, p.Z);
  ctx.fillStyle = color;
  ctx.beginPath(); ctx.arc(sx, sy, r || 4, 0, Math.PI * 2); ctx.fill();
  if (label) { ctx.fillStyle = color; ctx.font = '12px sans-serif'; ctx.fillText(label, sx + 7, sy - 7); }
}
function drop(p, color) { line(p, { X: p.X, Y: 0, Z: p.Z }, color || '#39415a', 1, [3, 4]); }

function drawGrid() {
  const N = 4;
  for (let i = -N; i <= N; i++) {
    line({ X: i, Y: 0, Z: -N }, { X: i, Y: 0, Z: N }, '#232838', 1);
    line({ X: -N, Y: 0, Z: i }, { X: N, Y: 0, Z: i }, '#232838', 1);
  }
}

function drawShape(sh) {
  if (sh.type === 'segment') {
    drop(sh.a); drop(sh.b);
    line(sh.a, sh.b, '#6ea8fe', 3);
  } else if (sh.type === 'triangle') {
    [sh.a, sh.b, sh.c].forEach(p => drop(p));
    ctx.fillStyle = 'rgba(110,168,254,0.18)';
    ctx.strokeStyle = '#6ea8fe'; ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.moveTo(...proj(sh.a.X, sh.a.Y, sh.a.Z));
    ctx.lineTo(...proj(sh.b.X, sh.b.Y, sh.b.Z));
    ctx.lineTo(...proj(sh.c.X, sh.c.Y, sh.c.Z));
    ctx.closePath(); ctx.fill(); ctx.stroke();
  } else if (sh.type === 'box') {
    const { min, max } = sh;
    const P = (i) => ({ X: (i & 1) ? max.X : min.X, Y: (i & 2) ? max.Y : min.Y, Z: (i & 4) ? max.Z : min.Z });
    drawBoxFaces(P);
  } else if (sh.type === 'obb') {
    drawBoxFaces(obbCorner(sh.obb));
  } else if (sh.type === 'sphere') {
    drop(sh.c);
    const [sx, sy] = proj(sh.c.X, sh.c.Y, sh.c.Z);
    ctx.fillStyle = 'rgba(127,209,139,0.18)';
    ctx.strokeStyle = '#7fd18b'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(sx, sy, sh.r * S, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
    dot(sh.c, '#7fd18b', 3);
  } else if (sh.type === 'capsule') {
    drop(sh.a); drop(sh.b);
    line(sh.a, sh.b, '#ffa657', 2 * sh.r * S);   // 直径，圆头
    [sh.a, sh.b].forEach(p => {
      const [sx, sy] = proj(p.X, p.Y, p.Z);
      ctx.strokeStyle = '#ffa657'; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(sx, sy, sh.r * S, 0, Math.PI * 2); ctx.stroke();
    });
  }
}

function render() {
  const r = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, r.width, r.height);
  drawGrid();
  const built = buildScene();
  if (!built) return;
  built.shapes.forEach(drawShape);

  const res = lastResult;
  const c1 = V(res.closest), c2 = V(res.closest2);
  const overlap = !!res.overlap;
  const col = overlap ? '#ff4d4d' : '#ffd866';
  if (c1) dot(c1, col, overlap ? 6 : 5, overlap ? '接触' : '最近点');
  if (c2) dot(c2, col, overlap ? 6 : 5);
  if (c1 && c2 && (overlap || (res.dist ?? 0) > 1e-9)) {
    line(c1, c2, col, overlap ? 3 : 1.5, overlap ? [] : [4, 4]);
  }
  if (overlap && c1) { // 重叠：再套一个红圈，一眼能看出碰撞
    const [sx, sy] = proj(c1.X, c1.Y, c1.Z);
    ctx.strokeStyle = '#ff4d4d'; ctx.lineWidth = 2;
    ctx.beginPath(); ctx.arc(sx, sy, 15, 0, Math.PI * 2); ctx.stroke();
  }
  // 接触法向（由 Go 的 Contact 给出）：从接触点沿法向画红色箭头
  const ct = res.contact;
  if (ct) {
    const cp = V(ct.point), cn = V(ct.normal);
    const len = Math.max(0.35, Math.min(2.0, (ct.depth ?? 0) * 4));
    const tip = { X: cp.X + cn.X * len, Y: cp.Y + cn.Y * len, Z: cp.Z + cn.Z * len };
    line(cp, tip, '#ff4d4d', 2);
    dot(tip, '#ff4d4d', 3.5);
  }

  dot({ X: state.anchor.x, Y: state.height, Z: state.anchor.z }, '#5ee1ff', 6, '拖动我');

  const hud = document.getElementById('hud');
  const isOverlapMode = state.mode === 'capsuleCapsule' || state.mode === 'sphereAABB' || state.mode === 'sphereOBB';
  const ov = isOverlapMode
    ? (res.overlap ? '<b>重叠 / 命中</b>' : '<span class="no">分离</span>')
    : ((res.dist ?? 0) < 1e-9 ? '<b>点在形状上</b>' : '<span class="no">点在形状外</span>');
  hud.innerHTML =
    `<div>模式：${state.mode}</div>` +
    `<div>状态：${ov}</div>` +
    `<div>距离：${(res.dist ?? 0).toFixed(3)}（平方 ${(res.distSq ?? 0).toFixed(3)}）</div>` +
    (ct ? `<div>穿透深度：${ct.depth.toFixed(3)}　法向（B→A）：(${V(ct.normal).X.toFixed(2)}, ${V(ct.normal).Y.toFixed(2)}, ${V(ct.normal).Z.toFixed(2)})</div>` : '') +
    (c1 ? `<div>最近点：(${c1.X.toFixed(2)}, ${c1.Y.toFixed(2)}, ${c1.Z.toFixed(2)})</div>` : '') +
    `<small>拖空白处旋转 · 滚轮缩放 · 拖红点移动 · 滑杆调高度；结果由 Go (WASM) 计算` +
    (topDown ? '；俯视下高度不影响画面、只改距离' : '') + `</small>`;
}

function evalScene() {
  const built = buildScene();
  if (!built) return;
  try {
    lastResult = JSON.parse(window.collideEval(JSON.stringify(built.scene)));
  } catch (e) {
    lastResult = { error: String(e) };
  }
  render();
}

// ---- 交互：左键拖红点移动；空白处拖动旋转；滚轮缩放 ----
function pick(ev) {
  const r = canvas.getBoundingClientRect();
  return [ev.clientX - r.left, ev.clientY - r.top];
}
let dragMode = null;   // 'move' | 'orbit'
let lastPt = [0, 0];

function handleScreen() { return proj(state.anchor.x, state.height, state.anchor.z); }
function moveTo(mx, my) {
  const r = unproj(mx, my, state.height);
  if (!r) return;
  state.anchor.x = Math.max(-6, Math.min(6, r[0]));
  state.anchor.z = Math.max(-6, Math.min(6, r[1]));
  evalScene();
}
canvas.addEventListener('pointerdown', (ev) => {
  const [mx, my] = pick(ev);
  const [hx, hy] = handleScreen();
  dragMode = Math.hypot(mx - hx, my - hy) < 16 ? 'move' : 'orbit';
  lastPt = [mx, my];
  canvas.setPointerCapture(ev.pointerId);
  if (dragMode === 'move') moveTo(mx, my);
});
canvas.addEventListener('pointermove', (ev) => {
  const [mx, my] = pick(ev);
  if (!dragMode) {
    const [hx, hy] = handleScreen();
    canvas.style.cursor = Math.hypot(mx - hx, my - hy) < 16 ? 'grab' : 'move';
    return;
  }
  if (dragMode === 'move') {
    moveTo(mx, my);
  } else {
    yaw -= (mx - lastPt[0]) * 0.01;
    pitch = Math.max(PITCH_MIN, Math.min(PITCH_MAX, pitch + (my - lastPt[1]) * 0.01));
    updateCam();
    render();
  }
  lastPt = [mx, my];
});
canvas.addEventListener('pointerup', () => { dragMode = null; });
canvas.addEventListener('wheel', (ev) => {
  ev.preventDefault();
  zoom = Math.max(0.4, Math.min(4, zoom * Math.exp(-ev.deltaY * 0.0015)));
  render();
}, { passive: false });

document.getElementById('h').addEventListener('input', (ev) => {
  state.height = parseFloat(ev.target.value);
  document.getElementById('hv').textContent = state.height.toFixed(2);
  evalScene();
});
document.getElementById('reset').addEventListener('click', () => {
  state.anchor = { x: 0.6, z: 0.4 };
  state.height = MODE_DEFAULT_H[state.mode];
  document.getElementById('h').value = String(state.height);
  document.getElementById('hv').textContent = state.height.toFixed(2);
  setView(MODE_DEFAULT_TOPDOWN[state.mode]);
  evalScene();
});

function setView(isTopDown) {
  topDown = isTopDown;
  if (topDown) {
    pitch = PITCH_MAX;
    yaw = 0;
  } else {
    pitch = 0.95;
    yaw = 0.7;
  }
  updateCam();
  document.getElementById('view').textContent = topDown ? '视图：俯视' : '视图：3D';
}

document.getElementById('view').addEventListener('click', () => {
  setView(!topDown);
  evalScene();
});

const modesEl = document.getElementById('modes');
MODES.forEach(([id, label]) => {
  const b = document.createElement('button');
  b.textContent = label;
  b.className = id === state.mode ? 'on' : '';
  b.onclick = () => {
    state.mode = id;
    [...modesEl.children].forEach(c => c.className = '');
    b.className = 'on';
    state.height = MODE_DEFAULT_H[id];
    document.getElementById('h').value = String(state.height);
    document.getElementById('hv').textContent = state.height.toFixed(2);
    setView(MODE_DEFAULT_TOPDOWN[id]);
    evalScene();
  };
  modesEl.appendChild(b);
});

window.addEventListener('resize', resize);

// 同步滑杆到当前模式的默认高度
(function initHeight() {
  const h = MODE_DEFAULT_H[state.mode];
  state.height = h;
  document.getElementById('h').value = String(h);
  document.getElementById('hv').textContent = h.toFixed(2);
  setView(MODE_DEFAULT_TOPDOWN[state.mode]);
})();

// ---- 启动 WASM ----
(async function () {
  const go = new Go();
  const buf = await (await fetch('collide.wasm?v=7')).arrayBuffer();
  const mod = await WebAssembly.instantiate(buf, go.importObject);
  go.run(mod.instance);   // main 里 select{}，不会返回
  resize();
  evalScene();
})();
