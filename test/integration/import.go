package integration

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"
	gm "github.com/onsi/gomega"

	"github.com/openshift/online-archivist/pkg/archive"
	projectv1 "github.com/openshift/origin/pkg/project/apis/project/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/kubectl/resource"
)

type testVisitor struct {
	InjectErr error
	Infos     []*resource.Info
}

func (v *testVisitor) Handle(info *resource.Info, err error) error {
	if err != nil {
		return err
	}
	v.Infos = append(v.Infos, info)
	return v.InjectErr
}

func (v *testVisitor) Objects() []runtime.Object {
	objects := []runtime.Object{}
	for i := range v.Infos {
		objects = append(objects, v.Infos[i].Object)
	}
	return objects
}

func getTestProjectNameImport(prefix string) string {
	rand.Seed(time.Now().Unix())
	i := rand.Intn(10000)
	return fmt.Sprintf("%s-%d", prefix, i)
}

func testImportViaStream(t *testing.T, h *testHarness) {
	gm.RegisterTestingT(t)
	pn := getTestProjectName("importtest")
	log.SetLevel(log.DebugLevel)
	tlog := log.WithFields(log.Fields{
		"namespace": pn,
		"test":      "importtest",
	})

	testProject := &projectv1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pn,
			Namespace:   "",
			Annotations: map[string]string{},
		},
	}
	var err error
	testProject, err = h.pc.ProjectV1().Projects().Create(testProject)
	if err != nil {
		t.Fatal("error creating project:", err)
	}
	tlog.Info("created test project")
	defer h.pc.ProjectV1().Projects().Delete(pn, &metav1.DeleteOptions{})

	/*
		expected := []string{
			"BuildConfig/testbc",
			"DeploymentConfig/testdc",
			"Secret/testsecret",
			"Secret/dockerbuildsecret",
			"ServiceAccount/testserviceaccount",
			"ServiceAccount/builder",
			"ServiceAccount/deployer",
			"ServiceAccount/default",
			"ImageStream/integratedregistry",
			"ImageStream/postgresql",
		}
	*/

	a := archive.NewArchiver(h.pc, h.ac, h.uc, h.uidmc, h.idc,
		h.clientFactory, h.oc, h.kc, pn, "user")

	file, err := ioutil.ReadFile("user.yaml")
	if err != nil {
		log.Info("error reading file")
	}

	err = a.Import(string(file))
	gm.Expect(err).NotTo(gm.HaveOccurred())
}
