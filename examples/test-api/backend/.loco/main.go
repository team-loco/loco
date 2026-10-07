package main

import loco "github.com/team-loco/loco/sdk/go"

func main() {
	loco.Run(func(ctx loco.Context) loco.Stack {
		replicas := int32(1)
		if ctx.EnvironmentType == "production" {
			replicas = 2
		}
		return loco.Stack{Name: "backend", Services: []loco.Service{
			{
				Key:           "backend",
				Build:         &loco.DockerBuild{Context: ".", Dockerfile: "Dockerfile"},
				Routing:       loco.Routing{Port: 8000},
				PrimaryRegion: "us-east-1",
				Health:        loco.Health{Path: "/"},
				Domain:        &loco.PlatformDomain{Hostname: "backend-" + ctx.Environment + ".onloco.app"},
				Regions: map[string]loco.Region{
					"us-east-1": {CPU: "100m", Memory: "256Mi", ReplicasMin: replicas, ReplicasMax: replicas},
				},
			},
		}}
	})
}
