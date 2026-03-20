package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/mmdb"
	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener/sing_tun"
	"github.com/metacubex/mihomo/log"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

var (
	stateMu            sync.Mutex
	homeDir            string
	cfgFile            string
	appGroupDir        string
	lastConfigLoadAt   time.Time
	isActive           bool
	socketProtectorMu  sync.RWMutex
	currentProtector   SocketProtector
	socketHookAttached bool
)

type SocketProtector interface {
	ProtectSocket(fd int64, network string, address string) bool
	MarkSocket(fd int64, network string, address string) bool
}

type PacketFlowPacket struct {
	data []byte
	af   int64
}

func NewPacketFlowPacket(data []byte, af int64) *PacketFlowPacket {
	cloned := append([]byte(nil), data...)
	return &PacketFlowPacket{data: cloned, af: af}
}

func (p *PacketFlowPacket) Data() []byte {
	if p == nil {
		return nil
	}
	return append([]byte(nil), p.data...)
}

func (p *PacketFlowPacket) AF() int64 {
	if p == nil {
		return 0
	}
	return p.af
}

type PacketFlowBridge interface {
	ReadPacket() *PacketFlowPacket
	WritePacket(packet *PacketFlowPacket) bool
	OnPacketFlowError(message string)
}

type packetFlowBridgeAdapter struct {
	bridge PacketFlowBridge
}

func (a *packetFlowBridgeAdapter) ReadPacket() *sing_tun.PacketFlowPacket {
	if a == nil || a.bridge == nil {
		return nil
	}
	packet := a.bridge.ReadPacket()
	if packet == nil {
		return nil
	}
	return sing_tun.NewPacketFlowPacket(packet.data, packet.af)
}

func (a *packetFlowBridgeAdapter) WritePacket(packet *sing_tun.PacketFlowPacket) bool {
	if a == nil || a.bridge == nil || packet == nil {
		return false
	}
	return a.bridge.WritePacket(NewPacketFlowPacket(packet.Data(), packet.AF()))
}

func (a *packetFlowBridgeAdapter) OnPacketFlowError(message string) {
	if a == nil || a.bridge == nil {
		return
	}
	a.bridge.OnPacketFlowError(message)
}

func SetSocketProtector(protector SocketProtector) {
	socketProtectorMu.Lock()
	currentProtector = protector
	socketProtectorMu.Unlock()

	if protector == nil {
		dialer.DefaultSocketHook = nil
		socketHookAttached = false
		return
	}
	if socketHookAttached {
		return
	}
	dialer.DefaultSocketHook = func(network, address string, conn syscall.RawConn) error {
		var fd int
		err := conn.Control(func(s uintptr) {
			fd = int(s)
		})
		if err != nil {
			return err
		}
		socketProtectorMu.RLock()
		p := currentProtector
		socketProtectorMu.RUnlock()
		if p == nil {
			return nil
		}
		if !p.ProtectSocket(int64(fd), network, address) {
			return fmt.Errorf("protect socket failed: fd=%d network=%s address=%s", fd, network, address)
		}
		_ = p.MarkSocket(int64(fd), network, address)
		return nil
	}
	socketHookAttached = true
}

func ClearSocketProtector() {
	SetSocketProtector(nil)
}

func SetPacketFlowBridge(bridge PacketFlowBridge) {
	if bridge == nil {
		sing_tun.ClearPacketFlowBridge()
		return
	}
	sing_tun.SetPacketFlowBridge(&packetFlowBridgeAdapter{bridge: bridge})
}

func ClearPacketFlowBridge() {
	sing_tun.ClearPacketFlowBridge()
}

func FeedPacketFromFlow(packet *PacketFlowPacket) bool {
	if packet == nil {
		return false
	}
	return sing_tun.FeedPacketFromFlow(sing_tun.NewPacketFlowPacket(packet.data, packet.af))
}

func SetAppGroupDirectory(dir string) bool {
	normalized, ok := normalizeDir(dir)
	if !ok {
		return false
	}
	stateMu.Lock()
	appGroupDir = normalized
	stateMu.Unlock()
	return true
}

