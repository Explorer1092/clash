package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/samber/lo"
	"gopkg.in/yaml.v3"

	"github.com/Dreamacro/clash/adapter"
	"github.com/Dreamacro/clash/common/convert"
	C "github.com/Dreamacro/clash/constant"
	types "github.com/Dreamacro/clash/constant/provider"
	"github.com/Dreamacro/clash/tunnel/statistic"
)

const (
	ReservedName = "default"
)

type ProxySchema struct {
	Proxies []map[string]any `yaml:"proxies"`
}

var _ types.ProxyProvider = (*proxySetProvider)(nil)

// ProxySetProvider for auto gc
type ProxySetProvider struct {
	*proxySetProvider
}

type proxySetProvider struct {
	*fetcher[[]C.Proxy]
	proxies        []C.Proxy
	healthCheck    *HealthCheck
	providersInUse []types.ProxyProvider
}

func (pp *proxySetProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":        pp.Name(),
		"type":        pp.Type().String(),
		"vehicleType": pp.VehicleType().String(),
		"proxies":     pp.Proxies(),
		"updatedAt":   pp.updatedAt,
	})
}

func (pp *proxySetProvider) Name() string {
	return pp.name
}

func (pp *proxySetProvider) HealthCheck() {
	pp.healthCheck.check()
}

func (pp *proxySetProvider) Update() error {
	elm, same, err := pp.fetcher.Update()
	if err == nil && !same {
		pp.onUpdate(elm)
	}
	return err
}

func (pp *proxySetProvider) Initial() error {
	elm, err := pp.fetcher.Initial()
	if err != nil {
		return err
	}

	pp.onUpdate(elm)
	return nil
}

func (pp *proxySetProvider) Type() types.ProviderType {
	return types.Proxy
}

func (pp *proxySetProvider) Proxies() []C.Proxy {
	return pp.proxies
}

func (pp *proxySetProvider) Touch() {
	pp.healthCheck.touch()
}

func (pp *proxySetProvider) RegisterProvidersInUse(providers ...types.ProxyProvider) {
	pp.providersInUse = append(pp.providersInUse, providers...)
}

func (pp *proxySetProvider) Finalize() {
	pp.healthCheck.close()
	_ = pp.fetcher.Destroy()
	for _, pd := range pp.providersInUse {
		pd.Finalize()
	}
}

func (pp *proxySetProvider) setProxies(proxies []C.Proxy) {
	old := pp.proxies
	pp.proxies = proxies
	pp.healthCheck.setProxy(proxies)

	for _, use := range pp.providersInUse {
		_ = use.Update()
	}

	if len(old) > 0 {
		names := lo.Map(old, func(item C.Proxy, _ int) string {
			p := item.(C.ProxyAdapter)
			name := p.Name()
			go p.Cleanup()
			return name
		})
		statistic.DefaultManager.KickOut(names...)
		go pp.healthCheck.check()
	} else if pp.healthCheck.auto() {
		go func(hc *HealthCheck) {
			time.Sleep(30 * time.Second)
			hc.check()
		}(pp.healthCheck)
	}
}

func NewProxySetProvider(
	name string,
	interval time.Duration,
	filter string,
	vehicle types.Vehicle,
	hc *HealthCheck,
	forceCertVerify bool,
	udp bool,
	randomHost bool,
	prefixName string,
) (*ProxySetProvider, error) {
	filterReg, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter regex: %w", err)
	}

	if hc.auto() {
		go hc.process()
	}

	pd := &proxySetProvider{
		proxies:     []C.Proxy{},
		healthCheck: hc,
	}

	pd.fetcher = newFetcher[[]C.Proxy](
		name,
		interval,
		vehicle,
		proxiesParseAndFilter(filter, filterReg, forceCertVerify, udp, randomHost, prefixName),
		proxiesOnUpdate(pd),
	)

	wrapper := &ProxySetProvider{pd}
	return wrapper, nil
}

var _ types.ProxyProvider = (*compatibleProvider)(nil)

// CompatibleProvider for auto gc
type CompatibleProvider struct {
	*compatibleProvider
}

type compatibleProvider struct {
	name        string
	healthCheck *HealthCheck
	proxies     []C.Proxy
}

func (cp *compatibleProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":        cp.Name(),
		"type":        cp.Type().String(),
		"vehicleType": cp.VehicleType().String(),
		"proxies":     cp.Proxies(),
	})
}

func (cp *compatibleProvider) Name() string {
	return cp.name
}

func (cp *compatibleProvider) HealthCheck() {
	cp.healthCheck.check()
}

func (cp *compatibleProvider) Update() error {
	return nil
}

func (cp *compatibleProvider) Initial() error {
	return nil
}

func (cp *compatibleProvider) VehicleType() types.VehicleType {
	return types.Compatible
}

func (cp *compatibleProvider) Type() types.ProviderType {
	return types.Proxy
}

func (cp *compatibleProvider) Proxies() []C.Proxy {
	return cp.proxies
}

func (cp *compatibleProvider) Touch() {
	cp.healthCheck.touch()
}

func (cp *compatibleProvider) Finalize() {
	cp.healthCheck.close()
}

