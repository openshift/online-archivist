package clustermonitor

import (
	"fmt"
	"sort"
	"time"

	"github.com/openshift/online-archivist/pkg/config"
	"github.com/openshift/online-archivist/pkg/util"

	buildapi "github.com/openshift/api/build/v1"
	buildclient "github.com/openshift/client-go/build/clientset/versioned"

	arkapi "github.com/heptio/ark/pkg/apis/ark/v1"
	arkclientset "github.com/heptio/ark/pkg/generated/clientset/versioned"
	arkinformers "github.com/heptio/ark/pkg/generated/informers/externalversions"
	arkv1informers "github.com/heptio/ark/pkg/generated/informers/externalversions/ark/v1"

	"github.com/pkg/errors"
	kapiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	kcache "k8s.io/client-go/tools/cache"

	log "github.com/Sirupsen/logrus"
)

const (
	archivedNamespaceLabel     string = "archived-namespace"
	projectRequesterAnnotation string = "openshift.io/requester"
)

func NewClusterMonitor(archivistConfig config.ArchivistConfig, clusterConfig config.ClusterConfig,
	bc buildclient.Interface, kc kubernetes.Interface, arkClient arkclientset.Interface) *ClusterMonitor {

	buildLW := &kcache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return bc.BuildV1().Builds(metav1.NamespaceAll).List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return bc.BuildV1().Builds(metav1.NamespaceAll).Watch(options)
		},
	}

	// TODO: deads2k suggests switching to SharedInformerFactory from
	// https://github.com/openshift/origin/blob/master/pkg/build/generated/informers/internalversion/factory.go#L29
	// Then using .Build().Builds().AddResourceEventHandler()
	buildInformer := kcache.NewSharedIndexInformer(
		buildLW,
		&buildapi.Build{},
		0, // not currently doing any re-syncing
		kcache.Indexers{
			kcache.NamespaceIndex: kcache.MetaNamespaceIndexFunc,
		},
	)

	rcLW := &kcache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return kc.Core().ReplicationControllers(metav1.NamespaceAll).List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return kc.CoreV1().ReplicationControllers(metav1.NamespaceAll).Watch(options)
		},
	}

	rcInformer := kcache.NewSharedIndexInformer(
		rcLW,
		&kapiv1.ReplicationController{},
		0, // not currently doing any re-syncing
		kcache.Indexers{
			kcache.NamespaceIndex: kcache.MetaNamespaceIndexFunc,
		},
	)

	nsLW := &kcache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return kc.CoreV1().Namespaces().List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return kc.CoreV1().Namespaces().Watch(options)
		},
	}

	nsInformer := kcache.NewSharedIndexInformer(
		nsLW,
		&kapiv1.Namespace{},
		0, // not currently doing any re-syncing
		kcache.Indexers{
		//kcache.NamespaceIndex: kcache.MetaNamespaceIndexFunc,
		},
	)

	sharedInformerFactory := arkinformers.NewSharedInformerFactory(arkClient, 0)
	backupInformer := sharedInformerFactory.Ark().V1().Backups()
	backupInformer.Informer().AddEventHandler(
		kcache.ResourceEventHandlerFuncs{
			UpdateFunc: func(oldObj, newObj interface{}) {
				// TODO: shutdown everything here if config disables project cleanup
				backup := newObj.(*arkapi.Backup)
				bLog := log.WithFields(log.Fields{
					"name":  backup.Name,
					"phase": backup.Status.Phase,
				})
				namespace, ok := backup.Labels[archivedNamespaceLabel]
				if !ok {
					bLog.Warnf("no %s label found on backup, skipping project deletion")
					return
				}
				bLog = bLog.WithField("namespace", namespace)

				switch backup.Status.Phase {
				case arkapi.BackupPhaseCompleted:
					bLog.Infoln("ark backup completed")
					if archivistConfig.DeleteArchivedNamespaces {
						err := kc.CoreV1().Namespaces().Delete(namespace, &metav1.DeleteOptions{})
						if err != nil {
							bLog.Errorf("error cleaning up namespace after archival completed: %v", err)
							return
						}
						bLog.Infoln("namespace deleted")
					} else {
						bLog.Infoln("skipping namespace deletion")
					}
				default:
					bLog.Infoln("ark backup updated")
				}
			},
		},
	)

	a := &ClusterMonitor{
		cfg:            archivistConfig,
		clusterCfg:     clusterConfig,
		kc:             kc,
		arkClient:      arkClient,
		buildInformer:  buildInformer,
		rcInformer:     rcInformer,
		nsInformer:     nsInformer,
		buildIndexer:   buildInformer.GetIndexer(),
		rcIndexer:      rcInformer.GetIndexer(),
		nsIndexer:      nsInformer.GetIndexer(),
		backupInformer: backupInformer,
	}
	return a
}

