// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package util

import (
	"fmt"

	kudzu "kudzu.sh/api/kudzu/v1alpha1"
)

var (
	FinalizerName = fmt.Sprintf("%s/api", kudzu.GroupName)
)

func HasFinalizer(finalizers []string) bool {
	for _, f := range finalizers {
		if f == FinalizerName {
			return true
		}
	}
	return false
}

func EnsureFinalizer(finalizers []string) ([]string, bool) {
	if HasFinalizer(finalizers) {
		return finalizers, false
	}
	return append(finalizers, FinalizerName), true
}

func RemoveFinalizer(finalizers []string) ([]string, bool) {
	if !HasFinalizer(finalizers) {
		return finalizers, false
	}
	updated := make([]string, 0, len(finalizers)-1)
	for _, f := range finalizers {
		if f != FinalizerName {
			updated = append(updated, f)
		}
	}
	return updated, true
}
