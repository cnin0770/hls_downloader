//go:build !darwin && !linux

package dl

// terminalWidthFD falls back to "unknown" on platforms without a TIOCGWINSZ
// implementation here (e.g. Windows); callers then keep the unlimited-width
// behavior.
func terminalWidthFD(fd uintptr) int {
	return 0
}
