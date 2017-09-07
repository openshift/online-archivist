package archive

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/openshift/online-archivist/pkg/util"

	authclientset "github.com/openshift/origin/pkg/authorization/generated/clientset"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/cli/cmd"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	userclientset "github.com/openshift/origin/pkg/user/generated/clientset"

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

// Archiver allows for a variety of export, import, archive and unarchive operations. One archiver
// should be created per operation as they are bound to a particular namespace and carry state.
// The Archiver attempts to export as much information as possible, and filter down on import. This
// is a safer approach in the event something goes wrong, and we need to fix a bug to get a project
// to successfully import and come back to life.
type Archiver struct {
	pc projectclientset.Interface
	ac authclientset.Interface
	uc userclientset.Interface

	// TODO: Legacy client usage here until we find their equivalent in new generated clientsets:
	uidmc osclient.UserIdentityMappingInterface
	idc   osclient.IdentityInterface

	f        *clientcmd.Factory
	oc       osclient.Interface
	kc       kclientset.Interface
	exporter cmd.Exporter
	*clientcmd.Factory
	mapper          meta.RESTMapper
	typer           runtime.ObjectTyper
	objectsToExport []runtime.Object
	objectsImported []runtime.Object //use for testing
	projectObject   runtime.Object
	namespace       string
	log             *log.Entry
	username        string
}

func NewArchiver(
	projectClient projectclientset.Interface,
	authClient authclientset.Interface,
	userClient userclientset.Interface,
	uidMapClient osclient.UserIdentityMappingInterface,
	idsClient osclient.IdentityInterface,
	f *clientcmd.Factory,
	oc osclient.Interface,
	kc kclientset.Interface,
	namespace string,
	username string) *Archiver {

	aLog := log.WithFields(log.Fields{
		"namespace": namespace,
		"user":      username,
	})
	mapper, typer := f.Object()

	// Re-using Origin exporter logic until the bugs in upstream kube are fixed:
	// https://github.com/kubernetes/kubernetes/issues/49497
	exporter := &cmd.DefaultExporter{}

	return &Archiver{
		pc: projectClient,
		ac: authClient,
		uc: userClient,

		uidmc: uidMapClient,
		idc:   idsClient,

		oc:        oc,
		kc:        kc,
		f:         f,
		namespace: namespace,
		username:  username,

		objectsToExport: []runtime.Object{},
		objectsImported: []runtime.Object{},

		exporter: exporter,
		mapper:   mapper,
		typer:    typer,
		log:      aLog,
	}
}

// Export generates and returns a kapi.List containing all exported objects from the project.
// Should contain as much data from the project as possible, we filter on import instead.
func (a *Archiver) Export() (runtime.Object, error) {
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
func (a *Archiver) scanProjectObjects() error {

	r := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		NamespaceParam(a.namespace).DefaultNamespace().AllNamespaces(false).
		ResourceTypeOrNameArgs(true, "all").
		Flatten()

	err := r.Do().Visit(func(info *resource.Info, err error) error {
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", a.ObjKind(info.Object), info.Name),
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
func (a *Archiver) scanProjectSecrets() ([]string, error) {
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
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", a.ObjKind(&s), s.Name),
		})

		objLog.Info("exporting")

		err := a.versionAndAppendObject(&s)
		if err != nil {
			return filteredSecrets, err
		}
	}
	return filteredSecrets, nil
}

// scanProjectServiceAccounts explicitly lists all service accounts in the project, and will
// export those that appear to be user created. Unfortunately today the best we can do here
// is skip those with the default names: builder, deployer, default.
func (a *Archiver) scanProjectServiceAccounts(filteredSecretNames []string) error {
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
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", a.ObjKind(&s), s.Name),
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

		err := a.versionAndAppendObject(&s)
		if err != nil {
			return err
		}
	}
	return nil
}

// versionAndAppendObject will ensure our Object is v1 versioned, and append to
// the list of objects for export. This prevents a situation where objects are
// exported to the template missing a kind and version.
func (a *Archiver) versionAndAppendObject(obj runtime.Object) error {
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

// ObjKind uses the object typer to lookup the plain kind string for an object. (i.e. Project,
// Secret, BuildConfig, etc)
func (a *Archiver) ObjKind(o runtime.Object) string {
	kinds, _, err := a.typer.ObjectKinds(o)
	if err != nil {
		a.log.Error("unable to lookup Kind for object:", err)
	}
	return kinds[0].Kind
}

func SerializeObjList(list runtime.Object) (string, error) {
	p := printers.YAMLPrinter{}
	buf := new(bytes.Buffer)
	err := p.PrintObj(list, buf)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Archive exports a template of the project and associated user metadata, handles snapshots of
// persistent volumes, archives them to long term storage and then deletes those objects from
// the cluster.
func (a *Archiver) Archive() (string, error) {
	a.log.Info("beginning archival")

	objList, err := a.Export()
	if err != nil {
		return "", err
	}

	// Serialize the objList to a string for return.
	yamlStr, err := SerializeObjList(objList)
	if err != nil {
		return "", err
	}

	a.log.Debug("got yaml string from export")
	a.log.Debug(yamlStr)

	// Finally delete the project's associated resources, but not the project.
	a.pc.ProjectV1().Projects().Delete(a.namespace, &metav1.DeleteOptions{})

	return yamlStr, nil
}

// exportTemplate takes a resultant object and prints it to a .yaml file
func (a *Archiver) exportTemplate(obj runtime.Object) error {
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

// Unarchive imports a template of the project and associated user metadata
// String YAML input is currently being used for testing
func (a *Archiver) Unarchive() error {
	a.log.Info("beginning unarchival")

	file, err := ioutil.ReadFile("user.yaml")
	if err != nil {
		log.Info("error reading file")
	}

	err = a.Import(string(file))
	if err != nil {
		return err
	}
	return nil
}

// createAndRefresh creates an object from input info and refreshes info with that object
func createAndRefresh(info *resource.Info) error {
	obj, err := resource.NewHelper(info.Client, info.Mapping).Create(info.Namespace, true, info.Object)
	if err != nil {
		return err
	}
	info.Refresh(obj, true)
	return nil
}

// Import creates a new project and generates objects for the project based on a template, which is currently a YAML string
// Import has additional functionality to correctly import Service Accounts and their ImagePullSecrets
func (a *Archiver) Import(yamlInput string) error {
	a.log.Info("beginning import")

	reader := strings.NewReader(yamlInput)

	builder := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
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
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", a.ObjKind(info.Object), info.Name),
		})

		// Use the "exporter" to strip fields we should not be setting. This is done (weirdly)
		// during import so we can take the approach of exporting as much as possible, and then
		// sorting it out on import.
		a.exporter.Export(info.Object, false)

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
		a.objectsImported = append(a.objectsImported, info.Object)
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
func (a *Archiver) scanServiceAccountsForImport(info *resource.Info) error {
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
		a.objectsImported = append(a.objectsImported, info.Object)
		a.log.Info("importing")
	}
	return nil
}

// GetImportedObjects returns a kapi List of imported objects
func (a *Archiver) GetImportedObjects() runtime.Object {
	template := &kapi.List{
		ListMeta: metav1.ListMeta{},
		Items:    a.objectsImported,
	}

	clientConfig, err := a.f.ClientConfig()
	if err != nil {
		return nil
	}

	var result runtime.Object
	outputVersion := *clientConfig.GroupVersion
	result, err = kapi.Scheme.ConvertToVersion(template, outputVersion)

	return result
}
