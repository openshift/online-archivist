package cmd

import (
	"os"

	"github.com/openshift/online-archivist/pkg/clustermonitor"
	"github.com/openshift/online-archivist/pkg/config"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"

	restclient "k8s.io/client-go/rest"
	kclientcmd "k8s.io/client-go/tools/clientcmd"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"

	log "github.com/Sirupsen/logrus"
	arkclient "github.com/heptio/ark/pkg/client"
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

		_, _, oc, kc, err := createClients()

		if err != nil {
			log.Panicf("error creating OpenShift/Kubernetes clients: %s", err)
		}

		// TODO: in Ark this appears to be the binary name when launching the CLI.
		// Not sure how this is being used.
		arkFactory := arkclient.NewFactory()
		arkClient, err := arkFactory.Client()
		if err != nil {
			log.Panicf("error creating Ark client: %s", err)
		}
		log.Debugf("got ark client: %v", arkClient)

		activityMonitor := clustermonitor.NewClusterMonitor(archivistCfg, archivistCfg.Clusters[0], oc, kc)
		activityMonitor.Run()

		log.Infoln("cluster monitor running")
	},
}

func createClients() (*restclient.Config, *clientcmd.Factory, osclient.Interface, kclientset.Interface, error) {
	dcc := clientcmd.DefaultClientConfig(pflag.NewFlagSet("empty", pflag.ContinueOnError))
	return CreateClientsForConfig(dcc)
}

// CreateClientsForConfig creates and returns OpenShift and Kubernetes clients (as well as other useful
// client objects) for the given client config.
// TODO: stop returning internalversion kclientset
func CreateClientsForConfig(dcc kclientcmd.ClientConfig) (*restclient.Config, *clientcmd.Factory, osclient.Interface, kclientset.Interface, error) {

	rawConfig, err := dcc.RawConfig()
	log.Infoln("current kubeconfig context", rawConfig.CurrentContext)

	clientFac := clientcmd.NewFactory(dcc)

	clientConfig, err := dcc.ClientConfig()
	if err != nil {
		log.Panicf("error creating cluster clientConfig: %s", err)
	}

	log.WithFields(log.Fields{
		"APIPath":     clientConfig.APIPath,
		"Host":        clientConfig.Host,
		"Username":    clientConfig.Username,
		"BearerToken": clientConfig.BearerToken,
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
