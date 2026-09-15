'use strict';

// Boss 行为树演示页：跑的是**真实世界**。
//
// 与 collidewasm 那些页面不同，这一页的 Go 侧不是"算一次几何"，而是
// 一个真实的 ecs.World + 真实系统装配 + 真实行为树：浏览器每帧调
// bossStep(n) 推进模拟，再拿快照回来画。所以画面上看到的行为，
// 就是服务器里 Boss 的行为。
//
// 渲染约定沿用 blast.js：正交相机 + 手动透视投影（proj 把游戏坐标
// x/y 映射到屏幕，y 轴抬高表示高度）。

const canvas = document.getElementById('c');
const ctx = canvas.getContext('2d');
const errBox = document.getElementById('err');

let S = 62, zoom = 1, ox = 0, oy = 0;
let yaw = 2.35, pitch = 0.62;
const PITCH_MIN = 0.2, PITCH_MAX = Math.PI / 2 - 0.01;

let cam = { r: { X: 1, Y: 0, Z: 0 }, u: { X: 0, Y: 1, Z: 0 } };

function updateCam() {
  const cp = Math.cos(pitch), sp = Math.sin(pitch), cy = Math.cos(yaw), sy = Math.sin(yaw);
  const eye = { X: sy * cp, Y: sp, Z: cy * cp };
  let r = { X: eye.Z, Z: -eye.X };
  const rl = Math.hypot(r.X, r.Z) || 1;
  r = { X: r.X / rl, Z: r.Z / rl };
  const u = {
    X: eye.Y * r.Z,
    Y: eye.Z * r.X - eye.X * r.Z,
    Z: -eye.Y * r.X,
  };
  cam = { r, u };
}
window.updateCam = updateCam;

// proj：3D 点（世界 x, 高度 y, 世界 z）→ 屏幕坐标
function proj(x, y, z) {
  const k = S * zoom;
  return [
    ox + (cam.r.X * x + cam.r.Z * z) * k,
    oy - (cam.u.X * x + cam.u.Y * y + cam.u.Z * z) * k,
  ];
}

function resize() {
  const rect = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(1, Math.round(rect.width * dpr));
  canvas.height = Math.max(1, Math.round(rect.height * dpr));
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.lineCap = 'round';
  ox = rect.width * 0.5;
  oy = rect.height * 0.58;
  S = Math.min(rect.width, rect.height) / 22;
  updateCam();
}

// ---- 状态 ----
const st = {
  paused: false,
  follow: true,
  snapshot: null,
  tree: null,
  wasmMissing: false,
  ground: [],   // 网格线（预生成）
  drag: null,
  lastMove: 0,
};

const FIELD = 16;   // 场地半边长（与 Go 侧 demoFieldHalf 一致）
const ORIGIN = 32;  // 场地中心坐标（与 Go 侧 demoOrigin 一致）

// 游戏坐标 → 渲染坐标：把场地中心平移到原点。
// Go 侧必须用正坐标（AOI 网格按 y*Width+x 索引，负坐标会被跳过），
// 渲染这边想以 (0,0) 为中心画，所以减掉 ORIGIN。
function gx(v) { return v - ORIGIN; }

// 预生成地面网格
for (let i = -FIELD; i <= FIELD; i++) {
  st.ground.push([[i, -FIELD], [i, FIELD]]);
  st.ground.push([[-FIELD, i], [FIELD, i]]);
}

// ---- 输入 ----
let dragging = false, lastX = 0, lastY = 0;
canvas.addEventListener('mousedown', (e) => {
  dragging = true; lastX = e.clientX; lastY = e.clientY;
  canvas.classList.add('drag');
});
window.addEventListener('mouseup', () => { dragging = false; canvas.classList.remove('drag'); });
window.addEventListener('mousemove', (e) => {
  if (!dragging) return;
  const dx = e.clientX - lastX, dy = e.clientY - lastY;
  lastX = e.clientX; lastY = e.clientY;
  yaw -= dx * 0.008;
  pitch = Math.max(PITCH_MIN, Math.min(PITCH_MAX, pitch + dy * 0.006));
  updateCam();
});
canvas.addEventListener('wheel', (e) => {
  e.preventDefault();
  zoom = Math.max(0.3, Math.min(4, zoom * (e.deltaY > 0 ? 0.92 : 1.08)));
}, { passive: false });

