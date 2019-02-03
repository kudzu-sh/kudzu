// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package api

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	core "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kudzu "kudzu.sh/api/kudzu/v1alpha1"
	"kudzu.sh/kudzu/controller/util"
	"kudzu.sh/kudzu/delegate"
)

var (
	LabelAPI       = fmt.Sprintf("%s/api", kudzu.GroupName)
	LabelManagedBy = "app.kubernetes.io/managed-by"

	FinalizerName = fmt.Sprintf("%s/api", kudzu.GroupName)
)

func Build(ctx context.Context, log *zap.Logger, mgr manager.Manager, dc delegate.Config, rs delegate.ResultStorage) {
	log = log.With(zap.String("reconciler", "api"))
	clog.SetLogger(zapr.NewLogger(log))

	_, err := builder.SimpleController().
		WithManager(mgr).
		ForType(&kudzu.API{}).
		Owns(&core.Pod{}).
		Owns(&apiext.CustomResourceDefinition{}).
		Build(&Reconciler{
			Delegates: &delegate.Manager{Config: dc},
			Results:   rs,
			ctx:       ctx,
			log:       log,
		})
	if err != nil {
		log.Fatal("Could not build controller", zap.Error(err))
	}
}

type Reconciler struct {
	Delegates *delegate.Manager
	Results   delegate.ResultStorage

	client.Client
	scheme *runtime.Scheme
	ctx    context.Context
	log    *zap.Logger
}

func (r *Reconciler) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()

	log := r.log.With(zap.String("api", req.Name))
	log.Debug("Reconciliation requested")

	api := &kudzu.API{}
	err := r.Get(ctx, req.NamespacedName, api)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !api.DeletionTimestamp.IsZero() {
		return r.Finalize(ctx, log, api)
	}

	return r.Sync(ctx, log, api)
}

