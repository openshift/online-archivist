package archive

import (
	"fmt"
	"strings"
	"time"

	"github.com/openshift/online-archivist/pkg/util"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	"github.com/openshift/origin/pkg/oc/cli/cmd"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kapi "k8s.io/kubernetes/pkg/api"
	kapiv1 "k8s.io/kubernetes/pkg/api/v1"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
	"k8s.io/kubernetes/pkg/kubectl/resource"

	log "github.com/Sirupsen/logrus"
)

type Importer struct {
	*clientcmd.Factory

	f              *clientcmd.Factory
	pc             projectclientset.Interface
	oc             osclient.Interface
	kc             kclientset.Interface
	originExporter cmd.Exporter
	mapper         meta.RESTMapper
	typer          runtime.ObjectTyper
	namespace      string
	log            *log.Entry
	username       string
}

func NewImporter(
	projectClient projectclientset.Interface,
	f *clientcmd.Factory,
	oc osclient.Interface,
	kc kclientset.Interface,
	namespace string,
	username string) *Importer {

	aLog := log.WithFields(log.Fields{
		"namespace": namespace,
		"user":      username,
	})
	mapper, typer := f.Object()

	// Re-using Origin exporter logic until the bugs in upstream kube are fixed:
	// https://github.com/kubernetes/kubernetes/issues/49497
	// Re-using Origin exporter logic until the bugs in upstream kube are fixed:
	// https://github.com/kubernetes/kubernetes/issues/49497
	originExporter := &cmd.DefaultExporter{}

	return &Importer{
		pc: projectClient,

		oc:        oc,
		kc:        kc,
		f:         f,
		namespace: namespace,
		username:  username,

		originExporter: originExporter,
		mapper:         mapper,
		typer:          typer,
		log:            aLog,
	}
}

// Import creates a new project and generates objects for the project based on a template, which is currently a YAML string
// Import has additional functionality to correctly import Service Accounts and their ImagePullSecrets
func (a *Importer) Import(yamlInput string) error {
	a.log.Info("beginning import")

	reader := strings.NewReader(yamlInput)

	builder := resource.NewBuilder(a.mapper, a.CategoryExpander(), a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		ContinueOnError().
		NamespaceParam(a.namespace).DefaultNamespace().AllNamespaces(false).
		Flatten()
	builder = builder.Stream(reader, "error building from YAML")

	// Create visitors for each resource:
	err := builder.Do().Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		kind, err := ObjKind(a.typer, info.Object)
		if err != nil {
			return err
		}
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", kind, info.Name),
		})

		// Use the "exporter" to strip fields we should not be setting. This is done (weirdly)
		// during import so we can take the approach of exporting as much as possible, and then
		// sorting it out on import.
		a.originExporter.Export(info.Object, false)

		switch r := info.ResourceMapping().Resource; r {

		case "pods", "replicationcontrollers", "builds":
			objLog.Info("skipping transient object")
			return nil

		case "serviceaccounts":
			// pass in the current object being visited into scanServiceAccountsForImport
			err = a.scanServiceAccountsForImport(info)
			if err != nil {
				objLog.Info("error when scanning for service account")
				return err
			}
			return nil

		case "services":
			svc := info.Object.(*kapi.Service)
			// Must strip the cluster IP from exported service.
			// TODO: This should probably be done in the export logic for services
			svc.Spec.ClusterIP = ""

		case "secrets":
			s := info.Object.(*kapi.Secret)
			// We don't want to import service account token secrets, these are recreated when the
			// service account is created.
			if s.Type == kapi.SecretTypeServiceAccountToken {
				objLog.Debugln("skipping service account token secret")
				return nil
			}
			// Skip dockercfg secrets if they're linked explicitly to a service account. These will be
			// recreated in the destination project for us.
			if s.Type == kapi.SecretTypeDockercfg {
				_, ok := s.GetAnnotations()[kapi.ServiceAccountUIDKey]
				if ok {
					objLog.Debugln("skipping dockercfg secret linked to service account")
					return nil
				}
			}
		}

		// All other resource types:
		err = createAndRefresh(info)
		if err != nil {
			objLog.Info("error creating object")
			return err
		}
		objLog.Info("importing")

		return nil
	})

	if err != nil {
		a.log.Error("error visiting objects", err)
		return err
	}
	return nil
}

// scanServiceAccountsForImport scans for an already existing Service Account and if the current Service Account
// matches an existing Service Account, the function adds imagePullSecrets if any exist
// pass in the current object being visited, which has already been identified as a Service Account
func (a *Importer) scanServiceAccountsForImport(info *resource.Info) error {
	// version the incoming resource
	clientConfig, err := a.f.ClientConfig()
	if err != nil {
		return err
	}
	outputVersion := *clientConfig.GroupVersion
	object, err := resource.AsVersionedObject([]*resource.Info{info}, false, outputVersion, kapi.Codecs.LegacyCodec(outputVersion))
	if err != nil {
		return err
	}
	saObject := object.(*kapiv1.ServiceAccount)

	sas, err := a.kc.CoreV1().ServiceAccounts(a.namespace).List(metav1.ListOptions{})
	if err != nil {
		a.log.Error("error finding service accounts", err)
		return err
	}

	a.log.Debugf("found %d service accounts", len(sas.Items))

	for i := range sas.Items {
		// Need to use the index here as we must use the pointer to use as a runtime.Object
		s := sas.Items[i]
		if saObject.Name == s.Name { // Service Account, s, already exists

			imgPullSecrets := []kapiv1.LocalObjectReference{}
			for _, r := range saObject.ImagePullSecrets {
				imgPullSecrets = append(s.ImagePullSecrets, r)
				saLog := a.log.WithFields(log.Fields{
					"secret":          fmt.Sprintf("%s", r),
					"service account": fmt.Sprintf("%s", saObject.Name),
				})
				saLog.Info("adding image pull secret to service account")
			}
			s.ImagePullSecrets = imgPullSecrets

			util.Retry(10, 1000*time.Millisecond, a.log, func() (err error) {
				_, err = a.kc.CoreV1().ServiceAccounts(a.namespace).Update(&s)
				if err != nil {
					a.log.Errorln("error updating existing service account: %s", err)
					// Do we consider this an import failure if this mismatch between resource version and update version exists?
					// Is there anything else we can do here besides re-try or have the user attempt to import again?
				}
				return err
			})
		}
	}

	match := false
	for i := range sas.Items {
		// Need to use the index here as we must use the pointer to use as a runtime.Object
		s := sas.Items[i]

		// if the incoming object, saObject, does not match ANY of the current SA objects, create a new SA
		if saObject.Name == s.Name {
			match = true
		}
	}
	a.log.Info("match: ", match)

	if match == false { // Service Account does NOT already exist
		a.log.Info("service account does NOT already exist: ", saObject.Name)
		err = createAndRefresh(info)
		if err != nil {
			a.log.Info("error creating object")
			return err
		}
		a.log.Info("importing")
	}
	return nil
}
