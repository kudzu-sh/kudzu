// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package delegate

import (
	"fmt"
	"strings"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
)

type StorageKey struct {
	Kind string
	UID  string
}

func StorageKeyForObject(parent Object) StorageKey {
	gvk := parent.GroupVersionKind()
	if gvk.Group != "kudzu.sh" {
		panic(fmt.Sprintf("%s is not a kudzu.sh resource", gvk))
	}

	return StorageKey{strings.ToLower(gvk.Kind), string(parent.GetUID())}
}

var (
	ErrNotFound = fmt.Errorf("Result not found; callback not received from delegate")
)

type ResultStorage interface {
	Put(key StorageKey, hash string, data []byte) error
	Pop(parent Object, hash string, dest interface{}) error
}

type memoryStorageValue struct {
	Hash string
	Data []byte
}

type MemoryResultStorage struct {
	data map[StorageKey]memoryStorageValue
}

func (s *MemoryResultStorage) Put(key StorageKey, hash string, data []byte) error {
	s.init()
	s.data[key] = memoryStorageValue{Hash: hash, Data: data}
	return nil
}

func (s *MemoryResultStorage) Pop(parent Object, hash string, dest interface{}) error {
	s.init()

	key := StorageKeyForObject(parent)
	if val, ok := s.data[key]; ok {
		delete(s.data, key)

		if val.Hash != hash {
			return ErrNotFound
		}

		return UnmarshalResult(val.Data, dest)
	}

	return ErrNotFound
}

func (s *MemoryResultStorage) init() {
	if s.data == nil {
		s.data = make(map[StorageKey]memoryStorageValue, 16)
	}
}

var ErrorGVK = schema.GroupVersionKind{"delegate.kudzu.sh", "v1alpha1", "Error"}

type ErrorResult struct {
	meta.TypeMeta `json:",inline"`
	Message       string `json:"message,omitempty"`
}

func (er ErrorResult) Error() string {
	return er.Message
}

func UnmarshalResult(data []byte, dest interface{}) error {
	er := ErrorResult{}
	if err := json.Unmarshal(data, &er); err == nil {
		if er.GroupVersionKind() == ErrorGVK {
			return &er
		}
	}

	return json.Unmarshal(data, dest)
}
