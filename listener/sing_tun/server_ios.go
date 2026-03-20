//go:build ios

package sing_tun

import (
	"errors"
	tun "github.com/metacubex/sing-tun"
)

func tunNew(options tun.Options) (tun.Tun, error) {
	bridge := getPacketFlowBridge()
	if bridge != nil {
		return newPacketFlowTun(bridge), nil
	}
	return nil, errors.New("packet flow bridge is required on ios")
}
