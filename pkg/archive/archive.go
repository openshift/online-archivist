package archive

import (
	"fmt"
	"os"

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
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/kubectl/resource"
	"k8s.io/kubernetes/pkg/printers"

	log "github.com/Sirupsen/logrus"
)

// Archiver allows for a variety of export, import, archive and unarchive operations. One archiver
// should be created per operation as they are bound to a particular namespace and carry state.
type Archiver struct {
	// TODO: Should these use the v1 clients so we don't have to constantly chain method calls to V1?
	pc projectclientset.Interface
	ac authclientset.Interface
	uc userclientset.Interface

	// TODO: Legacy client usage here until we find their equivalent in new generated clientsets:
	uidmc osclient.UserIdentityMappingInterface
	idc   osclient.IdentityInterface

	oc             osclient.Interface
	kc             kclientset.Interface
	f              *clientcmd.Factory
	mapper         meta.RESTMapper
	typer          runtime.ObjectTyper
	projectObjects []runtime.Object
	namespace      string
	log            *log.Entry
	username       string
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

		projectObjects: []runtime.Object{},

		mapper: mapper,
		typer:  typer,
		log:    aLog,
	}
}

// Export generates and returns a list of API objects for the project.
func (a *Archiver) Export() (*kapi.List, error) {
	a.log.Info("exporting project")

	projectName := a.namespace

	project, err := a.pc.ProjectV1().Projects().Get(projectName, metav1.GetOptions{})
	if err != nil {
		a.log.Errorln("unable to lookup project", err)
		return nil, err
	}
	a.log.Info("got project", project)

	// Get all resources for this project and archive them. Using this kubectl resource infra
	// allows us to list all objects generically, instead of hard coding a lookup for each API type
	// and then having to constantly keep that up to date as more types are added.
	a.log.Debugln("listing all project resources")
	r := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		NamespaceParam(projectName).DefaultNamespace().AllNamespaces(false).
		ResourceTypeOrNameArgs(true, "all").
		Flatten()

	err = r.Do().Visit(func(info *resource.Info, err error) error {
		a.log.Debugln("visiting", info.Object.GetObjectKind(), info.Name)
		// We do not want to archive transient objects such as pods or replication controllers:
		if info.ResourceMapping().Resource != "pods" &&
			info.ResourceMapping().Resource != "replicationcontrollers" {
			resourceName := fmt.Sprintf("%s/%s", info.ResourceMapping().Resource, info.Name)

			// Archive the resource
			err := a.archive(resourceName, projectName)
			if err != nil {
				return err
			}
		}
		return nil
	})

	// Secrets are not included by default when listing all resources. (via deads2k: hardcoded category alias, can't
	// be changed) We must list them explicitly.
	secrets, err := a.kc.Core().Secrets(projectName).List(metav1.ListOptions{})
	a.log.Debugf("found %d secrets", len(secrets.Items))
	for i := range secrets.Items {
		// Need to use the index here as we must use the pointer to use as a runtime.Object:
		s := secrets.Items[i]
		// Skip certain secret types, we'll let service accounts and such be recreated on the import:
		if s.Type == kapi.SecretTypeServiceAccountToken ||
			s.Type == kapi.SecretTypeDockercfg {
			continue
		}
		a.projectObjects = append(a.projectObjects, &s)
	}

	a.log.Debugln("creating template")
	// Make exported "template", which is really just a List of resources
	template := &kapi.List{
		ListMeta: metav1.ListMeta{},
		Items:    a.projectObjects,
	}

	// This may be redundant config stuff, but version template list
	clientConfig, err := a.f.ClientConfig()
	var result runtime.Object
	outputVersion := *clientConfig.GroupVersion
	result, err = kapi.Scheme.ConvertToVersion(template, outputVersion)
	if err != nil {
		return nil, err
	}

	// TODO: kill this writing to yaml file
	if err := a.exportTemplate(result); err != nil {
		return nil, err
	}
	a.log.Infoln("export generated successfully")

	return template, nil
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

// Archive exports a template of the project and associated user metadata, handles snapshots of
// persistent volumes, and then deletes those objects from the cluster.
func (a *Archiver) Archive() error {

	a.log.Info("beginning archival")

	_, err := a.Export()
	if err != nil {
		return err
	}

	// Finally delete the project. Note that this may take some time but the project
	// should be marked as in Terminating status much more quickly. This will cleanup
	// most objects we're concerned about.
	a.pc.ProjectV1().Projects().Delete(a.namespace, &metav1.DeleteOptions{})

	return nil
}

// archive adds the given object to the correct array for eventual addition to the template.
func (a *Archiver) archive(name, namespace string) error {
	// TODO: for most cases, this looks like a double instance of using the builder/info lookup,
	// as we already did this for normal objects in the project:
	a.log.WithFields(log.Fields{"name": name}).Infoln("archiving object")
	b := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping),
		kapi.Codecs.UniversalDecoder()).
		NamespaceParam(namespace).DefaultNamespace().
		ResourceTypeOrNameArgs(true, name).
		Flatten()

	one := false
	infos, err := b.Do().IntoSingleItemImplied(&one).Infos()
	if err != nil {
		a.log.WithFields(log.Fields{"name": name}).Errorln("error getting infos:", err)
		return err
	}
	a.log.WithFields(log.Fields{"name": name}).Infoln("got info")

	// May be redundant/unnecessary config settings
	clientConfig, err := a.f.ClientConfig()
	outputVersion := *clientConfig.GroupVersion
	objects, err := resource.AsVersionedObjects(infos, outputVersion, kapi.Codecs.LegacyCodec(outputVersion))
	if err != nil {
		a.log.Errorln("versioned objects error", err)
		return err
	}

	err = b.Do().Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		switch info.ResourceMapping().Resource {
		default:
			for _, object := range objects {
				a.projectObjects = append(a.projectObjects, object)
			}
		}

		return nil
	})
	return err
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
