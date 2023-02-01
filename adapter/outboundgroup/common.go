package outboundgroup

import (
	"time"

	"github.com/Dreamacro/clash/adapter"
	"github.com/Dreamacro/clash/adapter/outbound"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/constant/provider"
)

var fallbackProxyForEmptyProxyGroup = adapter.NewProxy(outbound.NewReject())

const (
	defaultGetProxiesDuration = time.Second * 5
)

func touchProviders(providers []provider.ProxyProvider) {
	for _, provider := range providers {
		provider.Touch()
	}
}

func getProvidersProxies(providers []provider.ProxyProvider, touch bool) []C.Proxy {
	proxies := []C.Proxy{}
	for _, provider := range providers {
		if touch {
			provider.Touch()
		}
		proxies = append(proxies, provider.Proxies()...)
	}

	// allow empty filterable proxy provider, add a fallback proxy to empty proxy group.
	if len(proxies) == 0 {
		proxies = append(proxies, fallbackProxyForEmptyProxyGroup)
	}
	return proxies
}