func Start(home, configFileName string) {
	stateMu.Lock()
	defer stateMu.Unlock()

	if appGroupDir == "" {
		panic("app group directory is required")
	}
	homeDir = appGroupDir
	cfgFile = configFileName

	C.SetHomeDir(homeDir)
	configPath, ok := resolveConfigPath(homeDir, cfgFile)
	if !ok {
		panic("config path is outside app group directory")
	}
	C.SetConfig(configPath)
	if err := config.Init(C.Path.HomeDir()); err != nil {
		panic(err)
	}
	cfg, err := parseIOSConfig(C.Path.Config())
	if err != nil {
		panic(err)
	}
	hub.ApplyConfig(cfg)
	lastConfigLoadAt = time.Now()
	isActive = true
}

func Stop() {
	stateMu.Lock()
	defer stateMu.Unlock()
	if !isActive {
		return
	}
	executor.Shutdown()
	isActive = false
}

func Sleep() {
	Stop()
}

func Wake() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	if homeDir == "" || cfgFile == "" {
		return false
	}
	if isActive {
		return true
	}
	C.SetHomeDir(homeDir)
	configPath, ok := resolveConfigPath(homeDir, cfgFile)
	if !ok {
		return false
	}
	C.SetConfig(configPath)
	cfg, err := parseIOSConfig(C.Path.Config())
	if err != nil {
		return false
	}
	hub.ApplyConfig(cfg)
	lastConfigLoadAt = time.Now()
	isActive = true
	return true
}

func RestartTunnelForNetworkChange() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	if homeDir == "" || cfgFile == "" {
		return false
	}
	if isActive {
		executor.Shutdown()
		isActive = false
	}
	C.SetHomeDir(homeDir)
	configPath, ok := resolveConfigPath(homeDir, cfgFile)
	if !ok {
		return false
	}
	C.SetConfig(configPath)
	cfg, err := parseIOSConfig(C.Path.Config())
	if err != nil {
		return false
	}
	hub.ApplyConfig(cfg)
	lastConfigLoadAt = time.Now()
	isActive = true
	return true
}

func SetLogLevel(level string) {
	logLevel, ok := log.LogLevelMapping[strings.ToLower(level)]
	if !ok {
		return
	}
	log.SetLevel(logLevel)
}

func ForceUpdateConfig(configFileName string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	if homeDir == "" {
		return
	}
	if time.Since(lastConfigLoadAt) < time.Second {
		return
	}
	cfgFile = configFileName
	configPath, ok := resolveConfigPath(homeDir, cfgFile)
	if !ok {
		panic("config path is outside app group directory")
	}
	C.SetConfig(configPath)
	cfg, err := parseIOSConfig(C.Path.Config())
	if err != nil {
		panic(err)
	}
	hub.ApplyConfig(cfg)
	lastConfigLoadAt = time.Now()
}

func SetMode(mode string) {
	if m, ok := tunnel.ModeMapping[strings.ToLower(mode)]; ok {
		tunnel.SetMode(m)
		statistic.DefaultManager.ClearConnections()
	}
}

func GetMode() string {
	return tunnel.Mode().String()
}

