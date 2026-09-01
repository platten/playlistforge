//go:build !windows

package credentials

import "os"

// replaceFile uses the atomic same-filesystem rename available on Unix hosts.
func replaceFile(source, destination string) error { return os.Rename(source, destination) }
