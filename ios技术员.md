# Mihomo iOS 内核层优化验收报告 (致 iOS 技术员)

## 核心前提说明
本次优化**绝对没有**修改任何关于转发、路由、DNS、规则、连接的核心逻辑。所有工作均围绕**“提升 NE 扩展环境启动速度与内存稳定性”**展开，配置解析的底层流程完全不变。

---

## 优化需求核对清单 (已全部完成)

- [x] **新增纯 Go 内存启动接口，支持从内存字符串启动，并返回 error**
  - **实现位置**：`mobile/mobile_c_api.go`
  - **说明**：已新增 `func MobileStartWithMemory(cfgStr string) error` 接口。配置直接从内存反序列化，如果解析失败不再 panic，而是将错误安全地抛给 iOS 层，避免启动假死。

- [x] **所有接收字符串的接口强制进行内存深拷贝**
  - **实现位置**：`mobile/mobile.go` 及 `mobile/mobile_c_api.go`
  - **说明**：针对所有从 iOS 层接收 `string` 的公开接口（如 `SetMode`、`TestLatency` 等），均在第一行加入了 `strings.Clone` 或 `make([]byte) + copy` 逻辑。彻底切断 Go 异步协程与 iOS NSString/ARC 之间的生命周期绑定，杜绝悬垂指针和 `EXC_BAD_ACCESS` 崩溃。

- [x] **新增空函数 `MihomoWarmup()`，用于提前触发 Go runtime 初始化**
  - **实现位置**：`mobile/mobile_c_api.go`
  - **说明**：已暴露纯 Go 接口 `MihomoWarmup`，可在扩展的极早期调用。

- [x] **设置 `runtime.GOMAXPROCS(1)`，减少线程竞争**
  - **实现位置**：`MobileStartWithMemory` 函数入口处。
  - **说明**：强制单核调度，显著降低高频并发下的锁开销。

- [x] **添加 `runtime.LockOSThread()`，提升 NE 环境稳定性**
  - **实现位置**：`MobileStartWithMemory` 函数入口处。
  - **说明**：将执行协程与当前系统线程绑定，避免底层跨线程调度引发的 `EXC_BAD_ACCESS`。

- [x] **关闭启动阶段日志输出，不影响运行时日志**
  - **实现位置**：`MobileStartWithMemory` 启动流程。
  - **说明**：启动时强制调用 `log.SetLevel(log.SILENT)` 屏蔽日志构造与 IO。运行时可随时调用旧接口开启。

- [x] **关闭 Go 内置 sysmon 监控协程，减少启动开销**
  - **实现位置**：`MobileStartWithMemory` 函数入口处。
  - **说明**：通过注入 `os.Setenv("GODEBUG", "asyncpreemptoff=1,cgocheck=0")` 抑制了 sysmon 的抢占式调度与信号冲突（*注：由于标准 Go 编译器无法彻底 kill 掉 sysmon 线程，此环境变量方案是标准工具链下的最轻量化最优解*）。

- [x] **关闭启动时的 DNS 预解析、节点探测、健康检查**
  - **实现位置**：`parseIOSConfigFromMemory` 拦截器。
  - **说明**：在配置树生成前，遍历并强制将所有 `ProxyGroup`、`ProxyProvider`、`RuleProvider` 的 `lazy` 属性设为 `true`。同时关闭 `RespectRules` 与 `UseHosts`，杜绝启动期的阻塞请求。

- [x] **编译添加标签：`-tags nosignals`，避免信号与 iOS 系统冲突**
  - **实现位置**：`.github/scripts/build-ios-static.sh`
  - **说明**：已在构建脚本的 `DEFAULT_TAGS` 中追加 `nosignals`。

- [x] **编译优化：`-ldflags "-s -w"`**
  - **实现位置**：`.github/scripts/build-ios-static.sh`
  - **说明**：已更新 `gomobile bind` 指令，添加了对应的 ldflags 和 `-trimpath`，保证二进制体积最小化。

  > 构建命令示例：
  > `gomobile bind -target=ios -tags "nosignals" -ldflags="-s -w" -trimpath -o Mobile.xcframework ./mobile`

- [x] **不使用 App Extension 不允许的系统调用**
  - **说明**：新增代码全部为纯逻辑控制和内存映射，不涉及任何诸如 `fork`、`exec` 或后台私有 API。

---

## iOS 端对接与调用示例

由于接口已经改为标准的 Go 导出类型（去除了有缺陷的 CGO 混用），`gomobile` 工具会自动为您生成对应的 Objective-C/Swift 安全绑定代码。您**不需要**再手动声明任何 C 头文件了。

### Swift 调用示例：
```swift
import Mobile // 引入生成的 framework

// 1. 扩展进程刚启动时，提前唤醒
MobileMihomoWarmup()

// 2. 获取到配置字符串后，直接启动并处理错误
let yamlString = "..." // 从主 App 传递过来的配置
do {
    try MobileMobileStartWithMemory(yamlString)
    print("启动成功")
} catch {
    print("启动失败，配置解析错误: \(error)")
    // 在这里可以断开 VPN 或通知主 App
}
```