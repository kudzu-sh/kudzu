// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	core "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kudzu "kudzu.sh/api/kudzu/v1alpha1"
	"kudzu.sh/kudzu/controller/util"
	"kudzu.sh/kudzu/delegate"
)

var (
	validImage = &kudzu.ImageSpec{Repository: "quay.io/lady"}
)

func init() {
	log, _ := zap.NewDevelopment()
	defer log.Sync()

	if err := apiext.AddToScheme(scheme.Scheme); err != nil {
		log.Fatal("Failed to add CRD types to scheme", zap.Error(err))
	}
	if err := kudzu.AddToScheme(scheme.Scheme); err != nil {
		log.Fatal("Failed to add API types to scheme", zap.Error(err))
	}
	if err := kudzu.RegisterDefaults(scheme.Scheme); err != nil {
		log.Fatal("Failed to add API defaults to scheme", zap.Error(err))
	}
}

func reconciler(t *testing.T, objs ...runtime.Object) *Reconciler {
	sch := scheme.Scheme
	client := fake.NewFakeClientWithScheme(sch, objs...)

	mgr := delegate.Manager{}
	rec := &Reconciler{
		Delegates: &mgr,
		Results:   &delegate.MemoryResultStorage{},
		ctx:       context.Background(),
		log:       zaptest.NewLogger(t),
	}

	rec.InjectClient(client)
	rec.InjectScheme(sch)
	return rec
}

func run(r *Reconciler, name string) (reconcile.Result, error) {
	return r.Reconcile(reconcile.Request{NamespacedName: id(name)})
}

func id(name string) types.NamespacedName {
	return types.NamespacedName{Name: name}
}

func api(
	name string,
	image *kudzu.ImageSpec,
	imageStatus *kudzu.ImageStatus,
	resources []kudzu.ResourceStatus,
) *kudzu.API {
	var sourceStatus *kudzu.SourceStatus
	if imageStatus != nil {
		sourceStatus = &kudzu.SourceStatus{Image: imageStatus}
	}

	return &kudzu.API{
		TypeMeta: meta.TypeMeta{
			APIVersion: kudzu.GroupVersion.String(),
			Kind:       "API",
		},
		ObjectMeta: meta.ObjectMeta{
			Name: name,
		},
		Spec: kudzu.APISpec{
			Source: kudzu.SourceSpec{Image: image},
		},
		Status: kudzu.APIStatus{
			Source:    sourceStatus,
			Resources: resources,
		},
	}
}

func TestInitializeConditions(t *testing.T) {
	assert := assert.New(t)
	r := reconciler(t, api("rainicorn", validImage, nil, nil))

	result, err := run(r, "rainicorn")
	assert.NoError(err, "unexpected Reconcile() error")
	assert.Equal(reconcile.Result{}, result, "unexpected Reconcile() result")

	api := kudzu.API{}
	assert.NoError(r.Get(context.Background(), id("rainicorn"), &api), "error getting API")

	assert.NotNil(api.Status.GetCondition(kudzu.APIReady), "Ready condition is nil")
	assert.NotNil(api.Status.GetCondition(kudzu.APIApplied), "Applied condition is nil")
	assert.NotNil(api.Status.GetCondition(kudzu.APIUpdated), "Updated condition is nil")

	assert.Equal(core.ConditionFalse, api.Status.GetCondition(kudzu.APIReady).Status, "Ready condition has wrong status")
	assert.Equal(core.ConditionFalse, api.Status.GetCondition(kudzu.APIApplied).Status, "Applied condition has wrong status")
	assert.Equal("New", api.Status.GetCondition(kudzu.APIApplied).Reason, "Applied condition has wrong reason")
	assert.Equal(core.ConditionFalse, api.Status.GetCondition(kudzu.APIUpdated).Status, "Updated condition has wrong status")
	assert.Equal("Updating", api.Status.GetCondition(kudzu.APIUpdated).Reason, "Updated condition has wrong reason")
}

func TestAddFinalizer(t *testing.T) {
	assert := assert.New(t)
	r := reconciler(t, api("rainicorn", validImage, nil, nil))

	result, err := run(r, "rainicorn")
	assert.NoError(err, "unexpected Reconcile() error")
	assert.Equal(reconcile.Result{}, result, "unexpected Reconcile() result")

	api := kudzu.API{}
	assert.NoError(r.Get(context.Background(), id("rainicorn"), &api), "error getting API")

	assert.Contains(api.Finalizers, util.FinalizerName, "reconciler did not add finalizer")
}

/*
func TestSetControllerReference(t *testing.T) {
	assert := assert.New(t)
	r := reconciler(t, api("rainicorn", validImage, nil, nil))

	result, err := run(r, "rainicorn")
	assert.NoError(err, "unexpected Reconcile() error")
	assert.Equal(reconcile.Result{}, result, "unexpected Reconcile() result")

	api := kudzu.API{}
	assert.NoError(r.Get(context.Background(), id("rainicorn"), &api), "error getting API")

	controller := true
	assert.Contains(
		api.OwnerReferences,
		meta.OwnerReference{
			APIVersion: kudzu.GroupVersion.String(),
			Kind:       "API",
			Name:       "rainicorn",
			Controller: &controller,
		},
		"reconciler did not add finalizer",
	)
}
*/

func TestCreateInitialPod(t *testing.T) {
	assert := assert.New(t)
	r := reconciler(t, api("rainicorn", validImage, nil, nil))

	result, err := run(r, "rainicorn")
	assert.NoError(err, "unexpected Reconcile() error")
	assert.Equal(reconcile.Result{}, result, "unexpected Reconcile() result")

	pod := core.Pod{}
	assert.NoError(r.Get(context.Background(), types.NamespacedName{Namespace: "kudzu", Name: "delegate-api-rainicorn"}, &pod), "error getting pod")

	assert.Equal("delegate", pod.Labels["kudzu.sh/role"], "unexpected role label value")
	assert.Equal("rainicorn", pod.Labels["kudzu.sh/api"], "unexpected api label value")

	assert.Len(pod.Spec.InitContainers, 1, "unexpected InitContainers length")
	reify := pod.Spec.InitContainers[0]
	assert.Equal(fmt.Sprintf("%s:latest", validImage.Repository), reify.Image, "unexpected reify image")
	assert.Equal([]string{"/opt/kudzu/bin/reify-api"}, reify.Command, "unexpected reify command")

	assert.Len(pod.Spec.Containers, 1, "unexpected Containers length")
}
