package freetier

import (
	"fmt"
	"os"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/admin/policy"
	"github.com/openshift/origin/pkg/cmd/server/bootstrappolicy"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectapi "github.com/openshift/origin/pkg/project/api"
	userapi "github.com/openshift/origin/pkg/user/api"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/meta"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/kubectl"
	"k8s.io/kubernetes/pkg/kubectl/resource"
	"k8s.io/kubernetes/pkg/runtime"
)

type Archiver struct {
	users                osclient.UserInterface
	identities           osclient.IdentityInterface
	userIdentityMappings osclient.UserIdentityMappingInterface
	projects             osclient.ProjectInterface
	openshiftClient      osclient.Interface
	client               *osclient.Client
	f                    *clientcmd.Factory
	mapper               meta.RESTMapper
	typer                runtime.ObjectTyper
	userObjects          []runtime.Object
	projectObjects       []runtime.Object
	unarchivalNamespace  string
}

func NewArchiver(users osclient.UserInterface, projects osclient.ProjectInterface,
	identities osclient.IdentityInterface, userIdentityMappings osclient.UserIdentityMappingInterface,
	f *clientcmd.Factory, openshiftClient osclient.Interface, client *osclient.Client, namespace string) *Archiver {

	mapper, typer := f.Object()
	return &Archiver{
		users:                users,
		projects:             projects,
		identities:           identities,
		userIdentityMappings: userIdentityMappings,
		openshiftClient:      openshiftClient,
		client:               client,
		userObjects:          []runtime.Object{},
		projectObjects:       []runtime.Object{},
		f:                    f,
		mapper:               mapper,
		typer:                typer,
		unarchivalNamespace:  namespace,
	}
}

// Export projects where user is admin to a single .json file and delete those projects
func (a *Archiver) ArchiveUser(username string) error {
	// Get all projects
	projects, err := a.projects.List(kapi.ListOptions{})
	if err != nil {
		return err
	}

	for _, project := range projects.Items {
		projectName := project.ObjectMeta.Name

		// Check if user is admin for current project, if so then archive
		// and add project to exported template
		if a.userIsRole(projectName, username, "admin") {
			if err != nil {
				fmt.Println(err)
			}

			// Get all resources for this project and archive them
			r := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping), kapi.Codecs.UniversalDecoder()).
				NamespaceParam(projectName).RequireNamespace().
				ResourceTypeOrNameArgs(true, "all").
				Flatten().
				Do()

			err = r.Visit(func(info *resource.Info, err error) error {
				// Don't need pods, RCs
				if info.ResourceMapping().Resource != "pods" &&
					info.ResourceMapping().Resource != "replicationcontrollers" {
					resourceName := fmt.Sprintf("%s/%s", info.ResourceMapping().Resource, info.Name)

					// Archive the resource
					fmt.Printf("Archiving: %s\n", resourceName)
					a.archive(resourceName, projectName)
				}
				return nil
			})

			// Archive the user, along with their identity and identitymapping
			a.archiveUserIdentity(username, projectName)

			// Must delete project last, but have it be the first item in the
			// List template, so that it will be created first during unarchival
			projectResourceName := fmt.Sprintf("project/%s", projectName)
			fmt.Printf("Archiving: %s\n", projectResourceName)
			a.archive(projectResourceName, projectName)
		}
	}

	// Make exported "template", which is really just a List of resources
	template := &kapi.List{
		ListMeta: unversioned.ListMeta{},
		Items:    append(a.projectObjects, a.userObjects...),
	}

	// This may be redundant config stuff, but version template list
	clientConfig, err := a.f.ClientConfig()
	var result runtime.Object
	outputVersion := *clientConfig.GroupVersion
	result, err = kapi.Scheme.ConvertToVersion(template, outputVersion.String())
	if err != nil {
		return err
	}

	// Save template list
	if err := exportTemplate(result, username); err != nil {
		return err
	}

	return nil
}

