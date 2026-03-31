package mobile

import (
	"os"
	"runtime"
	"time"

	"github.com/metacubex/mihomo/config"
	C_constant "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/log"
)

// MobileStartWithMemory starts the core with a configuration string directly from memory
func MobileStartWithMemory(cfgStr string) error {
	// 强制在 Go 堆上深拷贝一份配置数据，彻底切断与 Swift/Objective-C 的内存生命周期绑定
	// 避免解析过程中 iOS 端 ARC 提前释放字符串导致底层崩溃
	configBytes := make([]byte, len(cfgStr))
	copy(configBytes, cfgStr)

	stateMu.Lock()
	defer stateMu.Unlock()

	// 减少线程竞争
	runtime.GOMAXPROCS(1)

	// 提升 NE 环境稳定性
	runtime.LockOSThread()

	// 减少 sysmon 开销及相关信号冲突
	os.Setenv("GODEBUG", "asyncpreemptoff=1,cgocheck=0")

	// 关闭启动阶段日志输出，不影响运行时日志
	C_constant.SetHomeDir(homeDir)
	log.SetLevel(log.SILENT)

	// Parse config directly from memory using the deep-copied bytes
	cfg, err := parseIOSConfigFromMemory(configBytes)
	if err != nil {
		return err
	}

	// Apply config
	hub.ApplyConfig(cfg)
	lastConfigLoadAt = time.Now()
	isActive = true
	
	return nil
}

// MihomoWarmup is an empty function to trigger Go runtime initialization
func MihomoWarmup() {
	// Empty function to trigger Go runtime initialization
}

func parseIOSConfigFromMemory(data []byte) (*config.Config, error) {
	raw, err := config.UnmarshalRawConfig(data)
	if err != nil {
		return nil, err
	}
	
	// 关闭启动时的 DNS 预解析、节点探测、健康检查
	if raw != nil {
		// 设置所有的 ProxyGroup 延迟探测
		for _, group := range raw.ProxyGroup {
			group["lazy"] = true
		}
		// 设置所有的 Provider 延迟探测
		for _, p := range raw.ProxyProvider {
			if hc, ok := p["health-check"].(map[string]any); ok {
				hc["lazy"] = true
			}
		}
		for _, p := range raw.RuleProvider {
			if hc, ok := p["health-check"].(map[string]any); ok {
				hc["lazy"] = true
			}
		}
		// 禁用 DNS 预解析行为
		raw.DNS.RespectRules = false
		raw.DNS.UseHosts = false
		raw.DNS.UseSystemHosts = false
	}

	applyIOSRawProfile(raw)
	cfg, err := config.ParseRawConfig(raw)
	if err != nil {
		return nil, err
	}
	applyIOSCoreProfile(cfg)
	
	return cfg, nil
}
