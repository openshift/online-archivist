package clustermonitor

import (
	"testing"
	"time"

	"github.com/openshift/online/archivist/pkg/config"

	buildapi "github.com/openshift/origin/pkg/build/apis/build"
	otestclient "github.com/openshift/origin/pkg/client/testclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kcache "k8s.io/client-go/tools/cache"
	kapi "k8s.io/kubernetes/pkg/api"
	ktestclient "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/fake"

	"fmt"

	log "github.com/Sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func init() {
	log.SetLevel(log.DebugLevel)
}

type NamespaceTestData struct {
	name                 string
	builds               []*buildapi.Build
	rcs                  []*kapi.ReplicationController
	expectedLastActivity time.Time
}

func fakeNamespace(name string) *kapi.Namespace {
	p := kapi.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	return &p
}

func fakeBuild(projName string, name string, start time.Time) *buildapi.Build {
	buildStart := metav1.NewTime(start)
	b := buildapi.Build{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: projName,
		},
		Status: buildapi.BuildStatus{
			StartTimestamp: &buildStart,
		},
	}
	return &b
}

func fakeRC(projName string, name string, created time.Time) *kapi.ReplicationController {
	uct := metav1.NewTime(created)
	rc := kapi.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         projName,
			CreationTimestamp: uct,
		},
	}
	return &rc
}

