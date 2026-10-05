package library

import (
	"math/big"
	"path/filepath"
	"strings"

	"video-rename/internal/domain"
	"video-rename/internal/naming"
)

// numberedValue reads the ordinal of a name shaped prefix+digits; names with
// another shape carry no number and join the tail of the sequence.
func numberedValue(name, prefix string) *big.Int {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	base, found := strings.CutPrefix(base, prefix)
	if !found {
		return nil
	}
	if base == "" {
		return nil
	}
	for _, c := range base {
		if c < '0' || c > '9' {
			return nil
		}
	}
	n, ok := new(big.Int).SetString(base, 10)
	if !ok || n.Sign() <= 0 {
		return nil
	}
	return n
}

func padNumber(n *big.Int, width int) string {
	s := n.String()
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// Every format shares one sequence. Unselected files of the same class, and
// the peer class when one sequence is shared, reserve their numbers too.
func numberingNames(selected, outside, peers []Entry, r domain.MediaNumberingRules, peerPrefix string) []NumberingItem {
	max := new(big.Int)
	used := map[string]bool{}
	// Selected files raise the ceiling but never block their own number;
	// outside and peer files reserve theirs.
	scan := func(e Entry, prefix string, reserve bool) {
		if n := numberedValue(e.Name, prefix); n != nil {
			if n.Cmp(max) > 0 {
				max.Set(n)
			}
			if reserve {
				used[n.String()] = true
			}
		}
	}
	for _, e := range selected {
		scan(e, r.Prefix, false)
	}
	for _, e := range outside {
		scan(e, r.Prefix, true)
	}
	for _, e := range peers {
		scan(e, peerPrefix, true)
	}
	// Compact mode assigns fresh consecutive numbers and only steps around
	// numbers kept by files outside this batch.
	next := big.NewInt(0)
	result := make([]NumberingItem, 0, len(selected))
	for _, e := range selected {
		n := numberedValue(e.Name, r.Prefix)
		if r.Compact || n == nil || used[n.String()] {
			if r.Compact {
				next.Add(next, big.NewInt(1))
				for used[next.String()] {
					next.Add(next, big.NewInt(1))
				}
				n = new(big.Int).Set(next)
			} else {
				max.Add(max, big.NewInt(1))
				n = new(big.Int).Set(max)
			}
		}
		used[n.String()] = true
		ext := filepath.Ext(e.Name)
		if r.Lowercase || strings.EqualFold(ext, ".jpeg") {
			ext = naming.NormalizeExtension(ext)
		}
		result = append(result, NumberingItem{Entry: e.ID, Old: e.Name, New: r.Prefix + padNumber(n, r.Padding) + ext, Version: e.Version, Identity: e.Identity, Size: e.Size, Mtime: e.Mtime})
	}
	return result
}

// Plan both classes together so they share one fingerprint and rollback journal.
func mixedNumberingNames(entries, outside []Entry, settings domain.NumberingSettings) []NumberingItem {
	var images, audio, keptImages, keptAudio []Entry
	for _, e := range entries {
		if IsImage(e.Name) {
			images = append(images, e)
		} else {
			audio = append(audio, e)
		}
	}
	for _, e := range outside {
		if IsImage(e.Name) {
			keptImages = append(keptImages, e)
		} else {
			keptAudio = append(keptAudio, e)
		}
	}
	var peers []Entry
	if settings.Shared {
		peers = append(peers, keptAudio...)
		if !settings.Audio.Compact {
			peers = append(peers, audio...)
		}
	}
	items := numberingNames(images, keptImages, peers, settings.Image, settings.Audio.Prefix)
	peers = nil
	if settings.Shared {
		peers = append(peers, keptImages...)
		for _, item := range items {
			peers = append(peers, Entry{Name: item.New})
		}
	}
	return append(items, numberingNames(audio, keptAudio, peers, settings.Audio, settings.Image.Prefix)...)
}
