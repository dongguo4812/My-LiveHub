package desktop

type processParent struct {
	PID       uint32
	ParentPID uint32
}

// collectDescendantProcessIDs returns only processes reachable from the
// application PID through parent-process links. Matching executable names
// alone is intentionally insufficient because other applications may host
// their own WebView2 runtimes.
func collectDescendantProcessIDs(rootPID uint32, processes []processParent) map[uint32]struct{} {
	children := make(map[uint32][]uint32, len(processes))
	for _, process := range processes {
		children[process.ParentPID] = append(children[process.ParentPID], process.PID)
	}
	owned := make(map[uint32]struct{})
	queue := append([]uint32(nil), children[rootPID]...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid == 0 {
			continue
		}
		if _, exists := owned[pid]; exists {
			continue
		}
		owned[pid] = struct{}{}
		queue = append(queue, children[pid]...)
	}
	return owned
}
