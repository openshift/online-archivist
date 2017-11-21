package cmd

import (
	"os"
	"path/filepath"

	"github.com/openshift/online-archivist/pkg/clustermonitor"
	"github.com/openshift/online-archivist/pkg/config"

	buildclient "github.com/openshift/client-go/build/clientset/versioned"

	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	kclientcmd "k8s.io/client-go/tools/clientcmd"

	log "github.com/Sirupsen/logrus"
	arkclient "github.com/heptio/ark/pkg/client"
	"github.com/spf13/cobra"
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

		restConfig, kc, err := createClients()

		if err != nil {
			log.Panicf("error creating OpenShift/Kubernetes clients: %s", err)
		}

		// TODO: in Ark this appears to be the binary name when launching the CLI.
		// Not sure how this is being used.
		arkFactory := arkclient.NewFactory("ark")
		arkClient, err := arkFactory.Client()
		if err != nil {
			log.Panicf("error creating Ark client: %s", err)
		}
		log.Debugf("got ark client: %v", arkClient)

		buildClient := buildclient.NewForConfigOrDie(restConfig)

		activityMonitor := clustermonitor.NewClusterMonitor(archivistCfg, archivistCfg.Clusters[0],
			buildClient, kc, arkClient)
		activityMonitor.Run()

		log.Infoln("cluster monitor running")
	},
}

func createClients() (*restclient.Config, kubernetes.Interface, error) {
	return CreateClientsForConfig()
}

// CreateClientsForConfig creates and returns OpenShift and Kubernetes clients (as well as other useful
// client objects) for the given client config.
func CreateClientsForConfig() (*restclient.Config, kubernetes.Interface, error) {

	clientConfig, err := restclient.InClusterConfig()
	if err != nil {
		log.Warnf("error creating in-cluster config: %s", err)
		log.Warnf("attempting switch to external kubeconfig")
		// For external use with the current user's kubeconfig context: (development)
		clientConfig, err = kclientcmd.BuildConfigFromFlags("", filepath.Join(homeDir(), ".kube", "config"))
		if err != nil {
			log.Panicf("error creating cluster config: %s", err)
		}
	}

	log.WithFields(log.Fields{
		"APIPath":     clientConfig.APIPath,
		"Host":        clientConfig.Host,
		"Username":    clientConfig.Username,
		"BearerToken": clientConfig.BearerToken,
	}).Infoln("Created OpenShift client clientConfig:")

	kc := kubernetes.NewForConfigOrDie(clientConfig)
	return clientConfig, kc, err
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

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
