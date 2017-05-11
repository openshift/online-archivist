package main

import (
	"flag"
	"os"

	"github.com/openshift/online/archivist/pkg/clustermonitor"
	"github.com/openshift/online/archivist/pkg/config"

	buildclient "github.com/openshift/origin/pkg/build/client/clientset_generated/internalclientset/typed/core/internalversion"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"

	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"

	log "github.com/Sirupsen/logrus"
	"github.com/spf13/pflag"
)

func main() {
	log.SetOutput(os.Stdout)
	var cfgFile string
	flag.StringVar(&cfgFile, "config", "", "load configuration from file")
	flag.Parse()

	var archivistCfg config.ArchivistConfig
	if cfgFile != "" {
		var err error
		archivistCfg, err = config.NewArchivistConfigFromFile(cfgFile)
		if err != nil {
			log.Panicf("invalid configuration: %s", err)
		}
	} else {
		archivistCfg = config.ArchivistConfig{} // switch to defaults
		config.ApplyConfigDefaults(&archivistCfg)
		err := config.ValidateConfig(&archivistCfg)
		if err != nil {
			log.Panicf("invalid configuration: %s", err)
		}
	}
	if lvl, err := log.ParseLevel(archivistCfg.LogLevel); err != nil {
		log.Panic(err)
	} else {
		log.SetLevel(lvl)
	}
	log.Infoln("Using configuration:", archivistCfg)

	// TODO: make use of for real deployments
	// conf, err := restclient.InClusterConfig()
	dcc := clientcmd.DefaultClientConfig(pflag.NewFlagSet("empty", pflag.ContinueOnError))
	clientFac := clientcmd.NewFactory(dcc)
	clientConfig, err := dcc.ClientConfig()
	if err != nil {
		log.Panicf("error creating cluster clientConfig: %s", err)
	}

	log.WithFields(log.Fields{
		"APIPath":  clientConfig.APIPath,
		"CertFile": clientConfig.CertFile,
		"KeyFile":  clientConfig.KeyFile,
		"CAFile":   clientConfig.CAFile,
		"Host":     clientConfig.Host,
		"Username": clientConfig.Username,
	}).Infoln("Created OpenShift client clientConfig:")

	var oc osclient.Interface
	var kc kclientset.Interface

	oc, kc, err = clientFac.Clients()
	if err != nil {
		log.Panicf("error creating OpenShift/Kubernetes clients: %s", err)
	}

	var bc buildclient.CoreInterface
	bc, err = buildclient.NewForConfig(clientConfig)
	if err != nil {
		log.Panicf("Error creating OpenShift client: %s", err)
	}

	stopChan := make(chan struct{})

	activityMonitor := clustermonitor.NewClusterMonitor(archivistCfg, archivistCfg.Clusters[0], oc, kc, bc)
	activityMonitor.Run(stopChan)

	log.Infoln("all components running")
	<-stopChan
}
