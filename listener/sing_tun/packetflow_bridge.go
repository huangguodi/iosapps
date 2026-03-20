package sing_tun

import (
	"errors"
	"sync"
	"time"
)

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

var (
	packetFlowBridgeMu sync.RWMutex
	packetFlowBridge   PacketFlowBridge
)

func SetPacketFlowBridge(bridge PacketFlowBridge) {
	packetFlowBridgeMu.Lock()
	packetFlowBridge = bridge
	packetFlowBridgeMu.Unlock()
}

func ClearPacketFlowBridge() {
	SetPacketFlowBridge(nil)
}

func getPacketFlowBridge() PacketFlowBridge {
	packetFlowBridgeMu.RLock()
	bridge := packetFlowBridge
	packetFlowBridgeMu.RUnlock()
	return bridge
}

type packetFlowTun struct {
	bridge PacketFlowBridge
	closed chan struct{}
}

func newPacketFlowTun(bridge PacketFlowBridge) *packetFlowTun {
	return &packetFlowTun{
		bridge: bridge,
		closed: make(chan struct{}),
	}
}

func (t *packetFlowTun) Read(p []byte) (int, error) {
	for {
		select {
		case <-t.closed:
			return 0, errors.New("closed")
		default:
		}
		packet := t.bridge.ReadPacket()
		if packet == nil || len(packet.data) == 0 {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		payloadLen := len(packet.data) + 4
		if payloadLen > len(p) {
			payloadLen = len(p)
		}
		if payloadLen < 5 {
			return 0, nil
		}
		p[0] = 0
		p[1] = 0
		p[2] = 0
		p[3] = byte(packet.af)
		copy(p[4:payloadLen], packet.data[:payloadLen-4])
		return payloadLen, nil
	}
}

func (t *packetFlowTun) Write(p []byte) (int, error) {
	if len(p) < 5 {
		return len(p), nil
	}
	packet := NewPacketFlowPacket(p[4:], int64(p[3]))
	if !t.bridge.WritePacket(packet) {
		t.bridge.OnPacketFlowError("write packet failed")
	}
	return len(p), nil
}

func (t *packetFlowTun) Close() error {
	select {
	case <-t.closed:
	default:
		close(t.closed)
	}
	return nil
}
