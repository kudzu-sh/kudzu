// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package delegate

import (
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kudzu "kudzu.sh/api/kudzu/v1alpha1"
)

// Object is a Kubernetes resource that can have delegate Pods spawned on its behalf.
type Object interface {
	runtime.Object
	meta.Object

	GroupVersionKind() schema.GroupVersionKind

	GetSourceSpec() *kudzu.SourceSpec
	GetSourceStatus() *kudzu.SourceStatus
}
