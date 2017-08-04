package archive

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/online-archivist/pkg/util"

	authclientset "github.com/openshift/origin/pkg/authorization/generated/clientset"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/admin/policy"
	"github.com/openshift/origin/pkg/cmd/server/bootstrappolicy"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectapi "github.com/openshift/origin/pkg/project/apis/project"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	userapi "github.com/openshift/origin/pkg/user/apis/user"
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
type Archiver struct {
	pc projectclientset.Interface
	ac authclientset.Interface
	uc userclientset.Interface

	// TODO: Legacy client usage here until we find their equivalent in new generated clientsets:
	uidmc osclient.UserIdentityMappingInterface
	idc   osclient.IdentityInterface

	oc              osclient.Interface
	kc              kclientset.Interface
	f               *clientcmd.Factory
	mapper          meta.RESTMapper
	typer           runtime.ObjectTyper
	objectsToExport []runtime.Object
	importedObjects []runtime.Object
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
		importedObjects: []runtime.Object{},

		mapper: mapper,
		typer:  typer,
		log:    aLog,
	}
}

// Export generates and returns a kapi.List containing all exported objects from the project.
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

// createAndRefresh creates an object from input info and refreshes info with that object
func createAndRefresh(info *resource.Info) error {
	obj, err := resource.NewHelper(info.Client, info.Mapping).Create(info.Namespace, true, info.Object)
	if err != nil {
		return err
	}
	info.Refresh(obj, true)
	return nil
}

// Import generates API objects for the project based on a template (currently a YAML string).
func (a *Archiver) Import(yamlInput string) error {
	a.log.Info("beginning import")

	reader := strings.NewReader(yamlInput)

	// create the build
	build := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		ContinueOnError().
		NamespaceParam(a.namespace).DefaultNamespace().AllNamespaces(false).
		// ResourceTypeOrNameArgs(true, "all").
		Flatten()
	a.log.Info("build created")

	// complete the build with the YAML string
	completeBuild := build.Stream(reader, "error building from YAML")
	a.log.Info("build completed from YAML")

	// create visitors for each resource
	err := completeBuild.Do().Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", a.ObjKind(info.Object), info.Name),
		})

		// should there be a check for transient objects on import as a secondary check?
		if info.ResourceMapping().Resource != "pods" &&
			info.ResourceMapping().Resource != "replicationcontrollers" {

			// TODO: May be redundant/unnecessary config settings
			// Why not use info.Object?
			// clientConfig, err := a.f.ClientConfig()
			// if err != nil {
			// 	return err
			// }

			// do we need something explicit for the input version or is this taken care of somewhere else?

			err = createAndRefresh(info)
			if err != nil {
				objLog.Info("error creating object")
				return err
			}

			objLog.Info("importing")
			// a.importedObjects = append(a.importedObjects, object)
		} else {
			objLog.Info("skipping")
		}
		return nil
	})

	if err != nil {
		a.log.Error("error visiting objects", err)
		return err
	}

	return nil
}

// scanProjectObjects iterates most objects in a project and determines if they should be exported.
// Some types are not included in this however and must be dealt with separately. (i.e. Secrets)
func (a *Archiver) scanProjectObjects() error {

	// TODO: ExportParam is not actually hooked up server side for list operations yet but to:
	// https://github.com/kubernetes/kubernetes/issues/49497
	// Because of this we hack around the problem for now, see below.
	r := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		NamespaceParam(a.namespace).DefaultNamespace().AllNamespaces(false).
		ExportParam(true).
		ResourceTypeOrNameArgs(true, "all").
		Flatten()

	err := r.Do().Visit(func(info *resource.Info, err error) error {
		objLog := a.log.WithFields(log.Fields{
			"object": fmt.Sprintf("%s/%s", a.ObjKind(info.Object), info.Name),
		})
		a.stripObjectMeta(info.Object)
		// We do not want to archive transient objects such as pods or replication controllers:
		if info.ResourceMapping().Resource != "pods" &&
			info.ResourceMapping().Resource != "replicationcontrollers" &&
			info.ResourceMapping().Resource != "builds" {

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
		} else {
			objLog.Info("skipping")
		}
		return nil
	})

	if err != nil {
		a.log.Error("error visiting objects", err)
		return err
	}
	return nil
}

