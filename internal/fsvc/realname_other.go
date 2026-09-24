//go:build !windows

package fsvc

func (s *Service) checkRealName(string) error { return nil }
