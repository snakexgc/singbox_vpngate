package vpngate

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

func cloneNode(n Node) Node {
	if n.LatencyMs != nil {
		v := *n.LatencyMs
		n.LatencyMs = &v
	}
	if n.HandshakeMs != nil {
		v := *n.HandshakeMs
		n.HandshakeMs = &v
	}
	return n
}
func nodeIdentity(n Node) string {
	if n.IP != "" && n.Port > 0 {
		host := strings.ToLower(strings.TrimSuffix(n.IP, "."))
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		}
		protocol := strings.ToLower(n.Protocol)
		if protocol == "tcp4" || protocol == "tcp-client" || protocol == "tcp4-client" {
			protocol = "tcp"
		}
		return host + ":" + strconv.Itoa(n.Port) + ":" + protocol
	}
	return n.ID
}
func rank(status string) int {
	switch status {
	case "available":
		return 0
	case "untested":
		return 1
	case "testing":
		return 2
	case "canceled":
		return 3
	default:
		return 4
	}
}
func nodeLess(a, b Node) bool {
	if rank(a.Status) != rank(b.Status) {
		return rank(a.Status) < rank(b.Status)
	}
	if (a.LatencyMs == nil) != (b.LatencyMs == nil) {
		return a.LatencyMs != nil
	}
	if a.LatencyMs != nil && *a.LatencyMs != *b.LatencyMs {
		return *a.LatencyMs < *b.LatencyMs
	}
	// Equal measured availability is stable: feed score is not path performance.
	if a.Status == "available" && b.Status == "available" {
		return false
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Speed != b.Speed {
		return a.Speed > b.Speed
	}
	return a.ID < b.ID
}
func NewPool(ns []Node) *Pool {
	p := &Pool{nodes: []Node{}}
	indices := map[string]int{}
	for _, n := range ns {
		key := nodeIdentity(n)
		if i, ok := indices[key]; ok {
			if n.ConfigHash != p.nodes[i].ConfigHash {
				id := p.nodes[i].ID
				if n.Status != "available" || n.LastTested == "" || n.PathFingerprint == "" {
					clearHealth(&n)
				}
				n.ID = id
				p.nodes[i] = cloneNode(n)
			} else if nodeLess(n, p.nodes[i]) {
				p.nodes[i] = cloneNode(n)
			}
			continue
		}
		indices[key] = len(p.nodes)
		p.nodes = append(p.nodes, cloneNode(n))
	}
	sort.SliceStable(p.nodes, func(i, j int) bool { return nodeLess(p.nodes[i], p.nodes[j]) })
	return p
}
func (p *Pool) Snapshot() []Node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ns := make([]Node, len(p.nodes))
	for i, n := range p.nodes {
		ns[i] = cloneNode(n)
	}
	return ns
}

func clearHealth(n *Node) {
	n.Status = "untested"
	n.LatencyMs = nil
	n.HandshakeMs = nil
	n.LastTested = ""
	n.Error = ""
	n.FailureKind = ""
	n.PathFingerprint = ""
}

// Merge refreshes metadata and fills vacancies without evicting existing nodes.
// A changed profile invalidates health while preserving the stable remote ID.
func (p *Pool) Merge(incoming []Node, capacity int, activeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if capacity < 1 {
		return
	}
	seen := map[string]int{}
	for i, n := range p.nodes {
		seen[nodeIdentity(n)] = i
	}
	additions := NewPool(incoming).Snapshot()
	for _, n := range additions {
		key := nodeIdentity(n)
		if i, ok := seen[key]; ok {
			old := p.nodes[i]
			updated := old
			updated.Hostname = n.Hostname
			updated.Country = n.Country
			updated.CountryCode = n.CountryCode
			updated.Score = n.Score
			updated.Speed = n.Speed
			updated.LastSeen = n.LastSeen
			if n.ConfigHash != old.ConfigHash {
				updated.Config = n.Config
				updated.ConfigHash = n.ConfigHash
				clearHealth(&updated)
			}
			p.nodes[i] = cloneNode(updated)
			continue
		}
		if len(p.nodes) >= capacity {
			continue
		}
		seen[key] = len(p.nodes)
		p.nodes = append(p.nodes, n)
	}
	sort.SliceStable(p.nodes, func(i, j int) bool { return nodeLess(p.nodes[i], p.nodes[j]) })
}

// Optimize keeps the best capacity entries, retaining the active connection even if failed.
func (p *Pool) Optimize(capacity int, activeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if capacity < 1 {
		capacity = 1
	}
	sort.SliceStable(p.nodes, func(i, j int) bool { return nodeLess(p.nodes[i], p.nodes[j]) })
	if len(p.nodes) <= capacity {
		return
	}
	for i := capacity; i < len(p.nodes); i++ {
		if p.nodes[i].ID == activeID && activeID != "" {
			p.nodes[capacity-1] = p.nodes[i]
			break
		}
	}
	p.nodes = p.nodes[:capacity]
	sort.SliceStable(p.nodes, func(i, j int) bool { return nodeLess(p.nodes[i], p.nodes[j]) })
}
func (p *Pool) UpdateProbe(result ProbeResult) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.nodes {
		n := &p.nodes[i]
		if n.ID != result.NodeID {
			continue
		}
		switch result.Status {
		case "untested", "testing", "available", "failed", "canceled":
		default:
			return false
		}
		n.Status = result.Status
		n.LatencyMs = result.LatencyMs
		n.HandshakeMs = result.HandshakeMs
		n.LastTested = result.LastTested
		n.Error = result.Error
		n.FailureKind = result.FailureKind
		n.PathFingerprint = result.PathFingerprint
		p.nodes[i] = cloneNode(*n)
		return true
	}
	return false
}
func (p *Pool) Delete(id, activeID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id != "" && id == activeID {
		return fmt.Errorf("cannot delete active node")
	}
	for i, n := range p.nodes {
		if n.ID == id {
			p.nodes = append(p.nodes[:i], p.nodes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("node not found")
}
func (p *Pool) DeleteFailed(activeID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.nodes[:0]
	removed := 0
	for _, n := range p.nodes {
		if n.Status == "failed" && n.ID != activeID {
			removed++
		} else {
			kept = append(kept, n)
		}
	}
	p.nodes = kept
	return removed
}
