package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Backend represents a backend server
type Backend struct {
	URL          *url.URL
	Alive        bool
	mux          sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

// SetAlive updates the alive status of a backend
func (b *Backend) SetAlive(alive bool) {
	b.mux.Lock()
	b.Alive = alive
	b.mux.Unlock()
}

// IsAlive returns true when backend is Alive
func (b *Backend) IsAlive() (alive bool) {
	b.mux.RLock()
	alive = b.Alive
	b.mux.RUnlock()
	return
}

// LoadBalancer represents a load balancer
type LoadBalancer struct {
	backends []*Backend
	current  uint64
}

// NextBackend returns the next available backend to handle the request
func (lb *LoadBalancer) NextBackend() *Backend {
	// simple round-robin
	next := atomic.AddUint64(&lb.current, uint64(1)) % uint64(len(lb.backends))
	// Find the next available backend
	for i := 0; i < len(lb.backends); i++ {
		idx := (int(next) + i) % len(lb.backends)
		if lb.backends[idx].IsAlive() {
			return lb.backends[idx]
		}
	}
	return nil
}

// ================================== HEALTH CHECKING =============================== ///
// isBackendAlive checks whether a backend is alive by establishing a TCP connection
func isBackendAlive(u *url.URL) bool {
	timeout := 2 * time.Second
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		log.Printf("Site unreachable: %s", err)
		return false
	}

	defer conn.Close()
	return true
}

// HealthCheck pings the backend and updates thier status
func (lb *LoadBalancer) HealthCheck() {
	for _, b := range lb.backends {
		status := isBackendAlive(b.URL)
		b.SetAlive(status)
		if status {
			log.Printf("Backend %s is alive", b.URL)
		} else {
			log.Printf("Backend %s is dead", b.URL)
		}
	}
}

// HealthCheckPeriodically runs a routine health check every interval
func (lb *LoadBalancer) HealthCheckPeriodically(interval time.Duration) {
	t := time.NewTicker(interval)
	for {
		select {
		case <-t.C:
			lb.HealthCheck()
		}
	}
}

// ================================== HTTP HANDLER =============================== ///
// ServeHTTP implements the http.Handler interface for the LoadBalancer
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend := lb.NextBackend()
	if backend == nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	// Forward the request to the backend
	backend.ReverseProxy.ServeHTTP(w, r)
}

// ================================== MAIN PROGRAM =============================== ///
func main() {
	// Parse command line flags
	port := flag.Int("port", 8080, "Port to serve on")
	flag.Parse()

	// Configure backends
	serverList := []string{
		"http://localhost:8081",
		"http://localhost:8082",
		"http://localhost:8083",
	}

	// create Load balancer
	lb := LoadBalancer{}

	// initialize backends
	for _, serverURL := range serverList {
		url, err := url.Parse(serverURL)
		if err != nil {
			log.Fatal(err)
		}

		proxy := httputil.NewSingleHostReverseProxy(url)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Error: %v", err)
			http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		}

		lb.backends = append(lb.backends, &Backend{
			URL:          url,
			Alive:        true,
			ReverseProxy: proxy,
		})

		log.Printf("Configured backend: %s", url)
	}

	// Initial health check
	lb.HealthCheck()

	// Start periodic health check
	go lb.HealthCheckPeriodically(time.Minute)

	// Start server
	server := http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: &lb,
	}

	log.Printf("Load Balancer started at: %d\n", *port)

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
