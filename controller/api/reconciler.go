// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package api

import (
	"context"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	core "k8s.io/api/core/v1"
	kudzu "kudzu.sh/api/kudzu/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func Build(ctx context.Context, log *zap.Logger, mgr manager.Manager) {
	log = log.With(zap.String("reconciler", "api"))
	clog.SetLogger(zapr.NewLogger(log))

	_, err := builder.SimpleController().
		WithManager(mgr).
		ForType(&kudzu.API{}).
		Owns(&core.Pod{}).
		Build(&Reconciler{ctx: ctx, log: log})
	if err != nil {
		log.Fatal("Could not build controller", zap.Error(err))
	}
}

type Reconciler struct {
	client.Client
	ctx context.Context
	log *zap.Logger
}

func (r *Reconciler) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()

	log := r.log.With(zap.String("api", req.NamespacedName.Name))
	log.Debug("Reconciliation requested")

	api := kudzu.API{}
	err := r.Get(ctx, req.NamespacedName, &api)
	if err != nil {
		return reconcile.Result{}, err
	}

	log.Info("Reconciling", zap.String("source", api.Spec.Source.Image.Repository))

	return reconcile.Result{}, nil
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return nil
}