func tm(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestNamespaceLastActivity(t *testing.T) {

	tests := []struct {
		name       string
		namespaces []NamespaceTestData
	}{
		{
			name: "single namespace single build",
			namespaces: []NamespaceTestData{
				{
					name: "namespace1",
					builds: []*buildapi.Build{
						fakeBuild("namespace1", "build-1", tm(2016, time.November, 1)),
					},
					expectedLastActivity: tm(2016, time.November, 1),
				},
			},
		},

		{
			name: "single namespace single RC",
			namespaces: []NamespaceTestData{
				{
					name: "namespace1",
					rcs: []*kapi.ReplicationController{
						fakeRC("namespace1", "rc-1", tm(2016, time.November, 1)),
					},
					expectedLastActivity: tm(2016, time.November, 1),
				},
			},
		},

		{
			name: "no builds or RCs namespace",
			namespaces: []NamespaceTestData{
				{
					name:                 "namespace1",
					expectedLastActivity: time.Time{},
				},
			},
		},
		{
			name: "single namespace multi build",
			namespaces: []NamespaceTestData{
				{
					name: "namespace1",
					builds: []*buildapi.Build{
						fakeBuild("namespace1", "build-1", tm(2016, time.November, 1)),
						fakeBuild("namespace1", "build-2", tm(2017, time.May, 19)),
						fakeBuild("namespace1", "build-3", tm(2017, time.January, 1)),
					},
					expectedLastActivity: tm(2017, time.May, 19),
				},
			},
		},
		{
			name: "single namespace multi RC",
			namespaces: []NamespaceTestData{
				{
					name: "namespace1",
					rcs: []*kapi.ReplicationController{
						fakeRC("namespace1", "rc-1", tm(2016, time.November, 1)),
						fakeRC("namespace1", "rc-2", tm(2017, time.May, 19)),
						fakeRC("namespace1", "rc-3", tm(2017, time.January, 1)),
					},
					expectedLastActivity: tm(2017, time.May, 19),
				},
			},
		},
		{
			name: "single namespace mixed builds and RCs",
			namespaces: []NamespaceTestData{
				{
					name: "namespace1",
					builds: []*buildapi.Build{
						fakeBuild("namespace1", "build-1", tm(2016, time.November, 1)),
						fakeBuild("namespace1", "build-2", tm(2017, time.November, 19)),
						fakeBuild("namespace1", "build-3", tm(2017, time.January, 1)),
					},
					rcs: []*kapi.ReplicationController{
						fakeRC("namespace1", "rc-1", tm(2016, time.December, 1)),
						fakeRC("namespace1", "rc-2", tm(2017, time.July, 19)),
						fakeRC("namespace1", "rc-3", tm(2017, time.February, 1)),
					},
					expectedLastActivity: tm(2017, time.November, 19),
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oc := &otestclient.Fake{}
			kc := &ktestclient.Clientset{}

			aConfig := config.NewDefaultArchivistConfig()
			cm := NewClusterMonitor(aConfig, aConfig.Clusters[0], oc, kc)

			// Building our indexers to bypass the Informer framework, which is more
			// complicated to test and looks to involve sleeping until the informer
			// threads can run with the given testdata:
			cm.buildIndexer = kcache.NewIndexer(kcache.MetaNamespaceKeyFunc,
				kcache.Indexers{
					kcache.NamespaceIndex: kcache.MetaNamespaceIndexFunc,
				})
			cm.rcIndexer = kcache.NewIndexer(kcache.MetaNamespaceKeyFunc,
				kcache.Indexers{
					kcache.NamespaceIndex: kcache.MetaNamespaceIndexFunc,
				})

			// Add all test data to the cluster monitor first:
			for _, p := range tc.namespaces {
				for i := range p.builds {
					cm.buildIndexer.Add(p.builds[i])
				}
				for i := range p.rcs {
					cm.rcIndexer.Add(p.rcs[i])
				}
			}

			// Run through again for the actual testing:
			for _, p := range tc.namespaces {
				ts, err := cm.getLastActivity(p.name)
				if assert.Nil(t, err) {
					assert.Equal(t, p.expectedLastActivity, ts)
				}
			}
		})
	}
}

type NamespaceCapacityTestData struct {
	name         string
	lastActivity time.Time
}

func TestGetNamespacesToArchive(t *testing.T) {

	// create values for testing durations
	value1, _ := time.ParseDuration("720h")
	minInactiveDuration := config.MyDuration(value1)
	value2, _ := time.ParseDuration("1440h")
	maxInactiveDuration := config.MyDuration(value2)

	tests := []struct {
		name                string
		highWatermark       int
		lowWatermark        int
		MaxInactiveDuration config.MyDuration
		MinInactiveDuration config.MyDuration
		namespaces          []NamespaceCapacityTestData
		checkTime           time.Time
		expected            []string
	}{
		{
			name:                "over capacity oldest eligible inactive evicted",
			highWatermark:       5,
			lowWatermark:        3,
			MaxInactiveDuration: maxInactiveDuration, // Mar 30
			MinInactiveDuration: minInactiveDuration, // April 29
			checkTime:           tm(2017, time.May, 29),
			namespaces: []NamespaceCapacityTestData{
				{"vinactive1", tm(2015, time.January, 7)},
				{"vinactive2", tm(2016, time.January, 5)},
				{"vinactive3", tm(2017, time.January, 9)},
				{"vinactive4", tm(2017, time.February, 14)},
				{"vinactive5", tm(2017, time.March, 20)},
				{"inactive6", tm(2017, time.April, 25)},
				{"inactive7", tm(2017, time.April, 27)},
				{"active1", tm(2017, time.May, 25)},
				{"active2", tm(2017, time.May, 20)},
			},
			// inactive7 should not get archived unnecessarily:
			expected: []string{"vinactive1", "vinactive2", "vinactive3", "vinactive4", "vinactive5", "inactive6"},
		},
		{
			name:                "over capacity but no namespaces eligible for archival",
			highWatermark:       5,
			lowWatermark:        3,
			MaxInactiveDuration: maxInactiveDuration, // Mar 30
			MinInactiveDuration: minInactiveDuration, // April 29
			checkTime:           tm(2017, time.May, 29),
			namespaces: []NamespaceCapacityTestData{
				{"active1", tm(2017, time.May, 20)},
				{"active2", tm(2017, time.May, 25)},
				{"active3", tm(2017, time.May, 1)},
				{"active4", tm(2017, time.May, 17)},
				{"active5", tm(2017, time.May, 25)},
				{"active6", tm(2017, time.May, 20)},
			},
			expected: []string{},
		},
		{
			name:                "over capacity some namespaces eligible for archival but not enough",
			highWatermark:       5,
			lowWatermark:        3,
			MaxInactiveDuration: maxInactiveDuration, // Mar 30
			MinInactiveDuration: minInactiveDuration, // April 29
			checkTime:           tm(2017, time.May, 29),
			namespaces: []NamespaceCapacityTestData{
				{"active1", tm(2017, time.May, 20)},
				{"active2", tm(2017, time.May, 25)},
				{"inactive1", tm(2017, time.February, 1)},
				{"active4", tm(2017, time.May, 17)},
				{"active5", tm(2017, time.May, 25)},
				{"active6", tm(2017, time.May, 20)},
			},
			expected: []string{"inactive1"},
		},
		{
			name:                "under capacity but some namespaces over max inactivity",
			highWatermark:       5,
			lowWatermark:        3,
			MaxInactiveDuration: maxInactiveDuration, // Mar 30
			MinInactiveDuration: minInactiveDuration, // April 29
			checkTime:           tm(2017, time.May, 29),
			namespaces: []NamespaceCapacityTestData{
				{"inactive1", tm(2017, time.January, 20)},
				{"active2", tm(2017, time.May, 25)},
				{"inactive2", tm(2017, time.February, 1)},
			},
			expected: []string{"inactive1", "inactive2"},
		},
		{
			name:                "under capacity but protected namespaces over max inactivity",
			highWatermark:       5,
			lowWatermark:        3,
			MaxInactiveDuration: maxInactiveDuration, // Mar 30
			MinInactiveDuration: minInactiveDuration, // April 29
			checkTime:           tm(2017, time.May, 29),
			namespaces: []NamespaceCapacityTestData{
				{"openshift-infra", tm(2017, time.January, 20)},
				{"default", tm(2012, time.January, 20)},
				{"active2", tm(2017, time.May, 25)},
				{"inactive2", tm(2017, time.February, 1)},
			},
			expected: []string{"inactive2"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oc := &otestclient.Fake{}
			kc := &ktestclient.Clientset{}

			aConfig := config.NewDefaultArchivistConfig()
			aConfig.Clusters[0].NamespaceCapacity.HighWatermark = tc.highWatermark
			aConfig.Clusters[0].NamespaceCapacity.LowWatermark = tc.lowWatermark
			aConfig.Clusters[0].MaxInactiveDuration = tc.MaxInactiveDuration
			aConfig.Clusters[0].MinInactiveDuration = tc.MinInactiveDuration

			cm := NewClusterMonitor(aConfig, aConfig.Clusters[0], oc, kc)

			cm.nsIndexer = kcache.NewIndexer(kcache.MetaNamespaceKeyFunc, kcache.Indexers{})
			cm.rcIndexer = kcache.NewIndexer(kcache.MetaNamespaceKeyFunc, kcache.Indexers{
				kcache.NamespaceIndex: kcache.MetaNamespaceIndexFunc,
			})
			cm.buildIndexer = kcache.NewIndexer(kcache.MetaNamespaceKeyFunc,
				kcache.Indexers{
					kcache.NamespaceIndex: kcache.MetaNamespaceIndexFunc,
				})

			// Add all test data to the cluster monitor first:
			for _, p := range tc.namespaces {
				// Add a single build with the requested activity time:
				build := fakeBuild(p.name, p.name, p.lastActivity)
				cm.buildIndexer.Add(build)
				cm.nsIndexer.Add(fakeNamespace(p.name))
			}

			archiveNamespaces, err := cm.getNamespacesToArchive(tc.checkTime)
			if assert.NoError(t, err) {
				assertNamespaces(t, tc.expected, archiveNamespaces)
			}
		})
	}
}

func assertNamespaces(t *testing.T, expected []string, archiveNamespaces []LastActivity) {
	if assert.Equal(t, len(expected), len(archiveNamespaces)) {
		for _, expectedName := range expected {
			found := false
			for _, la := range archiveNamespaces {
				if la.Namespace.Name == expectedName {
					found = true
					break
				}
			}
			assert.True(t, found, fmt.Sprintf("namespace %s was not found in results", expectedName))
		}
	}
}
