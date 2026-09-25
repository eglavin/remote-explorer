//go:build !windows

package term

import "os"

func enableVT(*os.File) bool { return true }
