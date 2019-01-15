// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kudzuv1alpha1 "kudzu.sh/api/kudzu/v1alpha1"
	apicontroller "kudzu.sh/kudzu/controller/api"
)

type Config struct {
	ProductionLogging bool
	Cluster           ClusterConfig
}

func (c *Config) Expose(app *kingpin.Application) {
	app.Flag("production", "Use production log behavior").Short('p').BoolVar(&c.ProductionLogging)
	c.Cluster.Expose(app)
}

func (c *Config) Logger() (log *zap.Logger) {
	var err error

	if c.ProductionLogging {
		log, err = zap.NewProduction()
	} else {
		log, err = zap.NewDevelopment()
	}

	if err != nil {
		panic(fmt.Sprintf("Failed to create logger: %v", err))
	}

	return log
}

func main() {
	conf := Config{}

	app := kingpin.New("kudzu", "Kudzu API and Operator controller")
	app.DefaultEnvars()
	conf.Expose(app)

	kingpin.MustParse(app.Parse(os.Args[1:]))
	log := conf.Logger()
	defer log.Sync()

	log.Info("Initializing")

	mgr, err := manager.New(conf.Cluster.RESTConfig(log), manager.Options{})
	if err != nil {
		log.Fatal("Failed to create controller-runtime manager", zap.Error(err))
	}

	scheme := mgr.GetScheme()
	if err := kudzuv1alpha1.AddToScheme(scheme); err != nil {
		log.Fatal("Failed to add API types to scheme", zap.Error(err))
	}
	if err := kudzuv1alpha1.RegisterDefaults(scheme); err != nil {
		log.Fatal("Failed to add API defaults to scheme", zap.Error(err))
	}

	ctx := CancelOnSignal(context.Background(), log)

	apicontroller.Build(ctx, log, mgr)

	log.Info("Starting manager")
	if err := mgr.Start(ctx.Done()); err != nil {
		log.Fatal("Failed to start controller-runtime manager", zap.Error(err))
	}

	log.Info("Shutdown complete")
}