// stripObjectMeta removes object metadata that should not be present in an export as it ties
// it to a particular namespace and/or cluster. This closely matches the exportObjectMeta func
// that can be found in Kubernetes and would normally do this for us, but the export functionality
// there has a number of bugs that are in the process of being fixed. For now we encapsulate hacks
// around these bugs in this method.
func (a *Archiver) stripObjectMeta(obj runtime.Object) error {
	if accessor, err := meta.Accessor(obj); err == nil {
		accessor.SetUID("")
		accessor.SetNamespace("")
		accessor.SetCreationTimestamp(metav1.Time{})
		accessor.SetDeletionTimestamp(nil)
		accessor.SetResourceVersion("")
		accessor.SetSelfLink("")
		if len(accessor.GetGenerateName()) > 0 {
			accessor.SetName("")
		}
	} else {
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
		// Skip certain secret types, we'll let service accounts and such be recreated on the import:
		if s.Type == kapiv1.SecretTypeServiceAccountToken {
			objLog.Debugln("skipping service account token secret")
			filteredSecrets = append(filteredSecrets, s.Name)
			continue
		}
		// Skip dockercfg secrets if they're linked explicitly to a service account. These will be
		// recreated in the destination project for us.
		if s.Type == kapiv1.SecretTypeDockercfg {
			_, ok := s.GetAnnotations()[kapiv1.ServiceAccountUIDKey]
			if ok {
				objLog.Debugln("skipping dockercfg secret linked to service account")
				filteredSecrets = append(filteredSecrets, s.Name)
				continue
			}
		}
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
	a.stripObjectMeta(obj)
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

func serializeObjList(list runtime.Object) (string, error) {
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
	yamlStr, err := serializeObjList(objList)
	if err != nil {
		return "", err
	}

	a.log.Debug("got yaml string from export")
	a.log.Debug(yamlStr)

	// Finally delete the project. Note that this may take some time but the project
	// should be marked as in Terminating status much more quickly. This will cleanup
	// most objects we're concerned about.
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

// importTemplate takes .yaml file and creates a resultant object
func (a *Archiver) importTemplate(yamlObject string) error {

	a.log.Infoln("reading from YAML file")
	// create runtime.Object

	// append to objectsImported
	// a.objectsImported = append(a.objectsImported, obj)
	return nil
}

// Import a .yaml file and create the resources contained therein
// TODO user role bindings so that same permissions get restored to project
func (a *Archiver) Unarchive() error {
	filenames := []string{fmt.Sprintf("%s.yaml", a.username)}
	_, explicit, err := a.f.DefaultNamespace()
	if err != nil {
		return err
	}

	// Create resource and Infos from .yaml template file
	r := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		FilenameParam(explicit, &resource.FilenameOptions{Recursive: false, Filenames: filenames}).
		Flatten().
		Do()

	// Visit each Info from template and create corresponding resource
	err = r.Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		// Resources should be created under project namespace
		switch info.ResourceMapping().Resource {
		case "projects":
			// Check if project is being created in original namespace or a new one
			if a.namespace == "" {
				a.namespace = info.Name
			} else {
				info.Object.(*projectapi.Project).ObjectMeta.Name = a.namespace
			}
			a.log.Infoln("Unarchiving: %s/%s in namespace %s", info.ResourceMapping().Resource, info.Name, a.namespace)
			obj, err := resource.NewHelper(info.Client, info.Mapping).Create(a.namespace, true, info.Object)

			// Add default service account role bindings to newly-created project
			for _, binding := range bootstrappolicy.GetBootstrapServiceAccountProjectRoleBindings(a.namespace) {
				addRole := &policy.RoleModificationOptions{
					RoleName:      binding.RoleRef.Name,
					RoleNamespace: binding.RoleRef.Namespace,
					// TODO: try to eliminate oc use here, it's the last one.
					RoleBindingAccessor: policy.NewLocalRoleBindingAccessor(a.namespace, a.oc),
					Subjects:            binding.Subjects,
				}
				if err := addRole.AddRole(); err != nil {
					fmt.Printf("Could not add service accounts to the %v role: %v\n", binding.RoleRef.Name, err)
					return err
				}
			}
			if err != nil {
				return err
			}
			info.Refresh(obj, true)
		case "users":
			a.log.Infoln("Unarchiving: %s/%s in namespace %s", info.ResourceMapping().Resource, info.Name, a.namespace)
			obj, err := resource.NewHelper(info.Client, info.Mapping).Create(a.namespace, true, info.Object)
			if err != nil {
				return err
			}
			info.Refresh(obj, true)
			_, err = a.uidmc.Create(&userapi.UserIdentityMapping{
				User:     kapi.ObjectReference{Name: a.username},
				Identity: kapi.ObjectReference{Name: "anypassword:" + a.username},
			})
			if err != nil {
				return err
			}
			info.Refresh(obj, true)
		default:
			a.log.Infoln("Unarchiving: %s/%s in namespace %s", info.ResourceMapping().Resource, info.Name, a.namespace)
			obj, err := resource.NewHelper(info.Client, info.Mapping).Create(a.namespace, true, info.Object)
			if err != nil {
				return err
			}
			info.Refresh(obj, true)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