// ClusterMonitor monitors the state of the cluster and if necessary, evaluates namespace last activity to
// determine which namespaces should be archived.
// field of type StringSet (returned by sets.NewString) populate that in clustermonitor structor based on config.ProtectedNamespaces
type ClusterMonitor struct {
	cfg          config.ArchivistConfig
	clusterCfg   config.ClusterConfig
	kc           kubernetes.Interface
	arkClient    arkclientset.Interface
	buildIndexer kcache.Indexer
	rcIndexer    kcache.Indexer
	nsIndexer    kcache.Indexer

	// Avoid use in functions other than Run, the indexers are more testable:
	buildInformer  kcache.SharedIndexInformer
	rcInformer     kcache.SharedIndexInformer
	nsInformer     kcache.SharedIndexInformer
	backupInformer arkv1informers.BackupInformer
}

func (a *ClusterMonitor) Run() {
	go a.buildInformer.Run(wait.NeverStop)
	go a.rcInformer.Run(wait.NeverStop)
	go a.nsInformer.Run(wait.NeverStop)
	go a.backupInformer.Informer().Run(wait.NeverStop)

	log.Infoln("begin waiting for informers to sync")
	syncTimer := time.NewTimer(time.Minute * 5)
	go func() {
		<-syncTimer.C
		log.Fatal("informers have not synced, timeout after 5 minutes.")
	}()
	for {
		// use hassynced method to check build, rc, and ns informers status
		if a.buildInformer.HasSynced() == true && a.rcInformer.HasSynced() == true && a.nsInformer.HasSynced() == true {
			log.Infoln("informers synced")
			syncTimer.Stop()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Run an initial check on startup:
	go a.checkCapacity(time.Now())

	// ticker for MonitorCheckInterval
	duration := a.cfg.MonitorCheckInterval
	tickerTime := time.Duration(duration)
	ticker := time.NewTicker(tickerTime)

	go func() {
		for range ticker.C {
			log.Info("checking cluster capacity")
			go func() {
				err := a.checkCapacity(time.Now())
				if err != nil {
					log.Errorf("error checking cluster capacity: %v", err)
				}
			}()
		}
	}()
	// Will run the ClusterMonitor at a input time interval
	// currently, each will stop after 10 minutes for testing purposes but this can be easily changed below
	time.Sleep(time.Minute * 10)
	ticker.Stop()
}

// checkCapacity checks the capacity by all configured metrics and determines what (if any) namespaces need to
// be archived.
func (a *ClusterMonitor) checkCapacity(checkTime time.Time) error {
	namespacesToArchive, err := a.getNamespacesToArchive(checkTime)
	if err != nil {
		return errors.Wrap(err, "error getting namespaces to archive")
	}

	for _, la := range namespacesToArchive {
		nsLog := log.WithField("namespace", la.Namespace.Name)
		if a.cfg.DryRun {
			nsLog.Warnln("skipping archival due to dry-run mode")
		} else {
			nsLog.Infoln("archiving namespace")
			err := a.archiveNamespace(la.Namespace)
			if err != nil {
				log.Errorf("error archiving namespace: %v", err)
			}
		}
	}
	return nil
}

type LastActivity struct {
	Namespace *kapiv1.Namespace
	Time      time.Time
}

type LastActivitySorter []LastActivity

func (a LastActivitySorter) Len() int           { return len(a) }
func (a LastActivitySorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a LastActivitySorter) Less(i, j int) bool { return a[i].Time.Before(a[j].Time) }

func (a *ClusterMonitor) getNamespacesToArchive(checkTime time.Time) ([]LastActivity, error) {
	if a.clusterCfg.NamespaceCapacity.HighWatermark == 0 {
		log.Warnln("no namespace capacity high watermark defined, skipping")
		return nil, nil
	}
	if a.clusterCfg.NamespaceCapacity.LowWatermark == 0 {
		log.Warnln("no namespace capacity low watermark defined, skipping")
		return nil, nil
	}

	// Calculate the actual time for our activity range:
	minInactive := checkTime.Add(time.Duration(-a.clusterCfg.MinInactiveDuration))
	maxInactive := checkTime.Add(time.Duration(-a.clusterCfg.MaxInactiveDuration))

	veryInactive := make([]LastActivity, 0, 20)     // will definitely be archived
	somewhatInactive := make([]LastActivity, 0, 20) // may be archived if we need room

	// Calculate last activity time for all namespaces and sort it:
	namespaces := a.nsIndexer.List()
	log.WithFields(log.Fields{
		"checkTime":     checkTime,
		"minInactive":   minInactive,
		"maxInactive":   maxInactive,
		"highWatermark": a.clusterCfg.NamespaceCapacity.HighWatermark,
		"lowWatermark":  a.clusterCfg.NamespaceCapacity.LowWatermark,
	}).Infoln("calculating namespaces to be archived")

	for _, nsPtr := range namespaces {
		namespace := nsPtr.(*kapiv1.Namespace)
		if util.StringInSlice(namespace.Name, a.clusterCfg.ProtectedNamespaces) {
			log.WithFields(log.Fields{"namespace": namespace.Name}).Debugln("skipping protected namespace")
			continue
		}
		lastActivity, err := a.getLastActivity(namespace.Name)
		if err != nil {
			log.Errorln(err)
			return nil, err
		}
		if lastActivity.IsZero() {
			log.WithFields(log.Fields{"namespace": namespace.Name}).Warnln("no last activity time calculated for namespace")
			continue
		}
		if lastActivity.Before(maxInactive) {
			log.WithFields(log.Fields{
				"namespace":    namespace.Name,
				"lastActivity": lastActivity,
				"checkTime":    checkTime,
				"maxInactive":  maxInactive,
			}).Infoln("found namespace over max inactive time")
			veryInactive = append(veryInactive, LastActivity{namespace, lastActivity})
		} else if lastActivity.Before(minInactive) {
			log.WithFields(log.Fields{
				"namespace":    namespace.Name,
				"lastActivity": lastActivity,
				"checkTime":    checkTime,
				"minInactive":  minInactive,
				"maxInactive":  maxInactive,
			}).Infoln("found namespace between max/min inactive times")
			somewhatInactive = append(somewhatInactive, LastActivity{namespace, lastActivity})
		}
	}
	log.WithFields(log.Fields{
		"totalNamespaces":  len(namespaces),
		"veryInactive":     len(veryInactive),
		"somewhatInactive": len(somewhatInactive),
	}).Infoln("last activity totals")

	namespacesToArchive := make([]LastActivity, len(veryInactive),
		len(veryInactive)+len(somewhatInactive))
	copy(namespacesToArchive, veryInactive)
	newNSCount := len(namespaces) - len(namespacesToArchive)

	// If the number of namespaces is over the high watermark we need to get to the low.
	// If the number of namespaces we're definitely archiving because they are very inactive
	// is not enough to get us there, we need to start archiving the somewhat inactive
	// projects:
	if len(namespaces) >= a.clusterCfg.NamespaceCapacity.HighWatermark &&
		newNSCount >= a.clusterCfg.NamespaceCapacity.LowWatermark {

		targetCount := newNSCount - a.clusterCfg.NamespaceCapacity.LowWatermark
		log.Debugf("looking for %d semi-inactive namespaces to archive", targetCount)
		if targetCount >= len(somewhatInactive) {
			// We don't have enough somewhat inactive namespaces to hit low watermark,
			// we can safely add all of them to the archive list:
			namespacesToArchive = append(namespacesToArchive, somewhatInactive...)
		} else {
			// Only now do we actually need to sort, and only the namespaces eligible for archival.
			// Sort into ascending order, and we will use the namespaces at the start of the slice
			// (i.e. those with the most recent activity get to remain, despite being within the
			// threshold for archival).
			sort.Sort(LastActivitySorter(somewhatInactive))
			namespacesToArchive = append(namespacesToArchive,
				somewhatInactive[0:targetCount]...)
		}
	}
	log.Infof("found %d namespaces to archive", len(namespacesToArchive))

	newNSCount = len(namespaces) - len(namespacesToArchive)
	if newNSCount > a.clusterCfg.NamespaceCapacity.LowWatermark {
		log.WithFields(log.Fields{
			"lowWatermark": a.clusterCfg.NamespaceCapacity.LowWatermark,
			"newNSCount":   newNSCount,
		}).Warnln("unable to reach namespace capacity low watermark")
	}

	return namespacesToArchive, nil
}

func (a *ClusterMonitor) archiveNamespace(namespace *kapiv1.Namespace) error {
	backupName := fmt.Sprintf("%s-%s", namespace.Name, time.Now().Format("20060102150405"))
	log.WithField("name", backupName).Debugln("creating backup in ark")

	snapVols := true // snapshot all associated volumes

	requester, ok := namespace.Annotations[projectRequesterAnnotation]
	if !ok {
		return fmt.Errorf("skipping archival, no %s annotation found on project: %s",
			projectRequesterAnnotation, namespace.Name)
	}

	labels := map[string]string{
		archivedNamespaceLabel: namespace.Name,
	}

	annotations := map[string]string{
		// Storing the project requester in an annotation as it may not be a suitable string for a label
		projectRequesterAnnotation: requester,
	}

	backup := &arkapi.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   arkapi.DefaultNamespace,
			Name:        backupName,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: arkapi.BackupSpec{
			IncludedNamespaces: []string{namespace.Name},

			// Exclude these resources, for some reason this API endpoint responds with status.
			// This unusual behavior breaks Ark.
			ExcludedResources: []string{"projectrequests.project.openshift.io"},

			SnapshotVolumes: &snapVols,
			TTL:             metav1.Duration{Duration: time.Duration(a.cfg.ArchiveTTL)},
		},
	}
	backup, err := a.arkClient.ArkV1().Backups(arkapi.DefaultNamespace).Create(backup)
	if err != nil {
		errors.Wrapf(err, "error creating ark backup %s", backupName)
		return err
	}
	log.WithField("name", backupName).Infoln("ark backup created")
	return nil

}

// GetLastActivity returns the last activity time for a namespace by examining its builds and replication controllers.
// If no builds or replication controllers are found we return nil. If the namespace does not exist, we return an error.
func (a *ClusterMonitor) GetLastActivity(namespace string) (time.Time, error) {
	// return an error if the namespace doesn't exist
	_, exists, err := a.nsInformer.GetIndexer().GetByKey(namespace)
	if err != nil {
		return time.Time{}, err
	}
	if !exists {
		return time.Time{}, fmt.Errorf("namespace does not exist in cache: %s", namespace)
	}

	return a.getLastActivity(namespace)
}

func (a *ClusterMonitor) getLastActivity(namespace string) (time.Time, error) {
	nsLog := log.WithFields(log.Fields{
		"namespace": namespace,
	})

	// Not necessarily a problem here, but worth warning about:
	// set lookup using structs as a key in a map
	// set from slice -> sets package (sets.NewString)
	if util.StringInSlice(namespace, a.clusterCfg.ProtectedNamespaces) {
		nsLog.Warnln("called getLastActivity for protected namespace")
	}

	var lastActivity time.Time

	builds, err := a.buildIndexer.ByIndex(kcache.NamespaceIndex, namespace)
	if err != nil {
		return time.Time{}, err
	}
	rcs, err := a.rcIndexer.ByIndex(kcache.NamespaceIndex, namespace)
	if err != nil {
		return time.Time{}, err
	}
	nsLog.WithFields(log.Fields{"builds": len(builds), "rcs": len(rcs)}).Debugln(
		"calculating last activity time")

	for _, obj := range builds {
		b := obj.(*buildapi.Build)
		// Build may briefly have no start timestamp, ignore it:
		if b.Status.StartTimestamp == nil {
			nsLog.WithFields(log.Fields{
				"name": b.Name,
				"kind": "Build",
			}).Debugln("skipping build with no start time")
			continue
		}
		ts := b.Status.StartTimestamp
		if lastActivity.IsZero() || ts.Time.After(lastActivity) {
			lastActivity = ts.Time
			nsLog.WithFields(log.Fields{
				"lastActivity": lastActivity,
				"kind":         "Build",
				"name":         b.Name,
			}).Debugln("updating last activity time")
		}
	}

	for _, obj := range rcs {
		r := obj.(*kapiv1.ReplicationController)
		if r.ObjectMeta.CreationTimestamp.Time.IsZero() {
			nsLog.WithFields(log.Fields{
				"name": r.Name,
				"kind": "ReplicationController",
			}).Debugln("skipping RC with no start time")
			continue
		}
		ts := &r.ObjectMeta.CreationTimestamp
		if lastActivity.IsZero() || ts.Time.After(lastActivity) {
			lastActivity = ts.Time
			nsLog.WithFields(log.Fields{
				"lastActivity": lastActivity,
				"kind":         "ReplicationController",
				"name":         r.Name,
			}).Debugln("updating last activity time")
		}
	}

	if lastActivity.IsZero() {
		currentNamespace, err := a.kc.CoreV1().Namespaces().Get(namespace, metav1.GetOptions{})
		if err != nil {
			return time.Time{}, err
		}
		lastActivity = currentNamespace.ObjectMeta.CreationTimestamp.Time
		nsLog.WithFields(log.Fields{
			"lastActivity": lastActivity,
		}).Infoln("no builds or RCs for namespace, using project creation time for last activity")
	}

	nsLog.WithFields(log.Fields{"lastActivity": lastActivity}).Debugln("calculated last activity")
	return lastActivity, nil
}
