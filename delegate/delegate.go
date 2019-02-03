// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package delegate

import (
	"fmt"
	"strings"

	core "k8s.io/api/core/v1"
)

const (
	roleLabel = "kudzu.sh/role"
	roleValue = "delegate"

	namespace = "kudzu"
)

type Delegate struct {
	Parent    Object
	Pod       *core.Pod
	ConfigMap *core.ConfigMap
}

func (d Delegate) Succeeded() bool {
	return d.Pod != nil && d.Pod.Status.Phase == core.PodSucceeded
}

func delegateName(parent Object) string {
	gvk := parent.GroupVersionKind()
	name := fmt.Sprintf("delegate-%s-%s", strings.ToLower(gvk.Kind), parent.GetName())

	// TODO: factor out
	name = strings.Replace(name, ".", "-", -1)
	if len(name) > 63 {
		name = strings.TrimSuffix(name[:63], "-")
	}

	return name
}

func delegateLabels(parent Object) map[string]string {
	gvk := parent.GroupVersionKind()
	return map[string]string{
		roleLabel: roleValue,
		fmt.Sprintf("%s/%s", gvk.Group, strings.ToLower(gvk.Kind)): parent.GetName(),
	}
}

func delegateCallbackURL(baseURL string, parent Object) string {
	gvk := parent.GroupVersionKind()
	return fmt.Sprintf("%s/callbacks/%s/%s/result", baseURL, strings.ToLower(gvk.Kind), parent.GetUID())
}
