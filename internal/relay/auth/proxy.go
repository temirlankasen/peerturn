package auth

import "net/netip"

// ValidTrustedProxyPrefix confines forwarded-header trust to a canonical subnet
// wholly inside a private or loopback range. Public proxy networks are unsupported.
func ValidTrustedProxyPrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() || prefix != prefix.Masked() {
		return false
	}
	for _, boundary := range []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 0, 0, 0}), 8),
		netip.PrefixFrom(netip.AddrFrom4([4]byte{172, 16, 0, 0}), 12),
		netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 168, 0, 0}), 16),
		netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("fc00::/7"),
	} {
		if boundary.Addr().BitLen() == prefix.Addr().BitLen() && prefix.Bits() >= boundary.Bits() && boundary.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}
