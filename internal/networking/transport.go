package networking

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"
)

func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: proxyFromEnv,
		DialContext: (&net.Dialer{
			Timeout:   defaultTimeout,
			KeepAlive: defaultTimeout,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       100,
		ResponseHeaderTimeout: 15 * time.Second,
		DisableCompression:    false,
	}
}

// NewDownloadTransport returns a transport that only speaks HTTP/1.1.
// Twitter/X CDNs close HTTP/2 streams mid-body during parallel ranged
// downloads (PROTOCOL_ERROR); API clients keep the HTTP/2 transport.
func NewDownloadTransport() *http.Transport {
	transport := NewTransport()
	transport.ForceAttemptHTTP2 = false
	// empty map disables HTTP/2 negotiated via ALPN
	transport.TLSNextProto = map[string]func(authority string, c *tls.Conn) http.RoundTripper{}
	return transport
}

func NewTransportNoProxyFromEnv() *http.Transport {
	transport := NewTransport()
	transport.Proxy = func(_ *http.Request) (*url.URL, error) {
		return nil, nil
	}
	return transport
}

func NewTransportWithProxy(proxyURL *url.URL) *http.Transport {
	transport := NewTransport()
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport
}

func NewDownloadTransportNoProxyFromEnv() *http.Transport {
	transport := NewDownloadTransport()
	transport.Proxy = func(_ *http.Request) (*url.URL, error) {
		return nil, nil
	}
	return transport
}

func NewDownloadTransportWithProxy(proxyURL *url.URL) *http.Transport {
	transport := NewDownloadTransport()
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport
}
