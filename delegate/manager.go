// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package delegate

import (
	"context"
	"fmt"
	"strings"

	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kudzu "kudzu.sh/api/kudzu/v1alpha1"
)

const (
	DefaultBaseURL       = "http://kudzu.kudzu.svc.cluster.local"
	DefaultCallbackImage = "kudzutools/callback:latest"

	configAnnotation = "delegate.kudzu.sh/config"
)

type Config struct {
	// CallbackImage is the Docker image that will be used to send results back to Kudzu.
	// Defaults to DefaultCallbackImage.
	CallbackImage string

	// BaseURL is the URL of Kudzu. Defaults to DefaultBaseURL.
	BaseURL string
}

// Manager creates, updates, and deletes delegate Pods and ConfigMaps.
type Manager struct {
	Config

	client client.Client
	scheme *runtime.Scheme
}

func (m *Manager) Get(ctx context.Context, parent Object) (*Delegate, error) {
	selector := labels.SelectorFromSet(labels.Set(delegateLabels(parent)))
	opts := client.ListOptions{Namespace: namespace, LabelSelector: selector}

	pods := core.PodList{}
	if err := m.client.List(ctx, &opts, &pods); err != nil {
		return nil, err
	}

	// TODO: might find multiple pods; look up by name?
	var pod *core.Pod
	if len(pods.Items) > 0 {
		pod = &pods.Items[0]
	}

	configMaps := core.ConfigMapList{}
	if err := m.client.List(ctx, &opts, &configMaps); err != nil {
		return nil, err
	}

	var configMap *core.ConfigMap
	if len(configMaps.Items) > 0 {
		configMap = &configMaps.Items[0]
	}

	if pod != nil || configMap != nil {
		return &Delegate{parent, pod, configMap}, nil
	}

	return nil, nil
}

func (m *Manager) Ensure(
	ctx context.Context,
	parent Object,
	task string,
	config map[string]string,
	invalidated bool,
) (*Delegate, error) {
	source := parent.GetSourceSpec()
	if source.Image == nil || source.Image.Repository == "" {
		return nil, fmt.Errorf("spec for %s doesn't specify an image", parent.GetName())
	}

	confHash, err := configHash(config)
	if err != nil {
		return nil, err
	}

	del, err := m.Get(ctx, parent)
	if err != nil {
		return nil, err
	}
	if del != nil {
		if del.ConfigMap != nil && len(config) == 0 {
			err := m.client.Delete(ctx, del.ConfigMap)
			return nil, err
		}

		if del.ConfigMap != nil && del.ConfigMap.Annotations[configAnnotation] != confHash {
			cm, err := m.mapForConfig(parent, config, confHash)
			if err != nil {
				return nil, err
			}
			if err := m.client.Update(ctx, cm); err != nil {
				return nil, err
			}
			del.ConfigMap = cm
		}

		if confHash != del.Pod.Annotations[configAnnotation] || !source.Matches(parent.GetSourceStatus()) {
			err := m.client.Delete(ctx, del.Pod)
			return nil, err
		}
	} else {
		del = &Delegate{Parent: parent}
	}

	if del.ConfigMap == nil && len(config) > 0 {
		cm, err := m.mapForConfig(parent, config, confHash)
		if err != nil {
			return nil, err
		}
		if err := m.client.Create(ctx, cm); err != nil {
			return nil, err
		}
		del.ConfigMap = cm
	}

	if !invalidated {
		parentAnnotations := parent.GetAnnotations()
		invalidated = confHash != parentAnnotations[configAnnotation] || !source.Matches(parent.GetSourceStatus())
	}
	if del.Pod == nil && invalidated {
		pod, err := m.podForTask(parent, task, confHash)
		if err != nil {
			return nil, err
		}
		if err := m.client.Create(ctx, pod); err != nil {
			return nil, err
		}
		del.Pod = pod
	}

	return del, nil
}

func (m *Manager) Result(del *Delegate, storage ResultStorage, dest interface{}) error {
	if del == nil || del.Pod == nil {
		return ErrNotFound
	}

	return storage.Pop(del.Parent, del.Pod.Annotations[configAnnotation], dest)
}

