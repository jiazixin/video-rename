package storage

import (
	"strings"

	"modernc.org/sqlite"
)

func init() { sqlite.MustRegisterCollationUtf8("NATURAL_NAME", naturalName) }

// Compare digit runs without integer conversion, including arbitrarily long numbers.
func naturalName(a, b string) int {
	left, right := strings.ToLower(a), strings.ToLower(b)
	for i, j := 0, 0; ; {
		if i == len(left) || j == len(right) {
			if c := strings.Compare(left[i:], right[j:]); c != 0 {
				return c
			}
			return strings.Compare(a, b)
		}
		if left[i] >= '0' && left[i] <= '9' && right[j] >= '0' && right[j] <= '9' {
			x, y := i, j
			for i < len(left) && left[i] >= '0' && left[i] <= '9' {
				i++
			}
			for j < len(right) && right[j] >= '0' && right[j] <= '9' {
				j++
			}
			l, r := strings.TrimLeft(left[x:i], "0"), strings.TrimLeft(right[y:j], "0")
			if len(l) < len(r) {
				return -1
			}
			if len(l) > len(r) {
				return 1
			}
			if c := strings.Compare(l, r); c != 0 {
				return c
			}
		} else {
			if left[i] < right[j] {
				return -1
			}
			if left[i] > right[j] {
				return 1
			}
			i++
			j++
		}
	}
}
