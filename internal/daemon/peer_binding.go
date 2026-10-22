package daemon

import (
	"crypto/ed25519"
	"maps"

	peercrypto "github.com/temirlankasen/peerturn/internal/crypto"
	"github.com/temirlankasen/peerturn/internal/fault"
	"github.com/temirlankasen/peerturn/internal/identity"
)

// Index construction rejects ambiguous journals instead of selecting a random
// peer. A route is useful only with its unique, verified session binding.
func validatePeerState(s clientState) error {
	_, err := peerRouteIndex(s)
	return err
}

func peerRouteIndex(s clientState) (map[string]string, error) {
	index := make(map[string]string, len(s.Peers))
	seen := map[string]bool{}
	own, err := identity.VerifyChain(s.Chain, identity.RootFingerprint(s.Chain.RootPublicKey), unixNow())
	if err != nil {
		return nil, err
	}
	remember := func(cert identity.SessionCertificate, device, root, route []byte) bool {
		values := []string{cert.IdentityID, cert.SessionID, publicKey(cert.SigningKey), publicKey(cert.EncryptionKey), publicKey(device), publicKey(root), publicKey(route)}
		for _, value := range values {
			if seen[value] {
				return false
			}
		}
		for _, value := range values {
			seen[value] = true
		}
		return true
	}
	remember(own, ed25519.NewKeyFromSeed(s.DeviceSeed)[ed25519.SeedSize:], s.Chain.RootPublicKey, ed25519.NewKeyFromSeed(s.RoutingSeed)[ed25519.SeedSize:])
	for id, p := range s.Peers {
		if p == nil || len(p.RoutingKey) != ed25519.PublicKeySize || id != peerID(p.Chain) {
			return nil, fault.New(fault.Conflict)
		}
		// Expiry does not release a routing binding for reuse by another peer.
		cert, device, verifyErr := historicalBinding(p.Chain)
		if verifyErr != nil || !remember(cert, device, p.Chain.RootPublicKey, p.RoutingKey) {
			return nil, fault.New(fault.Conflict)
		}
		index[routePermit(p.RoutingKey)] = id
	}
	return index, nil
}

func historicalBinding(chain identity.Chain) (identity.SessionCertificate, []byte, error) {
	root := identity.RootFingerprint(chain.RootPublicKey)
	raw, err := peercrypto.Verify(chain.Device, root, peercrypto.DeviceType, chain.RootPublicKey)
	if err != nil {
		return identity.SessionCertificate{}, nil, err
	}
	device, err := identity.DecodeDevice(raw)
	if err != nil {
		return identity.SessionCertificate{}, nil, err
	}
	raw, err = peercrypto.Verify(chain.Session, identity.RootFingerprint(device.SigningKey), peercrypto.SessionType, device.SigningKey)
	if err != nil {
		return identity.SessionCertificate{}, nil, err
	}
	cert, err := identity.DecodeSession(raw)
	if err != nil {
		return cert, nil, err
	}
	cert, err = identity.VerifyChain(chain, root, cert.NotBefore)
	return cert, device.SigningKey, err
}

func (d *Daemon) checkPeerBinding(chain identity.Chain, route []byte) error {
	if _, exists := d.state.Peers[peerID(chain)]; exists {
		return fault.New(fault.Conflict)
	}
	s := d.state
	s.Peers = make(map[string]*peerState, len(d.state.Peers)+1)
	maps.Copy(s.Peers, d.state.Peers)
	s.Peers[peerID(chain)] = &peerState{Chain: chain, RoutingKey: route}
	return validatePeerState(s)
}
