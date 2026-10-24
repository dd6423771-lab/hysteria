# 面板对接 /kickall 的最小改动点设计（仅设计，不改面板代码）

## 现状（VPS /opt/hxy-admin/current/runtime/）

- `mc/src/nodes/hy2ctl.mjs`：`kickUrlFrom(statsUrl)` 拼出 `POST <stats>/kick`；`ctl.kick(targets)` 是面板唯一的"断开已有连接"控制口。
- `mc/src/nodes/enforcement-core.mjs`（139-181 行）：撤销时先读 `/online`，对在线目标做 **≤4 轮** kick→验证→补踢循环；`disconnectVerified` 只是快照，非因果证明；`kickRounds/kickIncomplete` 等字段进 enforcement-report。
- 内核侧 one-shot 语义导致：空闲连接永不消费标记、活跃连接第一条流量的连接被杀、标记一次性 → 4 轮补踢只能 best_effort。

## 改动点 1：hy2ctl.mjs 增加 kickAll（+约 15 行）

```js
export function kickallUrlFrom(statsUrl) {
  const u = baseOf(statsUrl);          // 复用现有 baseOf 逻辑
  return u ? u + '/kickall' : '';
}

// ctl 对象新增：
async kickAll(ids) {
  const r = await fetch(kickallUrlFrom(this.statsUrl), {
    method: 'POST',
    headers: { Authorization: this.secret, 'Content-Type': 'application/json' },
    body: JSON.stringify(ids),
  });
  if (!r.ok) return { ok: false };
  const j = await r.json();            // {"total":N,"results":[...]}
  return { ok: true, kicked: j.total };
}
```

兼容性：返回结构与现有 `ctl.kick` 的 `{ ok, kicked }` 契约一致，enforcement-report 的 `kicked` 字段语义不变（kickall 的 `total` = 实际关闭的连接数）。

## 改动点 2：enforcement-core.mjs 把轮次循环换成单发 + 单验（-约 20 行）

- 将 `for (let round = 0; round < maxKickRounds; round++)` 循环体替换为：
  1. `await ctl.kickAll(targets)` 一次调用（内核同步关闭全部现存连接，无标记残留）；
  2. `await pause(verifyDelayMs)` 后读一次 `/online` 佐证快照；
  3. `disconnectVerified` 语义可升级：kickall 返回 `closed>0` 即为**因果性**关闭证据（0x107 由本调用直接产生），快照仅作补充。
- `maxKickRounds` 配置保留但不再参与 hy2 路径（或仅对仍走 `/kick` 的旧内核节点生效），`kickRounds` 字段恒为 1，`kickIncomplete` 仅在 kickAll 网络失败时置位。

## 改动点 3：能力探测（灰度期兼容新旧内核）

- hy2ctl 初始化时 `HEAD/POST /kickall` 探测：200/400（4xx body 可解析）= 支持；404 = 旧内核。
- 旧内核节点继续走现有 4 轮补踢（best_effort 报告不变），新内核节点走 kickall。一个面板可同时管两种节点。

## 顺序要求（与现有一致，不变）

撤销仍然 = **认证拒绝先行（hy2-auth 层 deny）+ kickall 断存量**。kickall 不阻止新认证，认证拒绝必须先于/同时生效，否则被吊销成员可在窗口期内重连。

## 上线顺序建议

1. 内核灰度（本报告的 24450 模式）→ 2. 面板加 kickAll 能力探测与代码 → 3. 逐节点替换二进制（见切换方案）→ 4. 观察 enforcement-report 中 kickRounds=1 / disconnectVerified 因果语义 → 5. 清理旧补踢路径。
