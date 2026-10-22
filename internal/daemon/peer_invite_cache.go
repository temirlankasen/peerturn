package daemon

import "container/list"

type peerInviteKey struct{ peer, hash string }

// An LRU list and sender buckets let one sender replace its own entries without
// consuming other senders' budgets. The global cap also bounds bucket count.
type peerInviteCache struct {
	global  list.List
	buckets map[string]map[string]*list.Element
}

func (c *peerInviteCache) add(peer, hash string) {
	if c.buckets == nil {
		c.buckets = make(map[string]map[string]*list.Element)
	}
	bucket := c.buckets[peer]
	if entry := bucket[hash]; entry != nil {
		c.global.MoveToBack(entry)
		return
	}
	if len(bucket) >= maxInviteHashesPerPeer {
		for entry := c.global.Front(); entry != nil; entry = entry.Next() {
			if inviteKey(entry).peer == peer {
				c.remove(entry)
				break
			}
		}
	}
	if c.global.Len() >= maxPeerInviteHashes {
		c.remove(c.global.Front())
	}
	if c.buckets[peer] == nil {
		c.buckets[peer] = make(map[string]*list.Element)
	}
	c.buckets[peer][hash] = c.global.PushBack(peerInviteKey{peer, hash})
}

func (c *peerInviteCache) remove(entry *list.Element) {
	key := inviteKey(entry)
	delete(c.buckets[key.peer], key.hash)
	if len(c.buckets[key.peer]) == 0 {
		delete(c.buckets, key.peer)
	}
	c.global.Remove(entry)
}

func inviteKey(entry *list.Element) peerInviteKey {
	key, ok := entry.Value.(peerInviteKey)
	if !ok {
		panic("invalid peer invitation cache entry")
	}
	return key
}

func (c *peerInviteCache) contains(hash string) bool {
	found := false
	for _, bucket := range c.buckets {
		if entry := bucket[hash]; entry != nil {
			c.global.MoveToBack(entry)
			found = true
		}
	}
	return found
}
