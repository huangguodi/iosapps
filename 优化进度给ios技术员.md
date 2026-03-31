# iOS 端 libmihomo 优化进度报告

致 iOS 研发团队：

针对你们提出的 `mihomo在ios优化建议.md`，我们已在内核底层完成了一系列针对 iOS NetworkExtension 内存、并发与稳定性的深度优化。以下是各优化点的具体落实情况：

### 1. 核心优化点 1：重构 CGO 跨语言内存生命周期 (已完成)
- **新增 API**：提供了纯字节流交互接口 `MobileFeedPacketBytes(data []byte, af int64) bool` 和批量接口 `MobileFeedPacketsBytesBatch`，避免了 `MobileNewPacketFlowPacket` 产生的大量短生命周期对象。
- **底层内存复用**：在 `sing_tun/packetflow_bridge.go` 中引入了 `sync.Pool` 机制，网络包数据读取完成后自动回收 `[]byte`，彻底切断了与 Swift ARC 之间的生命周期绑定，解决了 `CppObjectLocks` 相关的 `EXC_BAD_ACCESS` 崩溃问题。

### 2. 核心优化点 2 & 5：内存占用、GC 优化与日志静默 (已完成)
- **GC 强制释放机制**：在 `mobile.Start()` 初始化后，内部启动了常驻的定时器（每 2 分钟），定期主动调用 `debug.FreeOSMemory()` 强制归还闲置内存给 iOS 系统，避免触发 Jetsam 15MB 强杀。
- **默认静默模式**：已确认 iOS Profile 强制配置 `raw.LogLevel = log.SILENT`，切断了高频的 CGO 日志回调损耗。

### 3. 核心功能裁剪与体积优化 (已完成)
- **静态资源裁剪**：在 `component/mmdb/mmdb.go` 中，由于当前是 iOS 定制版分支，已直接**硬编码移除**了 `Country.mmdb` 和 `GeoLite2-ASN.mmdb` 的全量加载，显著降低启动时的内存洪峰。
- **环境判断清理**：针对 iOS 定制版的特性，移除了多处不必要的 `runtime.GOOS == "ios"` 判断，让逻辑直接针对 iOS 环境生效，避免冗余的平台检测（包括在 Tunnel、MMDB 及 Updater 等模块的清理）。
- **编译参数优化**：修改了 `.github/scripts/build-ios-static.sh` 构建脚本，为 `gomobile bind` 补充了 `-ldflags="-s -w"` 和 `-trimpath` 标志，有效剔除了调试符号并缩减了生成的 `.a` / `xcframework` 体积。

### 4. 核心优化点 4 & 6：并发控制、超时策略与网络重置 (已完成)
- **并发连接数控制**：在 `tunnel/tunnel.go` 的 TCP/UDP 处理入口加入了全局连接数拦截（`statistic.DefaultManager.ConnectionsCount() >= 1000` 时直接丢弃新建连接），防止恶意并发耗尽 FD。
- **激进的超时回收**：将 TCP Keep-Alive 的 Idle 和 Interval 强制缩短至 15 秒，并将全局 UDP 会话过期时间 `udpTimeout` 缩短至 30 秒，且移除了 iOS 平台上无意义的 `KeepAlive` 禁用检测，确保快速回收无效句柄。
- **网络重置 API**：提供了轻量级的 `MobileResetNetwork()` 接口，供 iOS 端在网络切换或设备唤醒时调用。该接口会快速执行 `statistic.DefaultManager.ClearConnections()` 和 `resolver.ClearCache()`，无需重启整个内核实例。

### 5. 其他优化建议反馈 (Point 7, 8, 9)
- **DNS 污染与绕过 TUN (Point 7)**：请 iOS 端在初始化时继续使用 `MobileSetSocketProtector` 传入包含 `ProtectSocket` 实现的接口对象。底层 Dialer 已经完整接入了此 Hook，可以确保物理网络的绑定和系统 DNS 的隔离。
- **UDP 回退 (Point 8)**：当前已通过缩短 UDP Session Timeout 缓解假死问题，建议在配置中合理搭配 TCP Fallback 节点策略使用。
- **配置轻量化 (Point 9)**：建议 iOS 端在生成 `config.yaml` 时，全面启用 Rule-Provider 和 Proxy-Provider 并避免 inline 数万条规则。内核已原生支持 Provider 的 Lazy Load 机制。

**后续验收建议：**
请贵团队拉取最新代码并执行 iOS 构建，替换现有的 `libmihomo.a`。将 Swift 层的喂流逻辑从 `MobileNewPacketFlowPacket` + `MobileFeedPacketFromFlow` 迁移至新的 `MobileFeedPacketBytes`。期待你们的实测反馈！
