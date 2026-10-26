package audit

import "maps"

// RouteActionsForTest exposes a copy to tests that walk the real server router.
// Keeping this bridge in a test file avoids an audit -> server import cycle.
func RouteActionsForTest() map[string]string {
	return maps.Clone(actionMap)
}
