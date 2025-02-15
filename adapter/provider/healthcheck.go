package provider

import (
	"context"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/Dreamacro/clash/common/batch"
	C "github.com/Dreamacro/clash/constant"
)

const (
	defaultURLTestTimeout = time.Second * 5
)

type HealthCheckOption struct {
	URL      string
	Interval uint
}

type HealthCheck struct {
	url       string
	proxies   []C.Proxy
	interval  uint
	lazy      bool
	lastTouch *atomic.Int64
	running   *atomic.Bool
	done      chan struct{}
	mux       sync.Mutex
}

func (hc *HealthCheck) process() {
	hc.mux.Lock()
	if hc.running.Load() {
		hc.mux.Unlock()
		return
	}
	hc.running.Store(true)
	hc.mux.Unlock()

	ticker := time.NewTicker(time.Duration(hc.interval) * time.Second)

	for {
		select {
		case <-ticker.C:
			now := time.Now().Unix()
			if !hc.lazy || now-hc.lastTouch.Load() < int64(hc.interval) {
				hc.check()
			}
		case <-hc.done:
			ticker.Stop()
			return
		}
	}
}

func (hc *HealthCheck) setProxy(proxies []C.Proxy) {
	hc.proxies = proxies
}

func (hc *HealthCheck) auto() bool {
	return hc.interval != 0
}

func (hc *HealthCheck) touch() {
	hc.lastTouch.Store(time.Now().Unix())
}

func (hc *HealthCheck) check() {
	proxies := hc.proxies
	if len(proxies) == 0 {
		return
	}

	b, _ := batch.New[bool](context.Background(), batch.WithConcurrencyNum[bool](10))
	for _, proxy := range proxies {
		p := proxy
		b.Go(p.Name(), func() (bool, error) {
			ctx, cancel := context.WithTimeout(context.Background(), defaultURLTestTimeout)
			defer cancel()
			_, _, _ = p.URLTest(ctx, hc.url)
			return false, nil
		})
	}
	b.Wait()
}

func (hc *HealthCheck) close() {
	hc.mux.Lock()
	defer hc.mux.Unlock()

	if !hc.running.Load() {
		return
	}
	hc.running.Store(false)
	select {
	case hc.done <- struct{}{}:
	default:
	}
}

func NewHealthCheck(proxies []C.Proxy, url string, interval uint, lazy bool) *HealthCheck {
	return &HealthCheck{
		proxies:   proxies,
		url:       url,
		interval:  interval,
		lazy:      lazy,
		lastTouch: atomic.NewInt64(0),
		running:   atomic.NewBool(false),
		done:      make(chan struct{}, 1),
	}
}
