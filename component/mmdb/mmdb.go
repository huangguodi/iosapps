package mmdb

import (
	"sync"

	mihomoOnce "github.com/metacubex/mihomo/common/once"
	"github.com/metacubex/mihomo/log"

	"github.com/oschwald/maxminddb-golang"
)

type databaseType = uint8

const (
	typeMaxmind databaseType = iota
	typeSing
	typeMetaV0
)

var (
	ipReader  IPReader
	asnReader ASNReader
	ipOnce    sync.Once
	asnOnce   sync.Once
)

func LoadFromBytes(buffer []byte) {
	ipOnce.Do(func() {
		mmdb, err := maxminddb.FromBytes(buffer)
		if err != nil {
			log.Fatalln("Can't load mmdb: %s", err.Error())
		}
		ipReader = IPReader{Reader: mmdb}
		switch mmdb.Metadata.DatabaseType {
		case "sing-geoip":
			ipReader.databaseType = typeSing
		case "Meta-geoip0":
			ipReader.databaseType = typeMetaV0
		default:
			ipReader.databaseType = typeMaxmind
		}
	})
}

func Verify(path string) bool {
	instance, err := maxminddb.Open(path)
	if err == nil {
		instance.Close()
	}
	return err == nil
}

func IPInstance() IPReader {
	ipOnce.Do(func() {
		// Optimization Point 3: Avoid loading large MMDB on iOS (iOS specific build)
		log.Infoln("Skip loading MMDB file on iOS")
		return
	})

	return ipReader
}

func ASNInstance() ASNReader {
	asnOnce.Do(func() {
		// Optimization Point 3: Avoid loading large ASN on iOS (iOS specific build)
		log.Infoln("Skip loading ASN file on iOS")
		return
	})

	return asnReader
}

func ReloadIP() {
	mihomoOnce.Reset(&ipOnce)
}

func ReloadASN() {
	mihomoOnce.Reset(&asnOnce)
}