// 点地面 = 把玩家移过去（用地面平面的反投影）
canvas.addEventListener('click', (e) => {
  if (!st.snapshot) return;
  const rect = canvas.getBoundingClientRect();
  const sx = e.clientX - rect.left, sy = e.clientY - rect.top;
  const x = (sx - ox) / (S * zoom);
  const y = (sy - oy) / (S * zoom);
  // 在地面平面（高度 = 0）上反解：屏幕 y 来自 cam.u 的组合，这里用
  // 简化逆投影——地面点只依赖水平方向，所以按相机右向量的分量还原。
  let wx = (cam.r.X * x - cam.u.X * y) / (cam.r.X * cam.r.X + cam.u.X * cam.u.X);
  let wz = (cam.r.Z * x - cam.u.Z * y) / (cam.r.Z * cam.r.Z + cam.u.Z * cam.u.Z);
  wx = Math.max(-FIELD, Math.min(FIELD, wx));
  wz = Math.max(-FIELD, Math.min(FIELD, wz));
  st.follow = false;
  document.getElementById('follow').classList.remove('on');
  // 还原成游戏坐标（加回 ORIGIN）
  if (globalThis.bossMovePlayer) {
    globalThis.bossMovePlayer(Math.round(wx) + ORIGIN, Math.round(wz) + ORIGIN);
  }
});

document.getElementById('hit').addEventListener('click', () => {
  if (globalThis.bossDamage) st.snapshot = normalize(JSON.parse(globalThis.bossDamage(25)));
});
document.getElementById('burst').addEventListener('click', () => {
  // 直接打到阈值以下（观察阶段切换）
  const snap = st.snapshot;
  if (!snap) return;
  const need = snap.boss.hp - 200 + 1;
  if (need > 0 && globalThis.bossDamage) st.snapshot = normalize(JSON.parse(globalThis.bossDamage(need)));
});
document.getElementById('pause').addEventListener('click', (e) => {
  st.paused = !st.paused;
  e.target.classList.toggle('on', st.paused);
  e.target.textContent = st.paused ? '继续' : '暂停';
});
document.getElementById('reset').addEventListener('click', () => {
  if (globalThis.bossReset) st.snapshot = normalize(JSON.parse(globalThis.bossReset()));
});
document.getElementById('follow').addEventListener('click', (e) => {
  st.follow = !st.follow;
  e.target.classList.toggle('on', st.follow);
});

// ---- 绘制 ----
const ACT_LABEL = {
  '': '—', roar: '嚎叫', leap: '闪现突进', slam: '锤地 AOE', bomb: '投掷炸弹',
};
const AI_STATE = ['待机', '追击', '攻击', '逃跑'];

function drawGround() {
  ctx.strokeStyle = '#232733';
  ctx.lineWidth = 1;
  ctx.beginPath();
  for (const [[x1, z1], [x2, z2]] of st.ground) {
    const [sx1, sy1] = proj(x1, 0, z1);
    const [sx2, sy2] = proj(x2, 0, z2);
    ctx.moveTo(sx1, sy1); ctx.lineTo(sx2, sy2);
  }
  ctx.stroke();
}

// 画一个"人形"：地面圆 + 竖直胶囊
function drawFigure(x, z, radius, height, bodyColor, headColor) {
  const [bx, by] = proj(gx(x), 0, gx(z));
  const [tx, ty] = proj(gx(x), height, gx(z));
  // 身体
  ctx.strokeStyle = bodyColor;
  ctx.lineWidth = Math.max(3, radius * 2 * S * zoom);
  ctx.beginPath(); ctx.moveTo(bx, by); ctx.lineTo(tx, ty); ctx.stroke();
  // 头
  ctx.fillStyle = headColor;
  ctx.beginPath();
  ctx.arc(tx, ty, Math.max(3, radius * S * zoom * 0.9), 0, Math.PI * 2);
  ctx.fill();
  // 地面投影圈
  ctx.strokeStyle = 'rgba(255,255,255,.18)';
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.ellipse(bx, by, radius * S * zoom, radius * S * zoom * 0.45, 0, 0, Math.PI * 2);
  ctx.stroke();
}

