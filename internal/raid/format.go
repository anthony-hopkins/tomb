package raid

import (
	"fmt"
	"strconv"
)

// sentence is Sprintf under a name that says what the string is for.
func sentence(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// human writes a big number the way a raider reads one: 1.2M, 340K, 812.
func human(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func itoa(n int) string { return strconv.Itoa(n) }

func formatFloat(x float64) string { return strconv.FormatFloat(x, 'f', 1, 64) }
