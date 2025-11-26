package main

import (
	"flag"
	"fmt"
	"harmonycloud.cn/log-collector/pkg/adapter"
	"harmonycloud.cn/log-collector/pkg/apis/logcollector/v1alpha1"
	"harmonycloud.cn/log-collector/pkg/controller/host_rule"
	"harmonycloud.cn/log-collector/pkg/controller/rule"
	"harmonycloud.cn/log-collector/pkg/types"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

var (
	runScheme = runtime.NewScheme()
)

var (
	baseDir      string
	template     string
	globalConfig string
	probeAddr    string
	collector    string
	runTime      string
	metricsAddr  string
)

func init() {
	flag.StringVar(&template, "template", "/template.config", "Template config file for collector.")
	flag.StringVar(&globalConfig, "globalConfig", "/global.config", "Global config file for collector.")
	flag.StringVar(&baseDir, "baseDir", "/host", "Directory which mount host root.")
	flag.StringVar(&collector, "collector", "filebeat", "Collector to use,default is filebeat.")
	flag.StringVar(&probeAddr, "health-probe-addr", ":9001", "The address the probe endpoint binds to.")
	flag.StringVar(&runTime, "runtime", "containerd", "Container run time,default is containerd.")
	flag.StringVar(&metricsAddr, "metrics-addr", "0", "The address the metrics endpoint binds to.")

	_ = v1alpha1.AddToScheme(runScheme)
	_ = clientgoscheme.AddToScheme(runScheme)
}

func main() {
	flag.Parse()
	if template == "" {
		klog.Error(fmt.Sprintf("must set template file"))
		os.Exit(1)
	}
	temp, err := os.ReadFile(template)
	if err != nil {
		klog.Error(fmt.Sprintf("read template file err"))
		os.Exit(1)
	}
	baseDir, err = filepath.Abs(baseDir)
	if err != nil {
		klog.Error(fmt.Sprintf("get base directory err"))
		os.Exit(1)
	}

	if baseDir == "/" {
		baseDir = ""
	}
	hostname := os.Getenv("HOSTNAME")
	klog.Info("hostname: ", hostname)
	klog.Info("runtime: ", runTime)
	podName := os.Getenv("PODNAME")
	podNamespace := os.Getenv("PODNAMESPACE")
	cfg := types.DefaultConfiguration()
	cfg.HostName = hostname
	cfg.Template = string(temp)
	cfg.BaseDir = baseDir
	cfg.GlobalConf = globalConfig
	cfg.NamespacedName.Name = podName
	cfg.NamespacedName.Namespace = podNamespace
	restCfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 runScheme,
		Logger:                 ctrl.Log.WithName("log-controller"),
		HealthProbeBindAddress: probeAddr,
		MetricsBindAddress:     metricsAddr,
	})
	if err != nil {
		klog.Fatalf(fmt.Sprintf("failed create manager: %s", err))
	}

	if err := setupControllers(mgr, cfg); err != nil {
		klog.Fatalf(fmt.Sprintf("failed to create controller: %s", err))
	}
	go func() {
		<-mgr.Elected()
		go func() {
			err := adapter.GetCollector(collector).Start()
			if err != nil && types.ERR_ALREADY_STARTED != err.Error() {
				klog.Fatalf("cannot start filebeat: %v", err)
			}
		}()
	}()
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		klog.Fatalf("failed to setup health check")
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		klog.Fatalf("failed to setup ready check")
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Fatalf(fmt.Sprintf("failed running manager: %s", err))
	}
}

func setupControllers(mgr ctrl.Manager, cfg *types.Configuration) error {
	if err := (&rule.Reconciler{
		Client:         mgr.GetClient(),
		Cache:          cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}),
		Template:       cfg.Template,
		BaseDir:        cfg.BaseDir,
		GlobalConf:     cfg.GlobalConf,
		HostName:       cfg.HostName,
		NamespacedName: cfg.NamespacedName,
		Collector:      adapter.GetCollector(collector),
		Runtime:        runTime,
	}).SetupWithManager(mgr); err != nil {
		klog.Errorf(fmt.Sprintf("failed to run Reconciler: %s", err))
		return err
	}
	if err := (&host_rule.Reconciler{
		Client:         mgr.GetClient(),
		Template:       cfg.Template,
		BaseDir:        cfg.BaseDir,
		GlobalConf:     cfg.GlobalConf,
		HostName:       cfg.HostName,
		NamespacedName: cfg.NamespacedName,
		Collector:      adapter.GetCollector(collector),
		Runtime:        runTime,
	}).SetupWithManager(mgr); err != nil {
		klog.Errorf(fmt.Sprintf("failed to run Reconciler: %s", err))
		return err
	}
	/*	if err := (&annotation_watch.DeployReconciler{
			Client: mgr.GetClient(),
		}).SetupWithManager(mgr); err != nil {
			klog.Errorf(fmt.Sprintf("failed to run Reconciler: %s", err))
			return err
		}
		if err := (&annotation_watch.DaemonSetReconciler{
			Client: mgr.GetClient(),
		}).SetupWithManager(mgr); err != nil {
			klog.Errorf(fmt.Sprintf("failed to run Reconciler: %s", err))
			return err
		}
		if err := (&annotation_watch.StatefulSetReconciler{
			Client: mgr.GetClient(),
		}).SetupWithManager(mgr); err != nil {
			klog.Errorf(fmt.Sprintf("failed to run Reconciler: %s", err))
			return err
		}*/
	return nil
}
