//go:build !windows

package safepath

func hasVolumePrefix(string) bool { return false }

func validPlatformName(string) error { return nil }
