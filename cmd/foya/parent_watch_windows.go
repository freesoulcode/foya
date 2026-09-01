//go:build windows

package main

func watchParentProcess() <-chan struct{} {
	return nil
}
