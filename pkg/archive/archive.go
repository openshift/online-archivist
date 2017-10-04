package archive

import (
	"bytes"
	"fmt"
	"os"

	authclientset "github.com/openshift/origin/pkg/authorization/generated/clientset"
	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	userclientset "github.com/openshift/origin/pkg/user/generated/clientset"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	*clientcmd.Factory

	pc projectclientset.Interface
	ac authclientset.Interface
	uc userclientset.Interface

	// TODO: Legacy client usage here until we find their equivalent in new generated clientsets:
	uidmc osclient.UserIdentityMappingInterface
	idc   osclient.IdentityInterface

	f               *clientcmd.Factory
	oc              osclient.Interface
	kc              kclientset.Interface
	Exporter        *Exporter
	Importer        *Importer
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

	exporter := NewExporter(projectClient, f, oc, kc, namespace, username)
	importer := NewImporter(projectClient, f, oc, kc, namespace, username)

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

		Exporter: exporter,
		Importer: importer,
		mapper:   mapper,
		typer:    typer,
		log:      aLog,
	}
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

	objList, err := a.Exporter.Export()
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

// createAndRefresh creates an object from input info and refreshes info with that object
func createAndRefresh(info *resource.Info) error {
	obj, err := resource.NewHelper(info.Client, info.Mapping).Create(info.Namespace, true, info.Object)
	if err != nil {
		return err
	}
	info.Refresh(obj, true)
	return nil
}

// ObjKind uses the object typer to lookup the plain kind string for an object. (i.e. Project,
// Secret, BuildConfig, etc)
func ObjKind(t runtime.ObjectTyper, o runtime.Object) (string, error) {
	kinds, _, err := t.ObjectKinds(o)
	if err != nil {
		return "", fmt.Errorf("unable to lookup kind for object: %v", err)
	}
	return kinds[0].Kind, nil
}
