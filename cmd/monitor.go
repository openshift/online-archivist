package cmd

import (
	"os"

	"github.com/openshift/online/archivist/pkg/clustermonitor"
	"github.com/openshift/online/archivist/pkg/config"

	buildclient "github.com/openshift/origin/pkg/build/generated/clientset"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"

	restclient "k8s.io/client-go/rest"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"

	log "github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func init() {
	RootCmd.AddCommand(clusterMonitorCmd)
}

var clusterMonitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Monitors OpenShift cluster capacity and initiates archival.",
	Long:  `Monitors the capacity of an OpenShift cluster, project last activity, and submits requests to archive dormant projects as necessary.`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetOutput(os.Stdout)
		archivistCfg := loadConfig(cfgFile)

		clientConfig, _, oc, kc, err := createClients()

		if err != nil {
			log.Panicf("error creating OpenShift/Kubernetes clients: %s", err)
		}

		var bc buildclient.Interface
		bc, err = buildclient.NewForConfig(clientConfig)
		if err != nil {
			log.Panicf("Error creating OpenShift client: %s", err)
		}

		stopChan := make(chan struct{})

		activityMonitor := clustermonitor.NewClusterMonitor(archivistCfg, archivistCfg.Clusters[0], oc, kc, bc)
		activityMonitor.Run(stopChan)

		log.Infoln("cluster monitor running")
		// Block until we receive a notification from the activityMonitor on the channel.
		<-stopChan

	},
}

func createClients() (*restclient.Config, *clientcmd.Factory, osclient.Interface, kclientset.Interface, error) {
	// TODO: multi cluster connections
	// TODO: make use of for real deployments
	// conf, err := restclient.InClusterConfig()
	dcc := clientcmd.DefaultClientConfig(pflag.NewFlagSet("empty", pflag.ContinueOnError))
	//clientFac := clientcmd.NewFactory(dcc)
	clientFac := clientcmd.New(pflag.NewFlagSet("empty", pflag.ContinueOnError))

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

	oc, kc, err := clientFac.Clients()
	return clientConfig, clientFac, oc, kc, err
}

func loadConfig(configFile string) config.ArchivistConfig {
	var archivistCfg config.ArchivistConfig
	if configFile != "" {
		var err error
		archivistCfg, err = config.NewArchivistConfigFromFile(configFile)
		if err != nil {
			log.Panicf("invalid configuration: %s", err)
		}
	} else {
		archivistCfg = config.NewDefaultArchivistConfig()
	}
	if lvl, err := log.ParseLevel(archivistCfg.LogLevel); err != nil {
		log.Panic(err)
	} else {
		log.SetLevel(lvl)
	}
	log.Infoln("using configuration:", archivistCfg)
	return archivistCfg
}