func GetProxies() string {
	all := proxiesWithProviders()
	proxiesPayload := make(map[string]any, len(all))
	for name, proxy := range all {
		data, err := json.Marshal(proxy)
		if err != nil {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}
		if code := proxyCountry(proxy); code != "" {
			item["country"] = code
		}
		proxiesPayload[name] = item
	}
	payload := map[string]any{"proxies": proxiesPayload}
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func ProxyNames() string {
	all := proxiesWithProviders()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func SelectProxy(groupName, proxyName string) bool {
	proxies := tunnel.Proxies()
	group, ok := proxies[groupName]
	if !ok {
		return false
	}

	selector := findSelectable(group)
	if selector == nil && strings.EqualFold(groupName, "GLOBAL") {
		if globalGroup, exists := proxies["GLOBAL"]; exists {
			selector = findSelectable(globalGroup)
		}
	}
	if selector == nil {
		return false
	}
	if err := selector.Set(proxyName); err != nil {
		return false
	}
	statistic.DefaultManager.ClearConnections()
	return true
}

func TrafficUp() int64 {
	up, _ := statistic.DefaultManager.Now()
	return up
}

func TrafficDown() int64 {
	_, down := statistic.DefaultManager.Now()
	return down
}

func TrafficTotalUp() int64 {
	up, _ := statistic.DefaultManager.Total()
	return up
}

func TrafficTotalDown() int64 {
	_, down := statistic.DefaultManager.Total()
	return down
}

func TestLatency(proxyName string) string {
	proxy, ok := proxiesWithProviders()[proxyName]
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	delay, err := proxy.URLTest(ctx, C.DefaultTestURL, nil)
	if err != nil || delay == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(delay/10), 10)
}

func Version() string {
	return C.Version
}

func parseIOSConfig(path string) (*config.Config, error) {
	if !isPathInsideDir(path, homeDir) {
		return nil, fmt.Errorf("config path is outside app group directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := config.UnmarshalRawConfig(data)
	if err != nil {
		return nil, err
	}
	applyIOSRawProfile(raw)
	cfg, err := config.ParseRawConfig(raw)
	if err != nil {
		return nil, err
	}
	applyIOSCoreProfile(cfg)
	return cfg, nil
}

func normalizeDir(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

func resolveConfigPath(baseDir, fileName string) (string, bool) {
	if fileName == "" {
		return "", false
	}
	if filepath.IsAbs(fileName) {
		if isPathInsideDir(fileName, baseDir) {
			return filepath.Clean(fileName), true
		}
		return "", false
	}
	joined := filepath.Join(baseDir, fileName)
	if !isPathInsideDir(joined, baseDir) {
		return "", false
	}
	return filepath.Clean(joined), true
}

func isPathInsideDir(path, baseDir string) bool {
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	pathClean := filepath.Clean(pathAbs)
	baseClean := filepath.Clean(baseAbs)
	if pathClean == baseClean {
		return true
	}
	prefix := baseClean + string(filepath.Separator)
	return strings.HasPrefix(pathClean, prefix)
}

func applyIOSRawProfile(raw *config.RawConfig) {
	if raw == nil {
		return
	}
	raw.FindProcessMode = process.FindProcessOff
	raw.LogLevel = log.SILENT
	raw.GeoAutoUpdate = false
	raw.GeoUpdateInterval = 0
	raw.GeodataLoader = "memconservative"
	raw.ExternalController = ""
	raw.ExternalControllerTLS = ""
	raw.ExternalControllerUnix = ""
	raw.ExternalControllerPipe = ""
	raw.ExternalUI = ""
	raw.ExternalUIURL = ""
	raw.ExternalUIName = ""
	raw.ExternalDohServer = ""
	raw.Secret = ""
	raw.Profile.StoreSelected = false
	raw.Profile.StoreFakeIP = false
	raw.Sniffer.Enable = false
	raw.Sniffer.Sniff = map[string]config.RawSniffingConfig{}
	raw.Sniffer.Sniffing = nil
	raw.Sniffer.ForceDomain = nil
	raw.Sniffer.SkipSrcAddress = nil
	raw.Sniffer.SkipDstAddress = nil
	raw.Sniffer.SkipDomain = nil
	raw.Sniffer.ForceDnsMapping = false
	raw.Sniffer.ParsePureIp = false
	raw.Hosts = map[string]any{}
	raw.DNS.EnhancedMode = C.DNSNormal
	raw.DNS.RespectRules = false
	raw.DNS.PreferH3 = false
	raw.DNS.Listen = ""
	raw.DNS.FakeIPRange = ""
	raw.DNS.FakeIPRange6 = ""
	raw.DNS.FakeIPFilter = nil
	raw.DNS.FakeIPTTL = 0
	raw.DNS.UseHosts = false
	raw.DNS.UseSystemHosts = false
	raw.DNS.Fallback = nil
	raw.DNS.FallbackFilter = config.RawFallbackFilter{}
	raw.DNS.NameServerPolicy = nil
	raw.DNS.ProxyServerNameserver = nil
	raw.DNS.ProxyServerNameserverPolicy = nil
	raw.DNS.DirectNameServer = nil
	raw.DNS.DirectNameServerFollowPolicy = false
	raw.DNS.CacheAlgorithm = ""
	raw.DNS.CacheMaxSize = 0
	raw.Tun.DNSHijack = nil
	raw.Tun.FileDescriptor = 0
	raw.Tun.RecvMsgX = false
	raw.Tun.SendMsgX = false
	raw.Tun.Device = ""
	raw.Tun.StrictRoute = false
	raw.Tun.RouteAddress = nil
	raw.Tun.RouteExcludeAddress = nil
	raw.Tun.Inet4RouteAddress = nil
	raw.Tun.Inet6RouteAddress = nil
	raw.Tun.Inet4RouteExcludeAddress = nil
	raw.Tun.Inet6RouteExcludeAddress = nil
	raw.Tun.IncludeInterface = nil
	raw.Tun.ExcludeInterface = nil
	raw.Tun.IPRoute2TableIndex = 0
	raw.Tun.IPRoute2RuleIndex = 0
	raw.Tun.AutoRedirectInputMark = 0
	raw.Tun.AutoRedirectOutputMark = 0
	raw.Tun.AutoRedirectIPRoute2FallbackRuleIndex = 0
	raw.Tun.LoopbackAddress = nil
	raw.Tun.AutoRoute = false
	raw.Tun.AutoDetectInterface = false
	raw.Tun.AutoRedirect = false
	raw.Tun.IncludeAndroidUser = nil
	raw.Tun.IncludePackage = nil
	raw.Tun.ExcludePackage = nil
	raw.Tun.IncludeUID = nil
	raw.Tun.IncludeUIDRange = nil
	raw.Tun.ExcludeUID = nil
	raw.Tun.ExcludeUIDRange = nil
	raw.Tun.RouteAddressSet = nil
	raw.Tun.RouteExcludeAddressSet = nil
	raw.ProxyProvider = map[string]map[string]any{}
	raw.RuleProvider = map[string]map[string]any{}
}

func applyIOSCoreProfile(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if cfg.General != nil {
		cfg.General.FindProcessMode = process.FindProcessOff
		tun := &cfg.General.Tun
		tun.AutoRoute = false
		tun.AutoDetectInterface = false
		tun.AutoRedirect = false
		tun.DNSHijack = nil
		tun.IncludeAndroidUser = nil
		tun.IncludePackage = nil
		tun.ExcludePackage = nil
		tun.IncludeUID = nil
		tun.IncludeUIDRange = nil
		tun.ExcludeUID = nil
		tun.ExcludeUIDRange = nil
		tun.RouteAddressSet = nil
		tun.RouteExcludeAddressSet = nil
		tun.FileDescriptor = 0
		tun.RecvMsgX = false
		tun.SendMsgX = false
		tun.Device = ""
		tun.StrictRoute = false
		tun.RouteAddress = nil
		tun.RouteExcludeAddress = nil
		tun.Inet4RouteAddress = nil
		tun.Inet6RouteAddress = nil
		tun.Inet4RouteExcludeAddress = nil
		tun.Inet6RouteExcludeAddress = nil
		tun.IncludeInterface = nil
		tun.ExcludeInterface = nil
		tun.IPRoute2TableIndex = 0
		tun.IPRoute2RuleIndex = 0
		tun.AutoRedirectInputMark = 0
		tun.AutoRedirectOutputMark = 0
		tun.AutoRedirectIPRoute2FallbackRuleIndex = 0
		tun.LoopbackAddress = nil
	}
	if cfg.DNS != nil {
		cfg.DNS.EnhancedMode = C.DNSNormal
		cfg.DNS.Listen = ""
		cfg.DNS.FakeIPRange = netip.Prefix{}
		cfg.DNS.FakeIPRange6 = netip.Prefix{}
		cfg.DNS.FakeIPPool = nil
		cfg.DNS.FakeIPPool6 = nil
		cfg.DNS.FakeIPSkipper = nil
		cfg.DNS.FakeIPTTL = 0
		cfg.DNS.UseHosts = false
		cfg.DNS.UseSystemHosts = false
		cfg.DNS.Fallback = nil
		cfg.DNS.FallbackIPFilter = nil
		cfg.DNS.FallbackDomainFilter = nil
		cfg.DNS.ProxyServerNameserver = nil
		cfg.DNS.DirectNameServer = nil
		cfg.DNS.DirectFollowPolicy = false
		cfg.DNS.NameServerPolicy = nil
		cfg.DNS.ProxyServerPolicy = nil
		cfg.DNS.CacheAlgorithm = ""
		cfg.DNS.CacheMaxSize = 0
	}
	cfg.Hosts = nil
	if cfg.Controller != nil {
		cfg.Controller.ExternalController = ""
		cfg.Controller.ExternalControllerTLS = ""
		cfg.Controller.ExternalControllerUnix = ""
		cfg.Controller.ExternalControllerPipe = ""
		cfg.Controller.ExternalUI = ""
		cfg.Controller.ExternalUIURL = ""
		cfg.Controller.ExternalUIName = ""
		cfg.Controller.ExternalDohServer = ""
		cfg.Controller.Secret = ""
	}
	if cfg.Profile != nil {
		cfg.Profile.StoreSelected = false
		cfg.Profile.StoreFakeIP = false
	}
	if cfg.Sniffer != nil {
		cfg.Sniffer.Enable = false
		cfg.Sniffer.Sniffers = nil
		cfg.Sniffer.ForceDomain = nil
		cfg.Sniffer.SkipSrcAddress = nil
		cfg.Sniffer.SkipDstAddress = nil
		cfg.Sniffer.SkipDomain = nil
		cfg.Sniffer.ForceDnsMapping = false
		cfg.Sniffer.ParsePureIp = false
	}
	cfg.Providers = nil
	cfg.RuleProviders = nil
}

func proxiesWithProviders() map[string]C.Proxy {
	all := make(map[string]C.Proxy)
	for name, proxy := range tunnel.Proxies() {
		all[name] = proxy
	}
	for _, provider := range tunnel.Providers() {
		for _, proxy := range provider.Proxies() {
			all[proxy.Name()] = proxy
		}
	}
	return all
}

func findSelectable(proxy C.Proxy) outboundgroup.SelectAble {
	current := proxy
	for i := 0; i < 16 && current != nil; i++ {
		if selectable, ok := any(current).(outboundgroup.SelectAble); ok {
			return selectable
		}
		if adapter := current.Adapter(); adapter != nil {
			if selectable, ok := adapter.(outboundgroup.SelectAble); ok {
				return selectable
			}
		}
		next := current.Unwrap(nil, false)
		if next == nil || next == current {
			break
		}
		current = next
	}
	return nil
}

func proxyCountry(proxy C.Proxy) string {
	host, err := parseProxyHost(proxy.Addr())
	if err != nil || host == "" {
		return ""
	}

	if ip := net.ParseIP(host); ip != nil {
		return lookupCountryByIP(ip)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if ip == nil || ip.To16() == nil {
			continue
		}
		if code := lookupCountryByIP(ip); code != "" {
			return code
		}
	}
	return ""
}

func lookupCountryByIP(ip net.IP) string {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return ""
	}
	codes := mmdb.IPInstance().LookupCode(addr.AsSlice())
	if len(codes) == 0 || codes[0] == "" {
		return ""
	}
	return strings.ToUpper(codes[0])
}

func parseProxyHost(addr string) (string, error) {
	if addr == "" {
		return "", nil
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host, nil
	}
	if strings.Count(addr, ":") > 1 && !strings.Contains(addr, "]") {
		return addr, nil
	}
	return addr, nil
}
