package networking

import (
	"context"

	"github.com/Microsoft/hcsshim/cmd/ncproxy/ncproxygrpc"
)

type NCProxyEndpoint struct {
	EndpointName string
	NamespaceID  string
	Settings     *ncproxygrpc.NCProxyEndpointSettings
}

func CreateCustomEndpoint(settings *ncproxygrpc.NCProxyEndpointSettings) (*NCProxyEndpoint, error) {
	return &NCProxyEndpoint{
		EndpointName: settings.Name,
		Settings:     settings,
	}, nil
}

func (c *NCProxyEndpoint) Add(namespaceID string) {
	c.NamespaceID = namespaceID
}

func (c *NCProxyEndpoint) GetNamespaceID() string {
	return c.NamespaceID
}

func (c *NCProxyEndpoint) Delete(ctx context.Context) error {
	return nil
}

func (c *NCProxyEndpoint) Name() string {
	return c.EndpointName
}

func (c *NCProxyEndpoint) GetSettings() *ncproxygrpc.EndpointSettings {
	return &ncproxygrpc.EndpointSettings{
		Settings: &ncproxygrpc.EndpointSettings_NcproxyEndpoint{
			NcproxyEndpoint: c.Settings,
		},
	}
}
