package main

import (
	"sync"

	"github.com/Microsoft/hcsshim/internal/networking"
)

type customComputeNetworkingCache struct {
	networks  *customNetworkCache
	endpoints *customEndpointCache
}

func newCustomComputeNetworkingCache() *customComputeNetworkingCache {
	return &customComputeNetworkingCache{
		networks:  newCustomNetworkCache(),
		endpoints: newCustomEndpointCache(),
	}
}

type customNetworkCache struct {
	// lock for synchronizing read/write access to `cache`
	rw    sync.RWMutex
	cache map[string]*networking.NCProxyNetwork
}

func newCustomNetworkCache() *customNetworkCache {
	return &customNetworkCache{
		cache: make(map[string]*networking.NCProxyNetwork),
	}
}

func (n *customNetworkCache) get(name string) (*networking.NCProxyNetwork, bool) {
	n.rw.RLock()
	defer n.rw.RUnlock()
	result, ok := n.cache[name]
	return result, ok
}

func (n *customNetworkCache) put(name string, network *networking.NCProxyNetwork) {
	n.rw.Lock()
	defer n.rw.Unlock()
	n.cache[name] = network
}

func (n *customNetworkCache) delete(name string) {
	n.rw.Lock()
	defer n.rw.Unlock()
	delete(n.cache, name)
}

func (n *customNetworkCache) getAll() []*networking.NCProxyNetwork {
	n.rw.RLock()
	defer n.rw.RUnlock()
	copyCache := []*networking.NCProxyNetwork{}
	for _, network := range n.cache {
		copyCache = append(copyCache, network)
	}
	return copyCache
}

type customEndpointCache struct {
	// lock for synchronizing read/write access to `cache`
	rw    sync.RWMutex
	cache map[string]*networking.NCProxyEndpoint
}

func newCustomEndpointCache() *customEndpointCache {
	return &customEndpointCache{
		cache: make(map[string]*networking.NCProxyEndpoint),
	}
}

func (e *customEndpointCache) get(name string) (*networking.NCProxyEndpoint, bool) {
	e.rw.RLock()
	defer e.rw.RUnlock()
	result, ok := e.cache[name]
	return result, ok
}

func (e *customEndpointCache) put(name string, endpoint *networking.NCProxyEndpoint) {
	e.rw.Lock()
	defer e.rw.Unlock()
	e.cache[name] = endpoint
}

func (e *customEndpointCache) delete(name string) {
	e.rw.Lock()
	defer e.rw.Unlock()
	delete(e.cache, name)
}

func (e *customEndpointCache) getAll() []*networking.NCProxyEndpoint {
	e.rw.RLock()
	defer e.rw.RUnlock()
	copyCache := []*networking.NCProxyEndpoint{}
	for _, endpoint := range e.cache {
		copyCache = append(copyCache, endpoint)
	}
	return copyCache
}
