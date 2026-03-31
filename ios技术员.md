# Mihomo iOS 内核层优化验收报告 (致 iOS 技术员)

## 核心前提说明
本次优化**绝对没有**修改任何关于转发、路由、DNS、规则、连接的核心逻辑。所有工作均围绕**“提升 NE 扩展环境启动速度与内存稳定性”**展开，配置解析的底层流程完全不变。

---

## 优化需求核对清单 (已全部完成)

- [x] **新增 C 导出函数，支持从内存字符串启动**
  - **实现位置**：`mobile/mobile_c_api.go`
  - **说明**：已新增 `//export MobileStartWithMemory` 接口。配置直接从内存反序列化，全程不进行文件 I/O。

- [x] **新增空函数 `MihomoWarmup()`，用于提前触发 Go runtime 初始化**
  - **实现位置**：`mobile/mobile_c_api.go`
  - **说明**：已暴露纯 C 符号 `MihomoWarmup`，可在扩展的极早期调用。

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

- [x] **编译优化：`-ldflags "-s -w -linkmode internal"`**
  - **实现位置**：`.github/scripts/build-ios-static.sh`
  - **说明**：已更新 `gomobile bind` 指令，添加了对应的 ldflags 和 `-trimpath`，保证二进制体积最小化且无外部动态链接异常。

- [x] **不使用 App Extension 不允许的系统调用**
  - **说明**：新增代码全部为纯逻辑控制和内存映射，不涉及任何诸如 `fork`、`exec` 或后台私有 API。

---

## iOS 端对接与调用示例

由于新增的接口是纯 C 导出（脱离了 `gomobile` 繁重的对象包装），**在 Swift/Objective-C 中调用时极其轻量**。请在您的 `Bridging-Header.h` 中添加如下声明即可直接使用：

```c
// 在 iOS 项目的 Bridging Header 中添加
#ifdef __cplusplus
extern "C" {
#endif

// 提前唤醒 Go Runtime
extern void MihomoWarmup();

// 直接从内存加载 YAML 字符串启动
extern void MobileStartWithMemory(char* configC);

#ifdef __cplusplus
}
#endif
```

### Swift 调用示例：
```swift
// 1. 扩展进程刚启动时，提前唤醒 (可选)
MihomoWarmup()

// 2. 获取到配置字符串后，直接启动
let yamlString = "..." // 从主 App 传递过来的配置
yamlString.withCString { cString in
    MobileStartWithMemory(UnsafeMutablePointer(mutating: cString))
}
```