package store

import "strings"

func messageStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "read", "played":
		return 5
	case "delivered":
		return 4
	case "sent", "server_ack", "server ack", "ack":
		return 3
	case "pending", "queued", "sending":
		return 2
	case "failed", "error":
		return 1
	default:
		return 0
	}
}
