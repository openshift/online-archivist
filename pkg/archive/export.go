package archive

import (
	"fmt"
	"os"

	"github.com/openshift/online-archivist/pkg/util"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kapi "k8s.io/kubernetes/pkg/api"
	kapiv1 "k8s.io/kubernetes/pkg/api/v1"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
	"k8s.io/kubernetes/pkg/kubectl/resource"
	"k8s.io/kubernetes/pkg/printers"

	log "github.com/Sirupsen/logrus"
)

// Exporter creates a list of API objects representing everything we could pull from a project.
// This result can be used to serialize to a file and later be imported to recreate the project
// as closely as possible.
type Exporter struct {
	pc projectclientset.Interface

	f  *clientcmd.Factory
	oc osclient.Interface
	kc kclientset.Interface
	*clientcmd.Factory
	mapper          meta.RESTMapper
	typer           runtime.ObjectTyper
	objectsToExport []runtime.Object
	namespace       string
	log             *log.Entry
	username        string
}

func NewExporter(
	projectClient projectclientset.Interface,
	f *clientcmd.Factory,
	oc osclient.Interface,
	kc kclientset.Interface,
	namespace string,
	username string) *Exporter {

	aLog := log.WithFields(log.Fields{
		"namespace": namespace,
		"user":      username,
	})
	mapper, typer := f.Object()

	return &Exporter{
		pc: projectClient,

		oc:        oc,
		kc:        kc,
		f:         f,
		namespace: namespace,
		username:  username,

		objectsToExport: []runtime.Object{},

		mapper: mapper,
		typer:  typer,
		log:    aLog,
	}
}

// Export generates and returns a kapi.List containing all exported objects from the project.
// Should contain as much data from the project as possible, we filter on import instead.
func (a *Exporter) Export() (runtime.Object, error) {
	a.log.Info("beginning export")

	// Ensure project exists:
	_, err := a.pc.ProjectV1().Projects().Get(a.namespace, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Get all resources for this project and archive them. Using this kubectl resource infra
	// allows us to list all objects generically, instead of hard coding a lookup for each API type
	// and then having to constantly keep that up to date as more types are added.
	a.log.Debugln("scanning objects in project")
	err = a.scanProjectObjects()
	if err != nil {
		return nil, err
	}

	// Some objects such as secrets and service accounts are not included by default when
	// listing all resources. (via deads2k: hardcoded category alias, can't
	// be changed) We must process them explicitly.
	filteredSecretNames, err := a.scanProjectSecrets()
	if err != nil {
		return nil, err
	}
	err = a.scanProjectServiceAccounts(filteredSecretNames)
	if err != nil {
		return nil, err
	}

	a.log.Debug("creating template")
	// Make exported "template", which is really just a List of resources
	// TODO: should we switch to an actual template?
	template := &kapi.List{
		ListMeta: metav1.ListMeta{},
		Items:    a.objectsToExport,
	}

	// This may be redundant config stuff, but version template list
	clientConfig, err := a.f.ClientConfig()
	if err != nil {
		return nil, err
	}
	var result runtime.Object
	outputVersion := *clientConfig.GroupVersion
	result, err = kapi.Scheme.ConvertToVersion(template, outputVersion)
	if err != nil {
		return nil, err
	}

	// TODO: kill this writing to yaml file, for now it's really useful for debugging the test.
	if err := a.exportTemplate(result); err != nil {
		return nil, err
	}
	a.log.Infoln("export completed")

	return result, nil
}

// scanProjectObjects iterates most objects in a project and determines if they should be exported.
// Some types are not included in this however and must be dealt with separately. (i.e. Secrets, Service Accounts)
func (a *Exporter) scanProjectObjects() error {

	r := resource.NewBuilder(a.mapper, a.CategoryExpander(), a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		NamespaceParam(a.namespace).DefaultNamespace().AllNamespaces(false).
		ResourceTypeOrNameArgs(true, "all").
		Flatten()

	err := r.Do().Visit(func(info *resource.Info, err error) error {
		kind, err := ObjKind(a.typer, info.Object)
		if err != nil {
			return err
		}
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", kind, info.Name),
		})

		// Need to version the resources for export:
		clientConfig, err := a.f.ClientConfig()
		if err != nil {
			return err
		}
		outputVersion := *clientConfig.GroupVersion
		object, err := resource.AsVersionedObject([]*resource.Info{info}, false, outputVersion, kapi.Codecs.LegacyCodec(outputVersion))
		if err != nil {
			return err
		}

		objLog.Info("exporting")
		a.objectsToExport = append(a.objectsToExport, object)
		return nil
	})

	if err != nil {
		a.log.Error("error visiting objects", err)
		return err
	}
	return nil
}

