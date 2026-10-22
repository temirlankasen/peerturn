package auth

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

func requestTarget(r *http.Request, proxies []netip.Prefix, signing bool, expectedAuthority string) (string, string, error) {
	if r == nil || r.URL == nil || r.URL.User != nil || r.URL.Fragment != "" || r.URL.Opaque != "" || r.Method == "" || strings.ContainsAny(r.Method, " \t\r\n") || r.Header.Get("Host") != "" {
		return "", "", ErrUnauthorized
	}
	authority := r.Host
	if authority == "" {
		authority = r.URL.Host
	}
	if r.URL.Host != "" && r.URL.Host != authority {
		return "", "", ErrUnauthorized
	}
	secure := r.TLS != nil
	if signing {
		secure = r.URL.Scheme == "https"
	} else if trustedPeer(r.RemoteAddr, proxies) && hasProxyHeaders(r.Header) {
		if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Port") != "" {
			return "", "", ErrUnauthorized
		}
		proto, err := singleField(r.Header, "X-Forwarded-Proto", 16)
		if err != nil || proto != "https" {
			return "", "", ErrUnauthorized
		}
		authority, err = singleField(r.Header, "X-Forwarded-Host", 256)
		if err != nil {
			return "", "", ErrUnauthorized
		}
		secure = true
	}
	if !secure || (r.URL.Scheme != "" && r.URL.Scheme != "https") || !ValidAuthority(authority) || (!signing && authority != expectedAuthority) {
		return "", "", ErrUnauthorized
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path+r.URL.RawQuery, "\r\n#") || len(path)+len(r.URL.RawQuery) > 8192 {
		return "", "", ErrUnauthorized
	}
	target := r.URL.RequestURI()
	if r.RequestURI != "" && r.RequestURI != target {
		return "", "", ErrUnauthorized
	}
	return authority, "https://" + authority + target, nil
}

func trustedPeer(remote string, proxies []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return false
	}
	for _, prefix := range proxies {
		if prefix.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func hasProxyHeaders(header http.Header) bool {
	for key := range header {
		name := strings.ToLower(key)
		if name == "forwarded" || strings.HasPrefix(name, "x-forwarded-") {
			return true
		}
	}
	return false
}

// ValidAuthority accepts the exact canonical external authority used in signatures.
func ValidAuthority(authority string) bool {
	if authority == "" || len(authority) > 256 || authority != strings.ToLower(authority) || strings.ContainsAny(authority, ", /\\@?#%\t\r\n") {
		return false
	}
	u, err := url.Parse("https://" + authority)
	if err != nil || u.Host != authority || u.User != nil || u.Path != "" || u.Hostname() == "" {
		return false
	}
	if strings.HasSuffix(authority, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || n == 443 || strconv.Itoa(n) != port {
			return false
		}
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == "" && ip.String() == host && (ip.Is6() || !strings.HasPrefix(authority, "["))
	}
	if len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}
