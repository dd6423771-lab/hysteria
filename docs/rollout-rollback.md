# hysteria2 内核替换：生产切换与回退方案（2026-10-24）

## 前提

- 新二进制：`hysteria-linux-amd64`，sha256 `fba9862ec4e1aa03a09a330be5168555f5ded0cdb55f78978a63165263204710`，版本串 `v2.13.0-kickall`，commit `c9d3e8f2...`
- 生产现状：`hysteria-server.service`（24444，stats :24448）、`hysteria-obfs.service`（24446，stats :24449），`/opt/hysteria/hysteria` = 官方 v2.13.0（ca8fbd8）
- 配置文件无需任何修改（注册表在配置了 trafficStats 时自动启用）

## 切换步骤（单节点，预计停机窗口 <5 秒/服务）

1. **备份旧内核**：
   ```bash
   cp /opt/hysteria/hysteria /opt/hysteria/hysteria.bak-v2.13.0-$(date +%Y%m%d%H%M)
   sha256sum /opt/hysteria/hysteria.bak-* >> /opt/hysteria/ROLLBACK.txt
   ```
2. **落位新二进制**：
   ```bash
   sha256sum hysteria-linux-amd64   # 必须等于 fba9862e...，不符立即终止
   install -m 755 hysteria-linux-amd64 /opt/hysteria/hysteria-kickall
   ```
3. **先切 24446（obfs 节点，用户面小），观察 10 分钟**：
   ```bash
   systemctl restart hysteria-obfs && sleep 5 && systemctl is-active hysteria-obfs
   curl -s -H "Authorization: <secret>" http://127.0.0.1:24449/online   # 应恢复计数
   ```
4. **再切 24444（主节点）**，同上验证 `:24448/online`。
5. **验证 kickall 端点存在**（仅本机）：
   ```bash
   curl -s -X POST -H "Authorization: <secret>" -d '["__probe__"]' http://127.0.0.1:24449/kickall
   # 期望 {"total":0,"results":[{"id":"__probe__","closed":0}]}；404 = 二进制不对
   ```
6. 客户端侧：QUIC 断开后 hysteria client 自动重连（实测 client 进程存活并重连），无需用户操作。

## 回退（任一异常 30 秒内完成）

```bash
cp /opt/hysteria/hysteria.bak-v2.13.0-* /opt/hysteria/hysteria
systemctl restart hysteria-obfs hysteria-server
# 回退后 /kickall 变 404；面板侧 kickAll 探测自动降级回 4 轮补踢（见对接设计）
```

回退无条件安全：新内核只是**增加**了注册表与端点，未改动任何存量行为（/kick、认证、流量记账逐字节兼容，协议不变）。

## 红线与风险控制

- 切换只在低峰时段；一次只切一个服务；每步 `systemctl is-active` + `/online` 双确认。
- 健康判据：重启后 `/online` 在 60 秒内出现计数；连续 3 次探活失败立即回退。
- 面板在切换期间保持 MC_ENFORCE=0，切换完成并观察一个 enforce 周期后再开启。
- 不删远程分支、不 force push；旧二进制备份保留至稳定运行 7 天后清理。