// scanProjectSecrets explicitly lists all secrets in the project, and if of a valid type will add
// them to the list of objects to export. Secrets automatically created for service accounts are skipped,
// as they will be created automatically on import if applicable. Returns the list of secret names we
// filtered, as this is used in other areas to make sure we omit references to them.
func (a *Exporter) scanProjectSecrets() ([]string, error) {
	a.log.Debug("scanning secrets")
	filteredSecrets := []string{}
	secrets, err := a.kc.CoreV1().Secrets(a.namespace).List(metav1.ListOptions{})
	if err != nil {
		a.log.Error("error exporting secrets", err)
		return filteredSecrets, err
	}
	a.log.Debugf("found %d secrets", len(secrets.Items))
	for i := range secrets.Items {
		// Need to use the index here as we must use the pointer to use as a runtime.Object:
		s := secrets.Items[i]
		kind, err := ObjKind(a.typer, &s)
		if err != nil {
			return filteredSecrets, err
		}
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", kind, s.Name),
		})

		objLog.Info("exporting")

		err = a.versionAndAppendObject(&s)
		if err != nil {
			return filteredSecrets, err
		}
	}
	return filteredSecrets, nil
}

// scanProjectServiceAccounts explicitly lists all service accounts in the project, and will
// export those that appear to be user created. Unfortunately today the best we can do here
// is skip those with the default names: builder, deployer, default.
func (a *Exporter) scanProjectServiceAccounts(filteredSecretNames []string) error {
	a.log.Debug("scanning service accounts")
	sas, err := a.kc.CoreV1().ServiceAccounts(a.namespace).List(metav1.ListOptions{})
	if err != nil {
		a.log.Error("error exporting service accounts", err)
		return err
	}
	a.log.Debugf("found %d service accounts", len(sas.Items))
	a.log.Infoln(filteredSecretNames)
	for i := range sas.Items {
		// Need to use the index here as we must use the pointer to use as a runtime.Object:
		s := sas.Items[i]
		kind, err := ObjKind(a.typer, &s)
		if err != nil {
			return err
		}
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", kind, s.Name),
		})

		// Remove image build secrets we filtered during secret export:
		imgPullSecrets := []kapiv1.LocalObjectReference{}
		a.log.Infoln(imgPullSecrets)
		for _, r := range s.ImagePullSecrets {
			if !util.StringInSlice(r.Name, filteredSecretNames) {
				imgPullSecrets = append(imgPullSecrets, r)
			}
		}
		s.ImagePullSecrets = imgPullSecrets

		objLog.Info("exporting")

		err = a.versionAndAppendObject(&s)
		if err != nil {
			return err
		}
	}
	return nil
}

// versionAndAppendObject will ensure our Object is v1 versioned, and append to
// the list of objects for export. This prevents a situation where objects are
// exported to the template missing a kind and version.
func (a *Exporter) versionAndAppendObject(obj runtime.Object) error {
	clientConfig, err := a.f.ClientConfig()
	if err != nil {
		return err
	}
	outputVersion := *clientConfig.GroupVersion
	vObj, err := resource.TryConvert(kapi.Scheme, obj, outputVersion)
	if err != nil {
		return err
	}
	a.objectsToExport = append(a.objectsToExport, vObj)
	return nil
}

// exportTemplate takes a resultant object and prints it to a .yaml file
func (a *Exporter) exportTemplate(obj runtime.Object) error {
	p := printers.YAMLPrinter{}

	filename := fmt.Sprintf("%s.yaml", a.username)
	a.log.Infoln("writing to file", filename)
	fo, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	err = p.PrintObj(obj, fo)
	if err != nil {
		return err
	}
	return nil
}
