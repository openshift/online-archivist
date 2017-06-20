package archive

import (
	"fmt"
	"os"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/admin/policy"
	"github.com/openshift/origin/pkg/cmd/server/bootstrappolicy"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectapi "github.com/openshift/origin/pkg/project/apis/project"
	userapi "github.com/openshift/origin/pkg/user/apis/user"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kapi "k8s.io/kubernetes/pkg/api"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/kubectl/resource"
	"k8s.io/kubernetes/pkg/printers"

	log "github.com/Sirupsen/logrus"
)

type Archiver struct {
	users                osclient.UserInterface
	identities           osclient.IdentityInterface
	userIdentityMappings osclient.UserIdentityMappingInterface
	projects             osclient.ProjectInterface
	oc                   osclient.Interface
	kc                   kclientset.Interface
	f                    *clientcmd.Factory
	mapper               meta.RESTMapper
	typer                runtime.ObjectTyper
	userObjects          []runtime.Object
	projectObjects       []runtime.Object
	namespace            string
	log                  *log.Entry
	username             string
}

func NewArchiver(users osclient.UserInterface, projects osclient.ProjectInterface,
	identities osclient.IdentityInterface, userIdentityMappings osclient.UserIdentityMappingInterface,
	f *clientcmd.Factory, oc osclient.Interface, kc kclientset.Interface, namespace string, username string) *Archiver {

	aLog := log.WithFields(log.Fields{
		"namespace": namespace,
		"user":      username,
	})
	mapper, typer := f.Object()
	return &Archiver{
		users:                users,
		projects:             projects,
		identities:           identities,
		userIdentityMappings: userIdentityMappings,
		oc:                   oc,
		kc:                   kc,
		userObjects:          []runtime.Object{},
		projectObjects:       []runtime.Object{},
		f:                    f,
		mapper:               mapper,
		typer:                typer,
		namespace:            namespace,
		log:                  aLog,
		username:             username,
	}
}

// Export projects where user is admin to a single .yaml file and delete those projects
func (a *Archiver) Archive() error {

	a.log.Info("beginning archival")
	projectName := a.namespace

	project, err := a.projects.Get(projectName, metav1.GetOptions{})
	if err != nil {
		a.log.Errorln("unable to lookup project", err)
		return err
	}
	a.log.Info("got project", project)

	// Check if user is admin for current project, if so then archive
	// and add project to exported template

	// Find user's role binding for this project and archive it:
	a.log.Debugln("checking role bindings")
	rbs, err := a.oc.RoleBindings(a.namespace).List(metav1.ListOptions{})
	if err != nil {
		a.log.Errorln("unable to list role bindings")
		return err
	}
	a.log.Debugf("found %d role bindings", len(rbs.Items))

	for _, rb := range rbs.Items {
		a.log.Debug("checking role binding:", rb.RoleRef.Name)
		// We're looking specifically for the admin role:
		if rb.RoleRef.Name == "admin" {
			for _, user := range rb.Subjects {
				a.log.Debug("checking subject:", user.Name)
				if user.Name == a.username {
					resourceName := fmt.Sprintf("rolebindings/%s", rb.ObjectMeta.Name)
					err := a.archive(resourceName, a.namespace)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// Get all resources for this project and archive them
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

	a.log.Debugln("archiving user identity")
	// Archive the user, along with their identity and identitymapping
	a.archiveUserIdentity(a.username, projectName)

	// Must delete project last, but have it be the first item in the
	// List template, so that it will be created first during unarchival
	a.log.Debugln("archiving project")
	projectResourceName := fmt.Sprintf("project/%s", projectName)
	err = a.archive(projectResourceName, projectName)
	if err != nil {
		a.log.Error("error archiving project")
		return err
	}

	a.log.Debugln("creating template")
	// Make exported "template", which is really just a List of resources
	template := &kapi.List{
		ListMeta: metav1.ListMeta{},
		Items:    append(a.projectObjects, a.userObjects...),
	}

	// This may be redundant config stuff, but version template list
	clientConfig, err := a.f.ClientConfig()
	var result runtime.Object
	outputVersion := *clientConfig.GroupVersion
	result, err = kapi.Scheme.ConvertToVersion(template, outputVersion)
	if err != nil {
		return err
	}

	if err := a.exportTemplate(result); err != nil {
		return err
	}

	// TODO: make this optional, may just be looking to backup?
	a.projects.Delete(projectName)

	return nil
}

// archiveProject creates a yaml template of a project and deletes the project from openshift
func (a *Archiver) archive(name, namespace string) error {
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
		case "users", "identities", "useridentitymappings":
			for _, object := range objects {
				a.userObjects = append(a.userObjects, object)
			}
		case "projects":
			for _, object := range objects {
				a.projectObjects = append([]runtime.Object{object}, a.projectObjects...)
			}
		default:
			for _, object := range objects {
				a.projectObjects = append(a.projectObjects, object)
			}
		}

		return nil
	})
	return err
}

// Archives the user identity resources (user, identity) and deletes the
// user identity mapping. These need to be archived and deleted in a specific order
// Should we bother this? Why not recreate new in the new cluster and just restore objects the user
// is genuinely interested in.
func (a *Archiver) archiveUserIdentity(username, namespace string) error {
	// TODO: watchout for this
	identityProvider := "anypassword"
	identityName := identityProvider + ":" + username

	identityResourceName := fmt.Sprintf("identities/%s", identityName)
	userResourceName := fmt.Sprintf("users/%s", username)

	// Delete mapping, then delete identity so the identity isn't referenced in the archived user resource
	// Otherwise the user won't get bound to the newly-created identity when unarchived
	a.userIdentityMappings.Delete(identityName)

	err := a.archive(identityResourceName, "")
	if err != nil {
		return err
	}
	a.identities.Delete(identityName)

	err = a.archive(userResourceName, "")
	if err != nil {
		return err
	}
	a.users.Delete(username)
	return nil
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
					RoleName:            binding.RoleRef.Name,
					RoleNamespace:       binding.RoleRef.Namespace,
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
			_, err = a.userIdentityMappings.Create(&userapi.UserIdentityMapping{
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
