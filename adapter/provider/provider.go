package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"time"

	"github.com/Dreamacro/clash/adapter"
	C "github.com/Dreamacro/clash/constant"
	types "github.com/Dreamacro/clash/constant/provider"

	"gopkg.in/yaml.v3"
)

const (
	ReservedName = "default"
)

type ProxySchema struct {
	Proxies []map[string]any `yaml:"proxies"`
}

// for auto gc
type ProxySetProvider struct {
	*proxySetProvider
}

type proxySetProvider struct {
	*fetcher
	proxies     []C.Proxy
	healthCheck *HealthCheck
	children    []*FilterableProvider
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

func (pp *proxySetProvider) AddChild(child *FilterableProvider) {
	pp.children = append(pp.children, child)
}

func (pp *proxySetProvider) setProxies(proxies []C.Proxy) {
	pp.proxies = proxies
	pp.healthCheck.setProxy(proxies)
	if pp.healthCheck.auto() {
		go pp.healthCheck.check()
	}

	// update filterable proxy provider in proxy group
	for _, child := range pp.children {
		_ = child.Update()
	}
}

func stopProxyProvider(pd *ProxySetProvider) {
	pd.healthCheck.close()
	pd.fetcher.Destroy()
}

func NewProxySetProvider(name string, interval time.Duration, filter string, vehicle types.Vehicle, hc *HealthCheck) (*ProxySetProvider, error) {
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

	onUpdate := func(elm any) {
		ret := elm.([]C.Proxy)
		pd.setProxies(ret)
	}

	proxiesParseAndFilter := func(buf []byte) (any, error) {
		schema := &ProxySchema{}

		if err := yaml.Unmarshal(buf, schema); err != nil {
			return nil, err
		}

		if schema.Proxies == nil {
			return nil, errors.New("file must have a `proxies` field")
		}

		proxies := []C.Proxy{}
		for idx, mapping := range schema.Proxies {
			if name, ok := mapping["name"].(string); ok && len(filter) > 0 && !filterReg.MatchString(name) {
				continue
			}
			proxy, err := adapter.ParseProxy(mapping)
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

	fetcher := newFetcher(name, interval, vehicle, proxiesParseAndFilter, onUpdate)
	pd.fetcher = fetcher

	wrapper := &ProxySetProvider{pd}
	runtime.SetFinalizer(wrapper, stopProxyProvider)
	return wrapper, nil
}

// for auto gc
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

func stopCompatibleProvider(pd *CompatibleProvider) {
	pd.healthCheck.close()
}

func NewCompatibleProvider(name string, proxies []C.Proxy, hc *HealthCheck) (*CompatibleProvider, error) {
	if len(proxies) == 0 {
		return nil, errors.New("provider need one proxy at least")
	}

	if hc.auto() {
		go hc.process()
	}

	pd := &compatibleProvider{
		name:        name,
		proxies:     proxies,
		healthCheck: hc,
	}

	wrapper := &CompatibleProvider{pd}
	runtime.SetFinalizer(wrapper, stopCompatibleProvider)
	return wrapper, nil
}

// FilterableProvider for auto gc
type FilterableProvider struct {
	*filterableProvider
}

type filterableProvider struct {
	name        string
	parent      *ProxySetProvider
	healthCheck *HealthCheck
	proxies     []C.Proxy
	filterReg   *regexp.Regexp
}

func (fp *filterableProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":        fp.Name(),
		"type":        fp.Type().String(),
		"vehicleType": fp.VehicleType().String(),
		"proxies":     fp.Proxies(),
	})
}

func (fp *filterableProvider) Name() string {
	return fp.name
}

func (fp *filterableProvider) HealthCheck() {
	fp.healthCheck.check()
}

func (fp *filterableProvider) Update() error {
	proxies := []C.Proxy{}
	if fp.filterReg != nil {
		for _, proxy := range fp.parent.Proxies() {
			if !fp.filterReg.MatchString(proxy.Name()) {
				continue
			}
			proxies = append(proxies, proxy)
		}
	} else {
		proxies = fp.parent.Proxies()
	}

	// allow empty
	fp.proxies = proxies
	fp.healthCheck.setProxy(proxies)

	if fp.healthCheck.auto() {
		go fp.healthCheck.check()
	}
	return nil
}

func (fp *filterableProvider) Initial() error {
	return nil
}

func (fp *filterableProvider) VehicleType() types.VehicleType {
	return types.Compatible
}

func (fp *filterableProvider) Type() types.ProviderType {
	return types.Proxy
}

func (fp *filterableProvider) Proxies() []C.Proxy {
	return fp.proxies
}

func (fp *filterableProvider) Touch() {
	fp.healthCheck.touch()
}

func stopFilterableProvider(pd *FilterableProvider) {
	pd.healthCheck.close()
}

func NewFilterableProvider(name string, parent *ProxySetProvider, hc *HealthCheck, filterReg *regexp.Regexp) (*FilterableProvider, error) {
	if hc.auto() {
		go hc.process()
	}

	pd := &filterableProvider{
		name:        name,
		parent:      parent,
		healthCheck: hc,
		filterReg:   filterReg,
	}

	_ = pd.Update()

	wrapper := &FilterableProvider{pd}
	runtime.SetFinalizer(wrapper, stopFilterableProvider)
	return wrapper, nil
}
