//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

const defaultConfigPath = "/etc/replicate.yml"
const legacyConfigPath = "/etc/litestream.yml"

func isWindowsService() (bool, error) {
	return false, nil
}

func runWindowsService(ctx context.Context) error {
	panic("cannot run windows service as unix process")
}

func signalChan() <-chan os.Signal {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	return ch
}
