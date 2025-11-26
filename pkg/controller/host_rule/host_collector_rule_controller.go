package host_rule

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"harmonycloud.cn/log-collector/pkg/adapter"
	"harmonycloud.cn/log-collector/pkg/apis/logcollector/v1alpha1"
	"harmonycloud.cn/log-collector/pkg/types"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	pkgtypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/staging/src/k8s.io/apimachinery/pkg/api/errors"
	"path"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sync"
	"time"
)

type Reconciler struct {
	client.Client
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

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	collectionRule := &v1alpha1.HostLogCollectionRule{}
	err := r.Client.Get(ctx, req.NamespacedName, collectionRule)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Error(err, " get pod err")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
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
		if err := r.syncRule(ctx, collectionRule); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if controllerutil.ContainsFinalizer(collectionRule, types.FinalizerName) {
			klog.Info(fmt.Sprintf("delete event for %s/%s", collectionRule.Name, collectionRule.Namespace))
			err := r.deleteEvent(collectionRule)
			if err != nil {
				klog.Error(err, " deal with delete event err")
				return ctrl.Result{}, err
			}
		}
		controllerutil.RemoveFinalizer(collectionRule, types.FinalizerName)
		if err := r.Update(ctx, collectionRule); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *Reconciler) isHostMatch(ctx context.Context, collectionRule *v1alpha1.HostLogCollectionRule) bool {
	selector := labels.SelectorFromSet(collectionRule.Spec.NodeSelector)
	nodeList := &v1.NodeList{}
	err := r.Client.List(ctx, nodeList, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		klog.Error("list node error: ", err)
		return false
	}
	for _, host := range nodeList.Items {
		if host.Name == r.HostName {
			return true
		}
	}
	return false
}

func (r *Reconciler) syncRule(ctx context.Context, collectionRule *v1alpha1.HostLogCollectionRule) error {
	if !r.isHostMatch(ctx, collectionRule) {
		return r.deleteEvent(collectionRule)
	}
	needUpdate := mustSetUID(collectionRule)
	if needUpdate {
		if err := r.Client.Update(ctx, collectionRule); err != nil {
			return err
		}
	}
	for _, collector := range collectionRule.Spec.Collectors {
		c := collector
		err := r.newInput(c.HostRules, collectionRule)
		if err != nil {
			return err
		}
	}
	return nil
}

func mustSetUID(collectionRule *v1alpha1.HostLogCollectionRule) bool {
	needUpdate := false
	for i, collector := range collectionRule.Spec.Collectors {
		for j, hostRule := range collector.HostRules {
			if hostRule.UID != "" {
				continue
			}
			collectionRule.Spec.Collectors[i].HostRules[j].UID = uuid.New().String()
			needUpdate = true
		}
	}
	return needUpdate
}

func (r *Reconciler) deleteEvent(collectionRule *v1alpha1.HostLogCollectionRule) error {
	err := r.Collector.DeleteConfigForHost(types.Parameters{Node: r.HostName, RuleName: collectionRule.Name})
	if err != nil {
		return err
	}
	return nil
}

func (r *Reconciler) newInput(rules []v1alpha1.HostCollectionRule, collectionRule *v1alpha1.HostLogCollectionRule) error {
	r.Lock()
	defer r.Unlock()
	klog.Infof("prepare to generate new input for host by rule %v", collectionRule.Name)
	hostLogModels := r.parseRule(rules)
	initConf := r.GlobalConf
	if v, ok := collectionRule.Annotations[types.GlobalConfigAnno]; ok {
		initConf = v
	}
	return r.Collector.NewConfigForHost(hostLogModels, r.Template, types.Parameters{InitConfigPath: initConf, RuleName: collectionRule.Name, Node: r.HostName})
}

func (r *Reconciler) parseRule(rules []v1alpha1.HostCollectionRule) []types.HostLogModel {
	var hostLogModels []types.HostLogModel
	for _, rule := range rules {
		logModel := types.HostLogModel{}
		logModel.Index = rule.Elasticsearch.Index
		for _, p := range rule.Path {
			logModel.LogPath = append(logModel.LogPath, path.Join(r.BaseDir, p))
		}
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
		logModel.UID = rule.UID
		logModel.Tags = append(logModel.Tags, rule.Tags...)
		logModel.Topic = rule.Kafka.Topic
		logModel.ExcludeFiles = rule.ExcludeFiles
		hostLogModels = append(hostLogModels, logModel)
	}
	return hostLogModels
}

// SetupWithManager setup controller to manager
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.HostLogCollectionRule{}).
		Complete(r)
}
