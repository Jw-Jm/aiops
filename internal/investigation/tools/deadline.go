package tools

import "time"

// DeadlineFor bounds one MCP tool execution. The bound belongs to the tool
// class, not to the caller: a nested helper that consumed the whole bound would
// turn a degradable auxiliary read into a failed step, so every nested bound
// must be strictly below the owning tool's deadline.
func DeadlineFor(name string) time.Duration {
	switch name {
	case "query_deepflow":
		// Includes the bounded native/source identity fence and reply.
		return 9 * time.Second
	default:
		return 3 * time.Second
	}
}
