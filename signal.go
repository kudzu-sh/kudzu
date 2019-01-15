// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

// CancelOnSignal will cancel the given context when the process receives SIGINT
// or SIGTERM. A second signal will exit immediately.
func CancelOnSignal(ctx context.Context, log *zap.Logger) context.Context {
	ctx, cancel := context.WithCancel(ctx)

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		log.Info("Caught signal; starting graceful shutdown", zap.Stringer("signal", sig))
		cancel()

		sig = <-sigs
		log.Fatal("Caught second signal; exiting gracelessly", zap.Stringer("signal", sig))
	}()

	return ctx
}