function draw() {
  const rect = canvas.getBoundingClientRect();
  ctx.clearRect(0, 0, rect.width, rect.height);
  drawGround();

  const snap = st.snapshot;
  if (!snap) return;

  const boss = snap.boss, player = snap.player;

  // 爆炸圈（先画，压在地面下层次）
  for (const b of snap.blasts) {
    const t = Math.max(0, 1 - b.age / b.life);
    const r = b.radius * (0.5 + 0.5 * (1 - t) * 1.6);
    const [cx, cy] = proj(gx(b.x), 0.05, gx(b.y));
    ctx.strokeStyle = `rgba(255,140,60,${0.85 * t})`;
    ctx.lineWidth = 3;
    ctx.beginPath();
    ctx.ellipse(cx, cy, r * S * zoom, r * S * zoom * 0.45, 0, 0, Math.PI * 2);
    ctx.stroke();
    ctx.fillStyle = `rgba(255,90,40,${0.16 * t})`;
    ctx.fill();
  }

  // 炸弹
  for (const b of snap.bombs) {
    const [bx, by] = proj(gx(b.x), 0.35, gx(b.y));
    const pulse = 1 + 0.25 * Math.sin(b.age * 22);
    ctx.fillStyle = '#ffd866';
    ctx.beginPath(); ctx.arc(bx, by, 6 * pulse, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = `rgba(255,216,102,${0.7 * (1 - b.age / b.fuse)})`;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(bx, by, 12 + 10 * (b.age / b.fuse), 0, Math.PI * 2);
    ctx.stroke();
  }

  // 玩家
  drawFigure(player.x, player.y, 0.4, 1.8, '#5b8def', '#a8c7ff');
  // Boss（二阶段换色 + 更大）
  const phase2 = boss.phase === 2;
  const bodyColor = phase2 ? '#c0504d' : '#8a6d3b';
  const headColor = phase2 ? '#ff8a80' : '#d9b36c';
  drawFigure(boss.x, boss.y, 0.55, 2.4, bodyColor, headColor);

  // 二阶段：Boss 身上加一圈脉动光环，直观区分阶段
  if (phase2) {
    const [cx, cy] = proj(gx(boss.x), 0.05, gx(boss.y));
    const pulse = 1 + 0.08 * Math.sin(performance.now() / 160);
    ctx.strokeStyle = 'rgba(255,120,100,.5)';
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.ellipse(cx, cy, 1.1 * S * zoom * pulse, 1.1 * S * zoom * pulse * 0.45, 0, 0, Math.PI * 2);
    ctx.stroke();
  }

  // 连击提示：Boss 正在出拳时头顶画一个拳头标记
  if (boss.punching) {
    const [hx, hy] = proj(gx(boss.x), 2.9, gx(boss.y));
    ctx.fillStyle = '#ff8a80';
    ctx.font = 'bold 15px sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText('✊', hx, hy);
  }
}

// ---- HUD ----
let logLen = 0;
function updateHUD() {
  const snap = st.snapshot;
  if (!snap) return;
  const b = snap.boss, p = snap.player;

  const phaseEl = document.getElementById('phase');
  if (b.phase === 2) {
    phaseEl.textContent = '二阶段 · 嚎叫/闪现/三拳一砸';
    phaseEl.className = 'phase p2';
  } else {
    phaseEl.textContent = '一阶段 · 投弹';
    phaseEl.className = 'phase p1';
  }
  document.getElementById('hp').textContent = `${b.hp} / ${b.maxHp}`;
  document.getElementById('hpbar').style.width = `${(b.hp / b.maxHp) * 100}%`;
  document.getElementById('aistate').textContent =
    `${AI_STATE[b.state] || b.state}${b.punching ? ' · 出拳中' : ''}`;
  document.getElementById('bpos').textContent = `${b.x.toFixed(0)}, ${b.y.toFixed(0)}`;
  document.getElementById('php').textContent = p.hp;
  document.getElementById('ppos').textContent = `${p.x.toFixed(0)}, ${p.y.toFixed(0)}`;
  document.getElementById('act').textContent = ACT_LABEL[snap.lastAct] || snap.lastAct || '—';

  // 日志：只在有新增时重绘
  if (snap.events.length !== logLen) {
    logLen = snap.events.length;
    const el = document.getElementById('log');
    el.innerHTML = snap.events.slice(-40).reverse().map((e) =>
      `<div><span class="t">t${e.tick}</span> <b class="k-${e.kind}">${escapeHTML(e.text)}</b></div>`
    ).join('');
  }
}

// normalize 把快照里可能为 null 的数组字段归一化成空数组。
//
// Go 的 encoding/json 对 nil 切片输出 null（不是 []），前端直接 for...of
// 会抛 "is not iterable"。虽然 Go 侧已保证输出 []，这里再兜一层：
// 渲染循环**绝不能因为一个字段格式问题就整个中断**（表现为页面全白，
// 而且错误只在控制台里，非常难查——这个坑真实发生过）。
function normalize(snap) {
  if (!snap) return snap;
  if (!Array.isArray(snap.bombs)) snap.bombs = [];
  if (!Array.isArray(snap.blasts)) snap.blasts = [];
  if (!Array.isArray(snap.events)) snap.events = [];
  return snap;
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// ---- 主循环 ----
const TICK_MS = 50;      // 与 Go 侧 demoTick 一致（20Hz）
let acc = 0, lastT = performance.now();

function loop(now) {
  const dt = Math.min(200, now - lastT);
  lastT = now;
  // 整个循环体包在 try 里：任何一帧的异常都不该让 requestAnimationFrame
  // 断掉（断掉 = 页面定格成静态画面，且要开控制台才能看到原因）。
  try {
    if (!st.wasmMissing && !st.paused) {
      acc += dt;
      const steps = Math.min(8, Math.floor(acc / TICK_MS));
      if (steps > 0) {
        acc -= steps * TICK_MS;
        st.snapshot = normalize(JSON.parse(globalThis.bossStep(steps)));
        if (st.follow && st.snapshot) {
          // 玩家自动跟随在 Boss 附近，保证演示始终有交互
          const b = st.snapshot.boss, p = st.snapshot.player;
          const d = Math.hypot(b.x - p.x, b.y - p.y);
          if (d > 6) {
            const nx = b.x + (p.x - b.x) / d * 4;
            const ny = b.y + (p.y - b.y) / d * 4;
            st.snapshot = normalize(JSON.parse(globalThis.bossMovePlayer(Math.round(nx), Math.round(ny))));
          }
        }
      }
    }
    draw();
    updateHUD();
  } catch (err) {
    st.errCount = (st.errCount || 0) + 1;
    if (st.errCount <= 3) {
      console.error('[boss] 帧异常', err);
      showError('渲染异常：' + (err && err.message ? err.message : err));
    }
  }
  requestAnimationFrame(loop);
}

// ---- 启动 ----
//
// 注意 go.run() 是**永不返回**的（Go 的 main 里 select{} 阻塞，保持
// 导出函数可用）。所以它必须作为普通调用、**不能 await、也不能放进
// promise 链里**——否则它后面的初始化代码永远执行不到，页面就是空白的。
// 这里沿用 blast.js 里已验证的写法。
(async function boot() {
  if (!globalThis.Go) {
    st.wasmMissing = true;
    showError('wasm_exec.js 未加载（缺少 web/collide/wasm_exec.js）');
    return;
  }
  try {
    const go = new Go();
    const buf = await (await fetch('boss.wasm?v=14')).arrayBuffer();
    const mod = await WebAssembly.instantiate(buf, go.importObject);
    go.run(mod.instance); // 不 await：它永远不返回

    if (!globalThis.bossReset) {
      st.wasmMissing = true;
      showError('boss.wasm 未导出接口（编译目标不对？应跑 make wasm-boss）');
      return;
    }
    st.snapshot = normalize(JSON.parse(globalThis.bossReset()));
    st.tree = JSON.parse(globalThis.bossTree());
    document.getElementById('tree').textContent =
      `${st.tree.kind} · ${st.tree.nodes} 个节点\n\n${st.tree.text.trim()}`;
    resize();
    requestAnimationFrame(loop);
  } catch (err) {
    st.wasmMissing = true;
    showError('加载 boss.wasm 失败：' + (err && err.message ? err.message : err) +
      '\n（先在项目根目录跑 make wasm-boss 生成 boss.wasm）');
  }
})();

function showError(msg) {
  errBox.style.display = 'grid';
  errBox.textContent = msg;
}

window.addEventListener('resize', () => { resize(); draw(); });
