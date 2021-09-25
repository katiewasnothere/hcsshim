package networking

import (
	"github.com/Microsoft/hcsshim/cmd/ncproxy/ncproxygrpc"
)

type NCProxyNetwork struct {
	NetworkName string
	Settings    *ncproxygrpc.NCProxyNetworkSettings
}

func CreateCustomNetwork(settings *ncproxygrpc.NCProxyNetworkSettings) (*NCProxyNetwork, error) {
	return &NCProxyNetwork{
		NetworkName: settings.Name,
		Settings:    settings,
	}, nil
}
