package annotation_watch

import (
	"context"
	"fmt"
	"harmonycloud.cn/log-collector/pkg/apis/logcollector/v1alpha1"
	v1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

type StatefulSetReconciler struct {
	client.Client
	Cache cache.Indexer
}

func (r *StatefulSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	instance := &v1.StatefulSet{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{Requeue: true, RequeueAfter: 30 * time.Second}, err
	}
	config, has := HasLogAnnotation(instance.Annotations)
	if !has {
		return ctrl.Result{}, nil
	}
	rule := &v1alpha1.LogCollectionRule{}
	ruleName := instance.Name + LogSuffix
	err = r.Client.Get(ctx, types.NamespacedName{Name: ruleName}, rule)
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Error(err)
		return ctrl.Result{}, err
	}
	if rule.Name != "" {
		klog.Info(fmt.Sprintf("logrule for deployment %v/%v is already exists", instance.Name, instance.Namespace))
		return ctrl.Result{}, nil
	}
	labels := instance.Spec.Selector.MatchLabels
	parsedRule, err := ParseAnnotationToRule(config, ruleName, instance.Namespace, labels)
	if err != nil {
		klog.Error(fmt.Sprintf("error when parse rule for deployment %s/%s:%v", instance.Name, instance.Namespace, err))
		return ctrl.Result{}, err
	}
	err = r.Client.Create(ctx, parsedRule)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil

}

// SetupWithManager setup controller to manager
func (r *StatefulSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.StatefulSet{}).
		Complete(r)
}
