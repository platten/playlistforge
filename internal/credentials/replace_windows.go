//go:build windows

package credentials

import "golang.org/x/sys/windows"

// replaceFile uses MoveFileEx semantics so updating an existing config works
// atomically on Windows, where os.Rename cannot replace every existing target.
func replaceFile(source, destination string) error { return windows.Rename(source, destination) }
