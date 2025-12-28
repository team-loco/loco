package kube

import (
	"log/slog"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	crClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/go-logr/logr"
	locov1alpha1 "github.com/team-loco/loco/controller/api/v1alpha1"
)

// Client implements Kubernetes operations for deployments
type Client struct {
	ClientSet        kubernetes.Interface
	ControllerClient crClient.Client
	Manager          controllerruntime.Manager
	Cache            cache.Cache
	Config           *rest.Config
}

// NewClient initializes a new Kubernetes client based on the application environment.
// For local development, it uses the kubeconfig file. For production, it uses in-cluster config.
// todo: this is being called twice,
func NewClient(appEnv string) *Client {
	slog.Info("Initializing Kubernetes client", "env", appEnv)
	config := buildConfig(appEnv)

	clientSet := buildKubeClientSet(config)
	controllerRuntimeClient := buildControllerRuntimeClient(config)
	manager := buildManager(config)
	return &Client{
		ClientSet:        clientSet,
		ControllerClient: controllerRuntimeClient,
		Manager:          manager,
		Cache:            manager.GetCache(),
		Config:           config,
	}
}

// buildConfig creates a Kubernetes config using in-cluster config for production,
// or local kubeconfig for development.
func buildConfig(appEnv string) *rest.Config {
	if appEnv == "PRODUCTION" {
		slog.Info("Using in-cluster Kubernetes config")
		config, err := rest.InClusterConfig()
		if err != nil {
			slog.Error("Failed to create in-cluster config", "error", err)
			panic(err)
		}
		return config
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		panic(err)
	}
	return config
}

func buildKubeClientSet(config *rest.Config) kubernetes.Interface {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		slog.Error("Failed to create Kubernetes client", "error", err)
		panic(err)
	}

	slog.Info("Kubernetes client initialized")
	return clientSet
}

// ControllerRuntimeClient returns a lazy-initialized controller-runtime client.
// Used for creating custom resources like LocoResource.
func buildControllerRuntimeClient(config *rest.Config) crClient.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(locov1alpha1.AddToScheme(scheme))

	// controller-runtime uses logr, we convert to slog.
	slogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger := logr.FromSlogHandler(slogger.Handler())
	controllerruntime.SetLogger(logger)
	crClient, err := crClient.New(config, crClient.Options{Scheme: scheme})
	if err != nil {
		slog.Error("failed to create controller-runtime client", "error", err)
		panic(err)
	}
	slog.Info("controller-runtime client initialized")
	return crClient
}

func buildManager(config *rest.Config) controllerruntime.Manager {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(locov1alpha1.AddToScheme(scheme))

	mgr, err := controllerruntime.NewManager(config, controllerruntime.Options{
		Scheme:                 scheme,
		Logger:                 logr.FromSlogHandler(slog.Default().Handler()),
		Metrics:                server.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		slog.Error("failed to create controller-runtime manager", "error", err)
		panic(err)
	}
	slog.Info("controller-runtime manager initialized")
	return mgr
}
