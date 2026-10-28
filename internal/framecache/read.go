package framecache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const cacheFileLimit = 1 << 20

// Both manifests and individual JPEGs are bounded, including when a file grows
// between validation and reading. OpenInRoot confines manifest-selected paths.
func readCacheFile(ctx context.Context, dir, name string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenInRoot(dir, name)
	if err != nil {
		return nil, fmt.Errorf("open cached file %q: %w", name, err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > cacheFileLimit {
		return nil, errors.New("cached file is not a bounded regular file")
	}
	data, err = io.ReadAll(io.LimitReader(file, cacheFileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > cacheFileLimit {
		return nil, errors.New("cached file exceeds size limit")
	}
	return data, ctx.Err()
}
