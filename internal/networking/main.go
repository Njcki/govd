package networking

import (
	"net/http"
	"net/url"
	"time"

	"github.com/govdbot/govd/internal/logger"
)

var defaultTimeout = 30 * time.Second

// downloadTimeout covers the full response body for media fetches.
// 30s is enough for API JSON but too short for large X/IG videos from the VPS.
var downloadTimeout = 5 * time.Minute

func NewHTTPClient(options *NewHTTPClientOptions) *HTTPClient {
	if options == nil {
		options = &NewHTTPClientOptions{}
	}
	client := DefaultHTTPClient(options)

	switch {
	case options.Proxy != "":
		proxyURL, err := url.Parse(options.Proxy)
		if err != nil {
			logger.L.Warnf("invalid proxy URL: %v", err)
		} else {
			client.Client = &http.Client{
				Transport: NewTransportWithProxy(proxyURL),
				Timeout:   defaultTimeout,
			}
			client.Proxy = options.Proxy
		}
	case options.EdgeProxy != "":
		client.Client = NewEdgeProxyClient(options.EdgeProxy)
		client.EdgeProxy = options.EdgeProxy
	case options.DisableProxy:
		client.Client = &http.Client{
			Transport: NewTransportNoProxyFromEnv(),
			Timeout:   defaultTimeout,
		}
		client.DisableProxy = true
	}
	if options.Impersonate {
		client.Client = NewChromeClient()
	}

	client.DownloadProxy = options.DownloadProxy
	return client
}

func DefaultHTTPClient(options *NewHTTPClientOptions) *HTTPClient {
	if options == nil {
		options = &NewHTTPClientOptions{}
	}
	return &HTTPClient{
		Client: &http.Client{
			Transport: NewTransport(),
			Timeout:   defaultTimeout,
		},
		Headers: options.Headers,
		Cookies: options.Cookies,
	}
}

func (c *HTTPClient) AsDownloadClient() *HTTPClient {
	client := DefaultHTTPClient(&NewHTTPClientOptions{
		Headers: c.Headers,
		Cookies: c.Cookies,
	})
	// Always allow slow media bodies; API clients keep defaultTimeout.
	client.Client.Timeout = downloadTimeout
	if c.DownloadProxy != "" {
		proxyURL, err := url.Parse(c.DownloadProxy)
		if err != nil {
			logger.L.Warnf("invalid download proxy URL: %v", err)
			return c
		}
		client.Client = &http.Client{
			Transport: NewTransportWithProxy(proxyURL),
			Timeout:   downloadTimeout,
		}
		client.DownloadProxy = c.DownloadProxy
	} else if c.DisableProxy {
		client.Client = &http.Client{
			Transport: NewTransportNoProxyFromEnv(),
			Timeout:   downloadTimeout,
		}
		client.DisableProxy = true
	}
	return client
}
