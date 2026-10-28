// Package pagination bounds offset-based list windows.
package pagination

// Offset clamps a row offset to the last nonempty window without overflowing.
// Limit must be positive. Empty lists always start at zero.
func Offset[T ~int | ~int64](offset, total, limit T) T {
	return min(max(0, offset), max(0, total-1)/limit*limit)
}
