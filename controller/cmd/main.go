package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/team-loco/loco/controller/internal/capacity"
	"github.com/team-loco/loco/internal/capacitymetrics"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/controller/internal/tracing"
	"github.com/team-loco/loco/internal/buildinfo"
	"github.com/team-loco/loco/internal/otelsetup"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))

	utilruntime.Must(locov1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

var version string

func main() {
	var enableLeaderElection bool
	var probeAddr string
	var metricsAddr string
	var pprofAddr string
	var reconcilerName string
	flag.StringVar(&reconcilerName, "reconciler", "",
		"The reconciler this manager runs: "+reconcilerApplication+" or "+reconcilerBuild+".")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the Prometheus metrics endpoint binds to. Use 0 to disable it.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&pprofAddr, "pprof-bind-address", "0",
		"The address the pprof endpoint binds to, e.g. :6060. Leave as 0 to disable pprof.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	managerVersion := buildinfo.Version(version)
	setupLog.Info("starting loco controller", "version", managerVersion)

	operator := newOperatorConfig()
	setup, err := selectReconciler(reconcilerName, operator)
	if err != nil {
		setupLog.Error(err, "invalid reconciler configuration")
		os.Exit(1)
	}
	restConfig := ctrl.GetConfigOrDie()
	var stops []func()
	flush := func() {
		for _, stop := range stops {
			stop()
		}
	}
	if operator.Tracing {
		provider, tracingErr := otelsetup.Start(context.Background(), setup.serviceName, managerVersion)
		if tracingErr != nil {
			setupLog.Error(tracingErr, "unable to start tracing")
			os.Exit(1)
		}
		stops = append(stops, func() {
			if stopErr := otelsetup.Stop(provider); stopErr != nil {
				setupLog.Error(stopErr, "unable to stop tracing")
			}
		})
		tracing.TraceRequests(restConfig)
	}
	if operator.Metrics {
		provider, metricsErr := otelsetup.StartMetrics(
			context.Background(), setup.serviceName, managerVersion, registryMetrics(),
		)
		if metricsErr != nil {
			setupLog.Error(metricsErr, "unable to start metrics")
			flush()
			os.Exit(1)
		}
		stops = append(stops, func() {
			if stopErr := otelsetup.StopMetrics(provider); stopErr != nil {
				setupLog.Error(stopErr, "unable to stop metrics")
			}
		})
	}
	fail := func(err error, msg string, keysAndValues ...any) {
		setupLog.Error(err, msg, keysAndValues...)
		flush()
		os.Exit(1)
	}
	ctx, onWatchError := stopOnDeadWatch(ctrl.SetupSignalHandler())
	setup.cache.DefaultWatchErrorHandler = onWatchError
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Cache:                  setup.cache,
		Metrics:                serveMetrics(metricsAddr),
		HealthProbeBindAddress: probeAddr,
		PprofBindAddress:       pprofAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       setup.leaderElectionID,
		LeaseDuration:          new(60 * time.Second),
		RenewDeadline:          new(45 * time.Second),
		RetryPeriod:            new(5 * time.Second),
	})
	if err != nil {
		fail(err, "unable to start manager")
	}

	applicationMetrics := operator.Metrics && reconcilerName == reconcilerApplication
	if applicationMetrics && metricsAddr != metricsOff {
		if err := mgr.AddMetricsServerExtraHandler(
			capacitymetrics.Path,
			capacity.Handler(mgr.GetAPIReader(), mgr.Elected()),
		); err != nil {
			fail(err, "unable to serve pod capacity metrics")
		}
	}
	if err := setup.register(mgr); err != nil {
		fail(err, "unable to create controller", "controller", reconcilerName)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		fail(err, "unable to set up health check")
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		fail(err, "unable to set up ready check")
	}

	setupLog.Info("starting manager", "reconciler", reconcilerName)
	if err := startManager(ctx, mgr); err != nil {
		fail(err, "problem running manager")
	}
	flush()
}
