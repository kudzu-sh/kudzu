// Copyright © 2018 the Kudzu contributors.
// Licensed under the Apache License, Version 2.0; see the NOTICE file.

package main

import (
	"net/url"
	"os"

	"go.uber.org/zap"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type ClusterConfig struct {
	Path      string
	MasterURL *url.URL
}

func (c *ClusterConfig) Expose(app *kingpin.Application) {
	app.Flag(clientcmd.RecommendedConfigPathFlag, "Path to Kubernetes client config file").Short('K').
		Envar(clientcmd.RecommendedConfigPathEnvVar).Default(clientcmd.RecommendedHomeFile).StringVar(&c.Path)
	app.Flag("kube-api-url", "URL to Kubernetes API server").URLVar(&c.MasterURL)
}

func (c *ClusterConfig) RESTConfig(log *zap.Logger) *rest.Config {
	log = log.With(zap.String("path", c.Path))

	conf, err := clientcmd.LoadFromFile(c.Path)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal("Failed to load kubeconfig file", zap.Error(err))
	}

	if conf == nil {
		restconf, icerr := rest.InClusterConfig()
		if icerr != nil {
			log.Fatal("Could not find local kubeconfig file, and in-cluster config failed", zap.Error(err))
		}

		return restconf
	}

	overrides := clientcmd.ConfigOverrides{}
	if c.MasterURL != nil {
		overrides.ClusterInfo = clientcmdapi.Cluster{Server: c.MasterURL.String()}
	}

	restconfig, err := clientcmd.NewDefaultClientConfig(*conf, &overrides).ClientConfig()
	if err != nil {
		log.Fatal("Failed to configure Kubernetes client", zap.Error(err))
	}
	return restconfig
}
