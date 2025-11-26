package rule

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"harmonycloud.cn/log-collector/pkg/adapter"
	"harmonycloud.cn/log-collector/pkg/apis/logcollector/v1alpha1"
	"harmonycloud.cn/log-collector/pkg/types"
	"harmonycloud.cn/log-collector/pkg/utils"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	pkgtypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/staging/src/k8s.io/apimachinery/pkg/api/errors"
	"path/filepath"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"strings"
	"sync"
	"time"
)

// Reconciler type
type Reconciler struct {
	client.Client
	Cache          cache.Indexer
	Recorder       record.EventRecorder
	Template       string
	BaseDir        string
	GlobalConf     string
	HostName       string
	NamespacedName pkgtypes.NamespacedName
	sync.Mutex
	Collector adapter.Collector
	Runtime   string
}

type ContainerRuleSlice []v1alpha1.ContainerRule

func (s ContainerRuleSlice) Len() int           { return len(s) }
func (s ContainerRuleSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ContainerRuleSlice) Less(i, j int) bool { return s[i].Containers[0] < s[j].Containers[0] }

type ruleSlice []v1alpha1.CollectionRule

func (s ruleSlice) Len() int           { return len(s) }
func (s ruleSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ruleSlice) Less(i, j int) bool { return s[i].Path[0] < s[j].Path[0] }

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	collectionRule := &v1alpha1.LogCollectionRule{}
	var oldCollectionRule *v1alpha1.LogCollectionRule
	err := r.Client.Get(ctx, req.NamespacedName, collectionRule)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Error(err, " get rule err")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	item, exists, err := r.Cache.GetByKey(req.String())
	if err != nil {
		klog.Error(err, " get cache err")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if exists {
		oldCollectionRule = item.(*v1alpha1.LogCollectionRule)
	} else {
		oldCollectionRule = nil
	}
	if collectionRule.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(collectionRule, types.FinalizerName) {
			controllerutil.AddFinalizer(collectionRule, types.FinalizerName)
			if err := r.Client.Update(ctx, collectionRule); err != nil {
				if errors.IsConflict(err) {
					return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, nil
				}
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if oldCollectionRule == nil {
			err := r.createEvent(ctx, collectionRule)
			if err != nil {
				klog.Error(err, " deal with create event err")
				return ctrl.Result{}, err
			}
			err = r.Cache.Update(collectionRule)
			if err != nil {
				klog.Error(err, " update cache err")
				return ctrl.Result{}, err
			}
			klog.Info("cache create")
		} else {
			err := r.updateEvent(ctx, collectionRule, oldCollectionRule)
			if err != nil {
				klog.Error(err, " deal with update event err")
				return ctrl.Result{}, err
			}
			err = r.Cache.Update(collectionRule)
			if err != nil {
				klog.Error(err, " update cache err")
				return ctrl.Result{}, err
			}
			klog.Info("cache update")
		}
	} else {
		if controllerutil.ContainsFinalizer(collectionRule, types.FinalizerName) {
			klog.Info(fmt.Sprintf("delete event for %s/%s", collectionRule.Name, collectionRule.Namespace))
			err := r.deleteEvent(ctx, collectionRule)
			if err != nil {
				klog.Error(err, " deal with delete event err")
				return ctrl.Result{}, err
			}
			err = r.Cache.Delete(collectionRule)
			if err != nil {
				klog.Error(err, " delete cache err")
				return ctrl.Result{}, err
			}
			klog.Info("cache delete")
		}
		controllerutil.RemoveFinalizer(collectionRule, types.FinalizerName)
		if err := r.Update(ctx, collectionRule); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) createEvent(ctx context.Context, collectionRule *v1alpha1.LogCollectionRule) error {
	klog.Info(fmt.Sprintf("create event for %s/%s", collectionRule.Name, collectionRule.Namespace))
	needUpdate := mustSetUID(collectionRule)
	if needUpdate {
		if err := r.Client.Update(ctx, collectionRule); err != nil {
			return err
		}
	}
	for _, collector := range collectionRule.Spec.Collectors {
		if !collector.Enable {
			continue
		}
		podList := &v1.PodList{}
		selector := labels.SelectorFromSet(collector.MatchLabels)
		err := r.Client.List(ctx, podList, &client.ListOptions{LabelSelector: selector, Namespace: collectionRule.Namespace})
		if err != nil {
			klog.Error(err, " err when list pod")
			return err
		}
		for _, pod := range podList.Items {
			if pod.Spec.NodeName != r.HostName {
				continue
			}
			c := collector
			err = r.newInput(c.ContainerRule, &pod, collectionRule)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func mustSetUID(collectionRule *v1alpha1.LogCollectionRule) bool {
	needUpdate := false
	for i, collector := range collectionRule.Spec.Collectors {
		for j, containerRule := range collector.ContainerRule {
			for k, rule := range containerRule.Rules {
				if rule.UID != "" {
					continue
				}
				collectionRule.Spec.Collectors[i].ContainerRule[j].Rules[k].UID = uuid.New().String()
				needUpdate = true
			}
		}
	}
	return needUpdate
}

func (r *Reconciler) updateEvent(ctx context.Context, collectionRule, oldCollectionRule *v1alpha1.LogCollectionRule) error {
	klog.Info(fmt.Sprintf("update event for %s/%s", collectionRule.Name, collectionRule.Namespace))
	if reflect.DeepEqual(collectionRule.Spec, oldCollectionRule.Spec) {
		klog.Info("rule spec not change,skip")
		return nil
	}
	err := r.deleteEvent(ctx, oldCollectionRule)
	if err != nil {
		klog.Error("delete config when deal with update event err: ", err)
		return err
	}
	err = r.createEvent(ctx, collectionRule)
	if err != nil {
		klog.Error("create config when deal with update event err: ", err)
		return err
	}

	return nil
}

func (r *Reconciler) deleteEvent(ctx context.Context, collectionRule *v1alpha1.LogCollectionRule) error {
	for _, collector := range collectionRule.Spec.Collectors {
		if !collector.Enable {
			continue
		}
		podList := &v1.PodList{}
		selector := labels.SelectorFromSet(collector.MatchLabels)
		err := r.Client.List(ctx, podList, &client.ListOptions{LabelSelector: selector, Namespace: collectionRule.Namespace})
		if err != nil {
			klog.Error(err, " err when list pod")
			return err
		}
		for _, pod := range podList.Items {
			if pod.Spec.NodeName != r.HostName {
				continue
			}
			err := r.deletePodConfig(&pod, false, collectionRule.Name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *Reconciler) deletePodConfig(pod *v1.Pod, isPodDeleted bool, ruleName string) error {
	r.Lock()
	defer r.Unlock()
	err := r.Collector.DeleteConfig(types.Parameters{Pod: pod, IsPodDeleted: isPodDeleted, RuleName: ruleName})
	if err != nil {
		return err
	}
	return err
}

func (r *Reconciler) newInput(rules []v1alpha1.ContainerRule, pod *v1.Pod, collectionRule *v1alpha1.LogCollectionRule) error {
	r.Lock()
	defer r.Unlock()
	klog.Infof("prepare to generate new input for pod %v/%v", pod.Name, pod.Namespace)
	containerLogModels := r.parseRule(rules, pod)
	runtime := utils.GetRuntime(r.Runtime)
	for i := range containerLogModels {
		mountPoint, err := runtime.FindMount(pod, &containerLogModels[i], r.BaseDir)
		if err != nil {
			return err
		}
		pvcPath := getPVCPath(pod, containerLogModels[i].Container)
		mountPoint = setIfPVC(mountPoint, pvcPath)
		containerLogModels[i].Mount = mountPoint
		klog.Info(fmt.Sprintf("pod %s/%s container %v mount: %v", pod.Name, pod.Namespace, containerLogModels[i].Container, mountPoint))
	}
	initConf := r.GlobalConf
	if v, ok := collectionRule.Annotations[types.GlobalConfigAnno]; ok {
		initConf = v
	}
	return r.Collector.NewConfig(containerLogModels, r.Template, types.Parameters{Pod: pod, InitConfigPath: initConf, RuleName: collectionRule.Name})
}

func getPVCPath(pod *v1.Pod, container string) []string {
	pvc := make(map[string]struct{})
	var pvcPath []string
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName != "" {
			pvc[volume.Name] = struct{}{}
		}
	}
	for _, c := range pod.Spec.Containers {
		if c.Name != container {
			continue
		}
		for _, volumeMount := range c.VolumeMounts {
			if _, ok := pvc[volumeMount.Name]; ok {
				pvcPath = append(pvcPath, volumeMount.MountPath)
			}
		}
	}
	return pvcPath
}

func setIfPVC(mountPoint []types.Mount, pvcPath []string) []types.Mount {
	for i, mount := range mountPoint {
		for _, pvc := range pvcPath {
			relPath, err := filepath.Rel(pvc, mount.Destination)
			if err != nil {
				klog.Errorf("failed to rel file path for %v and %v", pvc, mount.Destination)
				continue
			}
			if strings.HasPrefix(relPath, "..") || relPath == ".." {
				continue
			}
			mountPoint[i].IsPVC = true
		}
	}
	return mountPoint
}

// make sure each container in pod has containerID
func (r *Reconciler) isContainerReady(pod *v1.Pod) bool {
	if len(pod.Status.ContainerStatuses) == 0 && len(pod.Status.InitContainerStatuses) == 0 {
		return false
	}
	// there is running init containers
	for _, initContainerStatus := range pod.Status.InitContainerStatuses {
		if initContainerStatus.ContainerID != "" && initContainerStatus.State.Running != nil {
			return true
		}
	}
	// need at least one container has containerID
	// even if container is not running
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.ContainerID != "" {
			return true
		}
	}
	return false
}

func (r *Reconciler) parseRule(rules []v1alpha1.ContainerRule, pod *v1.Pod) []types.ContainerLogModel {
	var containerLogModels []types.ContainerLogModel
	for _, containerRule := range rules {
		for _, rule := range containerRule.Rules {
			logModel := types.ContainerLogModel{}
			logModel.Index = rule.Elasticsearch.Index
			for _, p := range rule.Path {
				logModel.LogPath = append(logModel.LogPath, p)
			}
			if rule.Stdout {
				logModel.LogPath = append(logModel.LogPath, "stdout")
			}
			logModel.PodName = pod.Name
			logModel.Node = pod.Spec.NodeName
			logModel.PodUID = string(pod.GetUID())
			logModel.PodNamespace = pod.Namespace
			logModel.Fields = make(map[string]string)
			for k, v := range rule.Fields {
				logModel.Fields[k] = v
			}
			logModel.RootConfigurations = make(map[string]interface{})
			for k, v := range rule.RootConfigurations {
				if v == "true" {
					logModel.RootConfigurations[k] = true
					continue
				}
				if v == "false" {
					logModel.RootConfigurations[k] = false
					continue
				}
				logModel.RootConfigurations[k] = v
			}
			logModel.Tags = append(logModel.Tags, rule.Tags...)
			logModel.Topic = rule.Kafka.Topic
			logModel.ExcludeFiles = rule.ExcludeFiles
			if len(containerRule.Containers) == 0 {
				for _, container := range pod.Status.ContainerStatuses {
					logModel.UID = rule.UID + "-" + container.Name
					newModel := logModel
					newModel.Container = container.Name
					containerLogModels = append(containerLogModels, newModel)
				}
			} else {
				for _, container := range containerRule.Containers {
					logModel.UID = rule.UID + "-" + container
					newModel := logModel
					newModel.Container = container
					containerLogModels = append(containerLogModels, newModel)
				}
			}
		}
	}
	return containerLogModels
}

func (r *Reconciler) dealPodDeleted(instance *v1.Pod) bool {
	for _, containerStatus := range instance.Status.ContainerStatuses {
		if containerStatus.Ready {
			return false
		}
	}
	collectorRuleList := &v1alpha1.LogCollectionRuleList{}
	err := r.Client.List(context.TODO(), collectorRuleList, &client.ListOptions{Namespace: instance.Namespace})
	if err != nil {
		klog.Error(err, " err when list collector rule")
	}

	for _, item := range collectorRuleList.Items {
		for _, collector := range item.Spec.Collectors {
			if !collector.Enable {
				continue
			}
			find := true
			podLabels := instance.Labels
			for k, v := range collector.MatchLabels {
				if podV, ok := podLabels[k]; ok {
					if v == podV {
						continue
					}
					find = false
					break
				} else {
					find = false
					break
				}
			}
			if !find {
				continue
			}
			klog.Info(fmt.Sprintf("delete for pod %v,%v because it is deleting and all containers are stoped", instance.Name, instance.Namespace))
			err := r.deletePodConfig(instance, true, "")
			if err != nil {
				return false
			}
		}
	}
	return false
}

func (r *Reconciler) NewEventFilterFunc() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(createEvent event.CreateEvent) bool {
			if _, ok := createEvent.Object.(*v1.Pod); ok {
				return false
			}
			if _, ok := createEvent.Object.(*v1alpha1.LogCollectionRule); ok {
				return true
			}
			return false
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			if instance, ok := updateEvent.ObjectNew.(*v1.Pod); ok {
				if instance.Spec.NodeName != r.HostName {
					return false
				}
				if !instance.ObjectMeta.DeletionTimestamp.IsZero() {
					return r.dealPodDeleted(instance)
				}
				if !r.isContainerReady(instance) {
					return false
				}
				oldInstance, ok := updateEvent.ObjectOld.(*v1.Pod)
				if !ok {
					klog.Error("parse pod err")
					return false
				} else {
					needUpdate := false
					for _, cs := range instance.Status.ContainerStatuses {
						for _, ocs := range oldInstance.Status.ContainerStatuses {
							if cs.Name != ocs.Name {
								continue
							}
							if cs.ContainerID != ocs.ContainerID {
								needUpdate = true
								break
							}
						}
					}
					if !needUpdate {
						return false
					}
				}
				collectorRuleList := &v1alpha1.LogCollectionRuleList{}
				err := r.Client.List(context.TODO(), collectorRuleList, &client.ListOptions{Namespace: instance.Namespace})
				if err != nil {
					klog.Error(err, " err when list collector rule")
				}
				for _, item := range collectorRuleList.Items {
					for _, collector := range item.Spec.Collectors {
						if !collector.Enable {
							continue
						}
						find := true
						podLabels := instance.Labels
						for k, v := range collector.MatchLabels {
							if podV, ok := podLabels[k]; ok {
								if v == podV {
									continue
								}
								find = false
								break
							} else {
								find = false
								break
							}
						}
						if !find {
							continue
						}
						err = r.newInput(collector.ContainerRule, instance, &item)
						if err != nil {
							klog.Error(err, " err when new input for ", instance.Name, instance.Namespace)
							return false
						}
					}
				}
			}
			if _, ok := updateEvent.ObjectNew.(*v1alpha1.LogCollectionRule); ok {
				return true
			}
			return false
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			if instance, ok := deleteEvent.Object.(*v1.Pod); ok {
				if instance.Spec.NodeName != r.HostName {
					return false
				}
				collectorRuleList := &v1alpha1.LogCollectionRuleList{}
				err := r.Client.List(context.TODO(), collectorRuleList, &client.ListOptions{Namespace: instance.Namespace})
				if err != nil {
					klog.Error(err, " err when list collector rule")
				}
				for _, item := range collectorRuleList.Items {
					for _, collector := range item.Spec.Collectors {
						if !collector.Enable {
							continue
						}
						find := true
						podLabels := instance.Labels
						for k, v := range collector.MatchLabels {
							if podV, ok := podLabels[k]; ok {
								if v == podV {
									continue
								}
								find = false
								break
							} else {
								find = false
								break
							}
						}
						if !find {
							continue
						}
						err := r.deletePodConfig(instance, true, "")
						if err != nil {
							return false
						}
					}
				}
			}
			if _, ok := deleteEvent.Object.(*v1alpha1.LogCollectionRule); ok {
				return true
			}
			return false
		},
	}
}

// SetupWithManager setup controller to manager
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LogCollectionRule{}).
		Watches(&source.Kind{Type: &v1.Pod{}}, &handler.EnqueueRequestForObject{}).
		WithEventFilter(r.NewEventFilterFunc()).
		Complete(r)
}