func (r *Reconciler) Sync(ctx context.Context, log *zap.Logger, api *kudzu.API) (reconcile.Result, error) {
	crds, err := r.crds(ctx, api)
	if err != nil {
		return reconcile.Result{}, err
	}

	needsUpdate := false

	if len(api.Status.Conditions) == 0 {
		api.Status.InitializeConditions()
		needsUpdate = true
	}

	needsDelegate, err := r.evaluate(ctx, log, api, crds)
	if err != nil {
		return reconcile.Result{}, err
	}
	if needsDelegate {
		needsUpdate = true
	}

	del, err := r.Delegates.Ensure(ctx, api, "reify-api", map[string]string{}, needsDelegate)
	if err != nil {
		return reconcile.Result{}, err
	}

	if del.Pod != nil {
		api.Status.ConditionManager().MarkFalse(
			kudzu.APIUpdated,
			"Updating",
			"Created delegate Pod: %s/%s",
			del.Pod.Namespace,
			del.Pod.Name,
		)
		needsUpdate = true
	}

	crdList := apiext.CustomResourceDefinitionList{}
	err = r.Delegates.Result(del, r.Results, &crdList)
	if err != delegate.ErrNotFound {
		if err != nil {
			// TODO: check if ErrorResult
			log.Error("Error getting delegate result", zap.Error(err))
			return reconcile.Result{}, err
		}

		if err := r.process(ctx, log, api, crdList.Items); err != nil {
			log.Error("Error processing delegate result", zap.Error(err))
			return reconcile.Result{}, err
		}
		needsUpdate = true

		if err := r.Delegates.Commit(ctx, del); err != nil {
			return reconcile.Result{}, err
		}
	}

	if api.Status.ResourceCount != int32(len(api.Status.Resources)) {
		api.Status.ResourceCount = int32(len(api.Status.Resources))
		needsUpdate = true
	}

	if needsUpdate {
		if err := r.Status().Update(ctx, api); err != nil {
			log.Error("Error updating API status", zap.Error(err))
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) evaluate(
	ctx context.Context,
	log *zap.Logger,
	api *kudzu.API,
	crds []apiext.CustomResourceDefinition,
) (bool, error) {
	if len(api.Status.Resources) == 0 {
		log.Info("New API added")
		api.Status.ConditionManager().MarkFalse(kudzu.APIApplied, "New", "API is newly added")
		return true, nil
	}

	requested := map[string]bool{}
	extant := map[string]bool{}
	for _, r := range api.Status.Resources {
		requested[r.Name] = true
	}
	for _, crd := range crds {
		extant[crd.Name] = true
	}

	needsDelegate := false
	for _, r := range api.Status.Resources {
		if !extant[r.Name] {
			log.Info("CRD missing from cluster", zap.String("crd", r.Name))
			api.Status.ConditionManager().MarkFalse(kudzu.APIApplied, "Incomplete", "Some requested CRDs are missing")
			needsDelegate = true
		}
	}
	for _, crd := range crds {
		if !requested[crd.Name] && crd.DeletionTimestamp.IsZero() {
			log.Info("CRD not part of API; deleting", zap.String("crd", crd.Name))
			if err := r.Delete(ctx, &crd, client.Preconditions(&meta.Preconditions{UID: &crd.UID})); err != nil {
				log.Error("Failed to delete CRD", zap.String("crd", crd.Name), zap.Error(err))
				return needsDelegate, err
			}
		}
	}

	return needsDelegate, nil
}

func (r *Reconciler) process(
	ctx context.Context,
	log *zap.Logger,
	api *kudzu.API,
	crds []apiext.CustomResourceDefinition,
) error {
	names := make([]string, 0, len(crds))
	for _, crd := range crds {
		names = append(names, crd.Name)
	}
	log.Info("Received CRDs from delegate", zap.Strings("crds", names))

	labels := crdLabels(api)
	for _, crd := range crds {
		for name, label := range labels {
			crd.Labels[name] = label
		}
		if err := controllerutil.SetControllerReference(api, &crd, r.scheme); err != nil {
			return err
		}

		if err := r.Create(ctx, &crd); err != nil {
			if !errors.IsAlreadyExists(err) {
				log.Error("Error creating CRD", zap.String("crd", crd.Name), zap.Error(err))
				return err
			}

			// TODO: do we want to respect what's already there?
			if err := r.Update(ctx, &crd); err != nil {
				log.Error("Error updating CRD", zap.String("crd", crd.Name), zap.Error(err))
				return err
			} else {
				log.Info("Updated CRD", zap.String("crd", crd.Name))
			}
		} else {
			log.Info("Created CRD", zap.String("crd", crd.Name))
		}
	}

	resourceStatuses := make([]kudzu.ResourceStatus, 0, len(crds))
	for _, crd := range crds {
		resourceStatuses = append(resourceStatuses, statusForCRD(&crd))
	}
	api.Status.Resources = resourceStatuses

	cm := api.Status.ConditionManager()
	cm.MarkTrue(kudzu.APIApplied)
	cm.MarkTrue(kudzu.APIUpdated)

	return nil
}

func (r *Reconciler) Finalize(ctx context.Context, log *zap.Logger, api *kudzu.API) (reconcile.Result, error) {
	crds, err := r.crds(ctx, api)
	if err != nil {
		return reconcile.Result{}, err
	}

	if len(crds) > 0 {
		log.Info("Finalizing; deleting CRDs")
	}

	for _, crd := range crds {
		if !crd.DeletionTimestamp.IsZero() {
			continue
		}

		if err := r.Delete(ctx, &crd, client.Preconditions(&meta.Preconditions{UID: &crd.UID})); err != nil {
			log.Error("Failed to delete CRD", zap.String("crd", crd.Name), zap.Error(err))
			return reconcile.Result{}, err
		}
	}

	if crds, err := r.crds(ctx, api); err != nil && len(crds) == 0 {
		finalizers, changed := util.RemoveFinalizer(api.Finalizers)
		if changed {
			api.Finalizers = finalizers
			if err := r.Update(ctx, api); err != nil {
				log.Error("Failed to remove API finalizer", zap.Error(err))
				return reconcile.Result{}, err
			}
		}

		log.Info("API finalized")
		return reconcile.Result{}, nil
	}

	return reconcile.Result{Requeue: true, RequeueAfter: 20 * time.Second}, nil
}

func (r *Reconciler) crds(ctx context.Context, api *kudzu.API) ([]apiext.CustomResourceDefinition, error) {
	selector := labels.SelectorFromSet(labels.Set(crdLabels(api)))
	opts := client.ListOptions{LabelSelector: selector}

	list := apiext.CustomResourceDefinitionList{}
	if err := r.List(ctx, &opts, &list); err != nil {
		return nil, err
	}

	return list.Items, nil
}

func (r *Reconciler) InjectClient(c client.Client) error {
	r.Client = c
	return r.Delegates.InjectClient(c)
}

func (r *Reconciler) InjectScheme(scheme *runtime.Scheme) error {
	r.scheme = scheme
	return r.Delegates.InjectScheme(scheme)
}

func crdLabels(api *kudzu.API) map[string]string {
	return map[string]string{
		LabelAPI:       api.Name,
		LabelManagedBy: "kudzu",
	}
}

func statusForCRD(crd *apiext.CustomResourceDefinition) kudzu.ResourceStatus {
	version := ""
	if len(crd.Spec.Versions) > 0 {
		for _, v := range crd.Spec.Versions {
			if v.Storage {
				version = v.Name
				break
			}
		}
	}
	if version == "" {
		version = crd.Spec.Version
	}

	return kudzu.ResourceStatus{
		Name:    crd.Name,
		Group:   crd.Spec.Group,
		Version: version,
		Kind:    crd.Spec.Names.Kind,
	}
}
