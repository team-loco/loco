package main

import loco "github.com/team-loco/loco/sdk/go"

func main() {
	loco.Run(func(ctx *loco.Context) *loco.Stack {
		replicas := int32(1)
		if ctx.EnvironmentType == "production" {
			replicas = 2
		}
		return &loco.Stack{Name: "auth", Services: []*loco.Service{{
			Key: "auth", Source: loco.Docker(".", "Dockerfile"),
			Hostname: "auth-" + ctx.Environment + ".onloco.app",
			Spec: loco.Config(&loco.ServiceSpec{
				Routing:     &loco.Routing{Port: 8000},
				HealthCheck: &loco.Health{Path: "/auth/health"},
				Regions: map[string]*loco.Region{
					"us-east-1": {
						Enabled:     true,
						Primary:     true,
						Cpu:         "100m",
						Memory:      "256Mi",
						MinReplicas: replicas,
						MaxReplicas: replicas,
					},
				},
			}),
		}}}
	})
}
