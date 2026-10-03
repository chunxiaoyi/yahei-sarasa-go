package fetch

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProxyMode values understood by NewClient.
const (
	ProxyAuto   = ""       // HTTPS_PROXY / HTTP_PROXY env, then the OS proxy setting
	ProxyNone   = "direct" // never use a proxy
	proxySystem = "system"
)

// SystemProxy is set by platform code (Windows: the WinINET "Internet
// Options" proxy most VPN/proxy clients write). It returns "" when none.
var SystemProxy = func() string { return "" }

// NewClient returns an HTTP client for GitHub downloads. proxy is "" (auto),
// "direct", or an explicit proxy such as "http://127.0.0.1:7890" or
// "socks5://127.0.0.1:1080".
func NewClient(proxy string) (*http.Client, string, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSHandshakeTimeout = 20 * time.Second
	t.ResponseHeaderTimeout = 45 * time.Second
	t.DialContext = (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext

	desc := ""
	proxy = strings.TrimSpace(proxy)
	switch {
	case strings.EqualFold(proxy, ProxyNone):
		t.Proxy = nil
		desc = "直连"
	case proxy == "" || strings.EqualFold(proxy, proxySystem):
		envProxy := http.ProxyFromEnvironment
		sys := normalizeProxy(SystemProxy())
		var sysURL *url.URL
		if sys != "" {
			if u, err := url.Parse(sys); err == nil {
				sysURL = u
				desc = "系统代理 " + u.Host
			}
		}
		t.Proxy = func(r *http.Request) (*url.URL, error) {
			if u, err := envProxy(r); u != nil || err != nil {
				return u, err
			}
			return sysURL, nil
		}
		if desc == "" {
			desc = "自动（环境变量）"
		}
	default:
		u, err := url.Parse(normalizeProxy(proxy))
		if err != nil || u.Host == "" {
			return nil, "", fmt.Errorf("代理地址无效：%q（示例 http://127.0.0.1:7890）", proxy)
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, "", fmt.Errorf("不支持的代理协议 %q", u.Scheme)
		}
		t.Proxy = http.ProxyURL(u)
		desc = "代理 " + u.Scheme + "://" + u.Host
	}
	return &http.Client{Transport: t}, desc, nil
}

// normalizeProxy accepts WinINET style values ("host:port" or
// "http=host:port;https=host:port") and bare host:port.
func normalizeProxy(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.Contains(p, "=") {
		entries := map[string]string{}
		for _, part := range strings.Split(p, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok {
				entries[strings.ToLower(k)] = strings.TrimSpace(v)
			}
		}
		switch {
		case entries["https"] != "":
			p = entries["https"]
		case entries["http"] != "":
			p = entries["http"]
		case entries["socks"] != "":
			p = "socks5://" + entries["socks"]
		default:
			return ""
		}
	}
	if !strings.Contains(p, "://") {
		p = "http://" + p
	}
	return p
}