func NewCompatibleProvider(name string, proxies []C.Proxy, hc *HealthCheck) (*CompatibleProvider, error) {
	if len(proxies) == 0 {
		return nil, errors.New("provider need one proxy at least")
	}

	if hc.auto() {
		go hc.process()
		go func(hh *HealthCheck) {
			time.Sleep(30 * time.Second)
			hh.check()
		}(hc)
	}

	pd := &compatibleProvider{
		name:        name,
		proxies:     proxies,
		healthCheck: hc,
	}

	wrapper := &CompatibleProvider{pd}
	return wrapper, nil
}

var _ types.ProxyProvider = (*proxyFilterProvider)(nil)

// ProxyFilterProvider for filter provider
type ProxyFilterProvider struct {
	*proxyFilterProvider
}

type proxyFilterProvider struct {
	name        string
	psd         *ProxySetProvider
	proxies     []C.Proxy
	filter      *regexp.Regexp
	healthCheck *HealthCheck
}

func (pf *proxyFilterProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":        pf.Name(),
		"type":        pf.Type().String(),
		"vehicleType": pf.VehicleType().String(),
		"proxies":     pf.Proxies(),
	})
}

func (pf *proxyFilterProvider) Name() string {
	return pf.name
}

func (pf *proxyFilterProvider) HealthCheck() {
	pf.healthCheck.check()
}

func (pf *proxyFilterProvider) Update() error {
	pf.healthCheck.close()

	proxies := []C.Proxy{}
	if pf.filter != nil {
		for _, proxy := range pf.psd.Proxies() {
			if !pf.filter.MatchString(proxy.Name()) {
				continue
			}
			proxies = append(proxies, proxy)
		}
	} else {
		proxies = pf.psd.Proxies()
	}

	pf.proxies = proxies
	pf.healthCheck.setProxy(proxies)

	if len(proxies) != 0 && pf.healthCheck.auto() {
		go pf.healthCheck.process()
		if !pf.psd.healthCheck.auto() {
			go func(hc *HealthCheck) {
				time.Sleep(30 * time.Second)
				hc.check()
			}(pf.healthCheck)
		}
	}
	return nil
}

func (pf *proxyFilterProvider) Initial() error {
	return nil
}

func (pf *proxyFilterProvider) VehicleType() types.VehicleType {
	return types.Compatible
}

func (pf *proxyFilterProvider) Type() types.ProviderType {
	return types.Proxy
}

func (pf *proxyFilterProvider) Proxies() []C.Proxy {
	return pf.proxies
}

func (pf *proxyFilterProvider) Touch() {
	pf.healthCheck.touch()
}

func (pf *proxyFilterProvider) Finalize() {
	pf.healthCheck.close()
}

func NewProxyFilterProvider(name string, psd *ProxySetProvider, hc *HealthCheck, filterRegx *regexp.Regexp) *ProxyFilterProvider {
	pd := &proxyFilterProvider{
		psd:         psd,
		name:        name,
		healthCheck: hc,
		filter:      filterRegx,
	}

	_ = pd.Update()

	wrapper := &ProxyFilterProvider{pd}
	return wrapper
}

func proxiesOnUpdate(pd *proxySetProvider) func([]C.Proxy) {
	return func(elm []C.Proxy) {
		pd.setProxies(elm)
	}
}

func proxiesParseAndFilter(filter string, filterReg *regexp.Regexp, forceCertVerify, udp, randomHost bool, prefixName string) parser[[]C.Proxy] {
	return func(buf []byte) ([]C.Proxy, error) {
		schema := &ProxySchema{}

		if err := yaml.Unmarshal(buf, schema); err != nil {
			proxies, err1 := convert.ConvertsV2Ray(buf)
			if err1 != nil {
				proxies, err1 = convert.ConvertsWireGuard(buf)
			}
			if err1 != nil {
				return nil, errors.New("parse proxy provider failure, invalid data format")
			}
			schema.Proxies = proxies
		}

		if len(schema.Proxies) == 0 {
			return nil, errors.New("file must have a `proxies` field")
		}

		invalidServer := []string{
			"8.8.4.4",
			"8.8.8.8",
			"9.9.9.9",
			"1.0.0.1",
			"1.1.1.1",
			"1.2.3.4",
			"1.3.5.7",
			"127.0.0.1",
		}

		proxies := []C.Proxy{}
		for idx, mapping := range schema.Proxies {
			name, ok := mapping["name"].(string)
			if ok && len(filter) > 0 && !filterReg.MatchString(name) {
				continue
			}

			// skip invalid server address
			if server, ok1 := mapping["server"].(string); ok1 && lo.Contains(invalidServer, server) {
				continue
			}

			if prefixName != "" {
				mapping["name"] = prefixName + name
			}

			proxy, err := adapter.ParseProxy(mapping, forceCertVerify, udp, true, randomHost)
			if err != nil {
				return nil, fmt.Errorf("proxy %d error: %w", idx, err)
			}
			proxies = append(proxies, proxy)
		}

		if len(proxies) == 0 {
			if len(filter) > 0 {
				return nil, errors.New("doesn't match any proxy, please check your filter")
			}
			return nil, errors.New("file doesn't have any proxy")
		}

		return proxies, nil
	}
}