func (m *Manager) Commit(ctx context.Context, del *Delegate) error {
	if del.Pod == nil {
		panic("Delegate.Pod is nil")
	}

	imageIDParts := strings.Split(del.Pod.Status.InitContainerStatuses[0].ImageID, "@")

	sourceSpec := del.Parent.GetSourceSpec()
	sourceStatus := del.Parent.GetSourceStatus()
	sourceStatus.Image = &kudzu.ImageStatus{
		Repository: sourceSpec.Image.Repository,
		Tag:        sourceSpec.Image.Tag,
		Hash:       imageIDParts[len(imageIDParts)-1],
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		parentAnnotations := del.Parent.GetAnnotations()
		parentAnnotations[configAnnotation] = del.Pod.Annotations[configAnnotation]
		del.Parent.SetAnnotations(parentAnnotations)
		return m.client.Update(ctx, del.Parent)
	})
	if err != nil {
		return err
	}

	if err := m.client.Delete(ctx, del.Pod); err != nil {
		return err
	}
	del.Pod = nil
	if del.ConfigMap != nil {
		if err := m.client.Delete(ctx, del.ConfigMap); err != nil {
			return err
		}
		del.ConfigMap = nil
	}
	return nil
}

func (m *Manager) InjectClient(c client.Client) error {
	m.client = c
	return nil
}

func (m *Manager) InjectScheme(scheme *runtime.Scheme) error {
	m.scheme = scheme
	return nil
}

func (m *Manager) podForTask(parent Object, task string, confHash string) (*core.Pod, error) {
	source := parent.GetSourceSpec()
	if source.Image == nil || source.Image.Repository == "" {
		return nil, fmt.Errorf("spec for %s doesn't specify an image", parent.GetName())
	}

	volumes := []core.Volume{
		{
			Name:         "output",
			VolumeSource: core.VolumeSource{EmptyDir: &core.EmptyDirVolumeSource{}},
		},
	}
	mounts := []core.VolumeMount{
		{
			Name:      "output",
			MountPath: "/run/kudzu/output",
		},
	}
	if confHash != emptyConfigHash {
		var readOnly int32 = 0444
		volumes = append(volumes, core.Volume{
			Name: "input",
			VolumeSource: core.VolumeSource{
				ConfigMap: &core.ConfigMapVolumeSource{
					LocalObjectReference: core.LocalObjectReference{Name: delegateName(parent)},
					DefaultMode:          &readOnly,
				},
			},
		})
		mounts = append(mounts, core.VolumeMount{
			Name:      "input",
			MountPath: "/run/kudzu/input",
			ReadOnly:  true,
		})
	}

	callbackImage := m.CallbackImage
	if callbackImage == "" {
		callbackImage = DefaultCallbackImage
	}
	baseURL := m.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	pod := &core.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:        delegateName(parent),
			Namespace:   namespace,
			Labels:      delegateLabels(parent),
			Annotations: map[string]string{configAnnotation: confHash},
		},
		Spec: core.PodSpec{
			InitContainers: []core.Container{
				{
					Name:            task,
					Image:           source.Image.String(),
					ImagePullPolicy: source.Image.PullPolicy,
					Command:         []string{fmt.Sprintf("/opt/kudzu/bin/%s", task)},
					VolumeMounts:    mounts,
				},
			},
			Containers: []core.Container{
				{
					Name:            "callback",
					Image:           callbackImage,
					ImagePullPolicy: core.PullAlways,
					Command:         []string{"/opt/kudzu/bin/callback"},
					Env: []core.EnvVar{
						{Name: "KUDZU_CALLBACK_URL", Value: delegateCallbackURL(baseURL, parent)},
						{Name: "KUDZU_CONFIG_HASH", Value: confHash},
						{Name: "KUDZU_DELEGATE_IMAGE", Value: source.Image.String()},
					},
					VolumeMounts: []core.VolumeMount{
						{Name: "output", MountPath: "/run/kudzu/output", ReadOnly: true},
					},
				},
			},
			Volumes: volumes,
		},
	}

	if err := controllerutil.SetControllerReference(parent, pod, m.scheme); err != nil {
		return nil, err
	}
	return pod, nil
}

func (m *Manager) mapForConfig(parent Object, conf map[string]string, hash string) (*core.ConfigMap, error) {
	cm := &core.ConfigMap{
		ObjectMeta: meta.ObjectMeta{
			Name:        delegateName(parent),
			Namespace:   namespace,
			Labels:      delegateLabels(parent),
			Annotations: map[string]string{configAnnotation: hash},
		},
		Data: conf,
	}

	if err := controllerutil.SetControllerReference(parent, cm, m.scheme); err != nil {
		return nil, err
	}
	return cm, nil
}
