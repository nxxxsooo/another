//go:build !darwin

package tui

func platformInputSourceAPI() inputSourceAPI { return nil }