// Import a .json file and create the resources contained therein
// TODO user role bindings so that same permissions get restored to project
func (a *Archiver) UnarchiveUser(username string) error {
	filename := fmt.Sprintf("%s.json", username)
	_, explicit, err := a.f.DefaultNamespace()
	if err != nil {
		fmt.Println(err)
	}

	// Create resource and Infos from .json template file
	r := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping), kapi.Codecs.UniversalDecoder()).
		FilenameParam(explicit, filename).
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
			if a.unarchivalNamespace == "" {
				a.unarchivalNamespace = info.Name
			} else {
				info.Object.(*projectapi.Project).ObjectMeta.Name = a.unarchivalNamespace
			}
			fmt.Printf("Unarchiving: %s/%s in namespace %s\n", info.ResourceMapping().Resource, info.Name, a.unarchivalNamespace)
			obj, err := resource.NewHelper(info.Client, info.Mapping).Create(a.unarchivalNamespace, true, info.Object)

			// Add default service account role bindings to newly-created project
			for _, binding := range bootstrappolicy.GetBootstrapServiceAccountProjectRoleBindings(a.unarchivalNamespace) {
				addRole := &policy.RoleModificationOptions{
					RoleName:            binding.RoleRef.Name,
					RoleNamespace:       binding.RoleRef.Namespace,
					RoleBindingAccessor: policy.NewLocalRoleBindingAccessor(a.unarchivalNamespace, a.client),
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
			fmt.Printf("Unarchiving: %s/%s in namespace %s\n", info.ResourceMapping().Resource, info.Name, a.unarchivalNamespace)
			obj, err := resource.NewHelper(info.Client, info.Mapping).Create(a.unarchivalNamespace, true, info.Object)
			if err != nil {
				return err
			}
			info.Refresh(obj, true)
			_, err = a.userIdentityMappings.Create(&userapi.UserIdentityMapping{
				User:     kapi.ObjectReference{Name: username},
				Identity: kapi.ObjectReference{Name: "anypassword:" + username},
			})
			if err != nil {
				fmt.Println(err)
			}
			info.Refresh(obj, true)
		default:
			fmt.Printf("Unarchiving: %s/%s in namespace %s\n", info.ResourceMapping().Resource, info.Name, a.unarchivalNamespace)
			obj, err := resource.NewHelper(info.Client, info.Mapping).Create(a.unarchivalNamespace, true, info.Object)
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

// userIsRole iterates through the role bindings for a specific project (rbs)
// and checks if the user requested through the username flag has the requested
// role binding in the project. If they do, it will archive that rolebinding
func (a *Archiver) userIsRole(projectName, username, role string) bool {
	rbs, err := a.openshiftClient.RoleBindings(projectName).List(kapi.ListOptions{})
	if err != nil {
		fmt.Println(err)
		return false
	}

	for _, rb := range rbs.Items {
		if rb.RoleRef.Name == role {
			for _, user := range rb.Subjects {
				if user.Name == username {
					resourceName := fmt.Sprintf("rolebindings/%s", rb.ObjectMeta.Name)
					fmt.Printf("Archiving: %s\n", resourceName)
					a.archive(resourceName, projectName)
					return true
				}
			}
		}
	}
	return false
}

// archiveProject creates a JSON template of a project and deletes the project from openshift
func (a *Archiver) archive(name, namespace string) {
	b := resource.NewBuilder(a.mapper, a.typer, resource.ClientMapperFunc(a.f.ClientForMapping), kapi.Codecs.UniversalDecoder()).
		NamespaceParam(namespace).DefaultNamespace().
		ResourceTypeOrNameArgs(true, name).
		Flatten()

	one := false
	infos, err := b.Do().IntoSingular(&one).Infos()
	if err != nil {
		fmt.Println(err)
	}

	// May be redundant/unnecessary config settings
	clientConfig, err := a.f.ClientConfig()
	outputVersion := *clientConfig.GroupVersion
	objects, err := resource.AsVersionedObjects(infos, outputVersion.String(), kapi.Codecs.LegacyCodec(outputVersion))
	if err != nil {
		fmt.Println(err)
	}

	// Delete resource, if it is not a user identity resource
	err = b.Do().Visit(func(info *resource.Info, err error) error {
		if err != nil {
			return err
		}

		delete := true
		switch info.ResourceMapping().Resource {
		case "users", "identities", "useridentitymappings":
			delete = false
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

		if delete == true {
			if err := resource.NewHelper(info.Client, info.Mapping).Delete(info.Namespace, info.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

// Archives the user identity resources (user, identity) and deletes the
// user identity mapping. These need to be archived and deleted in a specific order
func (a *Archiver) archiveUserIdentity(username, namespace string) {
	identityProvider := "anypassword"
	identityName := identityProvider + ":" + username

	identityResourceName := fmt.Sprintf("identities/%s", identityName)
	userResourceName := fmt.Sprintf("users/%s", username)

	// Delete mapping, then delete identity so the identity isn't referenced in the archived user resource
	// Otherwise the user won't get bound to the newly-created identity when unarchived
	a.userIdentityMappings.Delete(identityName)

	fmt.Printf("Archiving: %s\n", identityResourceName)
	a.archive(identityResourceName, "")
	a.identities.Delete(identityName)

	fmt.Printf("Archiving: %s\n", userResourceName)
	a.archive(userResourceName, "")
	a.users.Delete(username)
}

// exportTemplate takes a resultant object and prints it to a .json file
func exportTemplate(obj runtime.Object, name string) error {
	p, _, err := kubectl.GetPrinter("json", "")
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("%s.json", name)
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
