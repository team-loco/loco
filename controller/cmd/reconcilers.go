package main

import (
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/team-loco/loco/controller/internal/builds"
	"github.com/team-loco/loco/controller/internal/controller"
)

const (
	reconcilerApplication       = "application"
	reconcilerBuild             = "build"
	applicationLeaderElectionID = "be6ed5b1.loco.io"
	buildLeaderElectionID       = "loco-build-controller.loco.io"
)

type reconcilerSetup struct {
	leaderElectionID string
	cache            cache.Options
	register         func(ctrl.Manager) error
}

func selectReconciler(name string, operator operatorConfig) (reconcilerSetup, error) {
	switch name {
	case reconcilerApplication:
		return applicationSetup(operator), nil
	case reconcilerBuild:
		return buildSetup(operator)
	default:
		return reconcilerSetup{}, fmt.Errorf(
			"--reconciler must be %q or %q, got %q",
			reconcilerApplication,
			reconcilerBuild,
			name,
		)
	}
}

func applicationSetup(operator operatorConfig) reconcilerSetup {
	cacheOptions := controller.CacheOptions(operator.LocoNamespace, operator.PullSecretName)
	register := func(mgr ctrl.Manager) error {
		reconciler := &controller.LocoResourceReconciler{
			Client:                 mgr.GetClient(),
			Scheme:                 mgr.GetScheme(),
			LocoNamespace:          operator.LocoNamespace,
			ObservabilityNamespace: operator.ObservabilityNamespace,
			PullSecretName:         operator.PullSecretName,
		}
		return reconciler.SetupWithManager(mgr)
	}
	return reconcilerSetup{
		leaderElectionID: applicationLeaderElectionID,
		cache:            cacheOptions,
		register:         register,
	}
}

func buildSetup(operator operatorConfig) (reconcilerSetup, error) {
	buildConfig, err := builds.ParseConfig(operator.RawBuildConfig)
	if err != nil {
		return reconcilerSetup{}, fmt.Errorf("build configuration: %w", err)
	}
	cacheOptions := builds.CacheOptions(buildConfig.Namespace)
	register := func(mgr ctrl.Manager) error {
		reconciler := &builds.Reconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			APIReader:      mgr.GetAPIReader(),
			Config:         buildConfig,
			LocoNamespace:  operator.LocoNamespace,
			PullSecretName: operator.PullSecretName,
		}
		return reconciler.SetupWithManager(mgr)
	}
	return reconcilerSetup{
		leaderElectionID: buildLeaderElectionID,
		cache:            cacheOptions,
		register:         register,
	}, nil
}
