package server

import (
	"strings"

	"ecs/internal/config"
)

func uniquePeers(peers []config.Peer) []config.Peer {
	seen := make(map[string]bool)
	result := make([]config.Peer, 0, len(peers))
	for _, peer := range peers {
		endpoint := strings.TrimRight(peer.URL, "/")
		if endpoint == "" {
			continue
		}
		// A node may change address or appear through static, discovered, and
		// authenticated sources at once. Its persistent identity wins over URL.
		key := "url:" + endpoint
		if peer.NodeID != "" {
			key = "node:" + peer.NodeID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, peer)
	}
	return result
}
